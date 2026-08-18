package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/crypto/ssh"
)

// isolatedKeyPolicy builds a HostKeyPolicy that never touches the real
// ~/.ssh/known_hosts: system files point at a nonexistent temp path, and the
// managed TOFU file lives under t.TempDir().
func isolatedKeyPolicy(t *testing.T, prompt HostKeyPrompt) *HostKeyPolicy {
	t.Helper()
	return &HostKeyPolicy{
		SystemKnownHosts: []string{filepath.Join(t.TempDir(), "no-such-file")},
		ManagedPath:      filepath.Join(t.TempDir(), "known_hosts"),
		Prompt:           prompt,
	}
}

func genHostKey(t *testing.T) (ed25519.PrivateKey, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return priv, signer.PublicKey()
}

func TestKnownHostsStrictRejectWithoutPrompt(t *testing.T) {
	_, addr := newSSHTestServer(t, "pw", false)
	host, port := splitHostPort(t, addr)
	policy := isolatedKeyPolicy(t, nil) // nil prompt = strict

	_, hops, err := dialSSH(context.Background(), dialConfig{
		host:        ResolvedHost{HostName: host, Port: port, User: "u"},
		auth:        &AuthOptions{Password: func() (string, error) { return "pw", nil }},
		hostKeys:    policy,
		dialTimeout: testDialTimeout(),
	})
	if err == nil {
		closeAll(hops)
		t.Fatal("dial without TOFU prompt unexpectedly succeeded")
	}
	if !errors.Is(err, ErrHostKeyRejected) {
		t.Fatalf("err = %v, want ErrHostKeyRejected", err)
	}
}

func TestKnownHostsTOFUAcceptThenSilentReuse(t *testing.T) {
	_, addr := newSSHTestServer(t, "pw", false)
	host, port := splitHostPort(t, addr)

	prompts := 0
	policy := isolatedKeyPolicy(t, func(ctx context.Context, q HostKeyQuestion) (bool, error) {
		prompts++
		return true, nil
	})
	auth := &AuthOptions{Password: func() (string, error) { return "pw", nil }}
	rh := ResolvedHost{HostName: host, Port: port, User: "u"}

	cl, hops, err := dialSSH(context.Background(), dialConfig{host: rh, auth: auth, hostKeys: policy, dialTimeout: testDialTimeout()})
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	cl.Close()
	closeAll(hops)

	// Second dial: the accepted key is in the managed file; no prompt.
	if _, _, err := dialSSH(context.Background(), dialConfig{host: rh, auth: auth, hostKeys: policy, dialTimeout: testDialTimeout()}); err != nil {
		t.Fatalf("second dial: %v", err)
	}
	if prompts != 1 {
		t.Fatalf("prompts = %d, want exactly 1 (first use)", prompts)
	}

	// The managed file exists and is non-empty.
	data, err := os.ReadFile(policy.ManagedPath)
	if err != nil || len(data) == 0 {
		t.Fatalf("managed known_hosts not written: %v", err)
	}
}

func TestKnownHostsMismatchIsHardFail(t *testing.T) {
	_, addr := newSSHTestServer(t, "pw", false)
	host, port := splitHostPort(t, addr)

	// Record a DIFFERENT key for this host in the managed file.
	_, decoyPub := genHostKey(t)
	managed := filepath.Join(t.TempDir(), "known_hosts")
	line := ssh.MarshalAuthorizedKey(decoyPub)
	header := "[" + host + "]:" + strconv.Itoa(port) + " "
	if err := os.WriteFile(managed, append([]byte(header), line...), 0o600); err != nil {
		t.Fatal(err)
	}

	prompted := false
	policy := &HostKeyPolicy{
		SystemKnownHosts: []string{filepath.Join(t.TempDir(), "no-such-file")},
		ManagedPath:      managed,
		Prompt: func(ctx context.Context, q HostKeyQuestion) (bool, error) {
			prompted = true
			return true, nil
		},
	}
	_, hops, err := dialSSH(context.Background(), dialConfig{
		host:        ResolvedHost{HostName: host, Port: port, User: "u"},
		auth:        &AuthOptions{Password: func() (string, error) { return "pw", nil }},
		hostKeys:    policy,
		dialTimeout: testDialTimeout(),
	})
	if err == nil {
		closeAll(hops)
		t.Fatal("dial with mismatched host key unexpectedly succeeded")
	}
	var mismatch *HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want HostKeyMismatchError", err)
	}
	if prompted {
		t.Fatal("a mismatch must never be promptable")
	}
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatal("mismatch must unwrap to ErrHostKeyMismatch")
	}
}
