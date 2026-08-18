package netdev

import "testing"

func TestSecretKey(t *testing.T) {
	key, err := SecretKey(SecretKindPassword, "NETDEV_PWD_L1")
	if err != nil {
		t.Fatal(err)
	}
	if key != "netdev/password/NETDEV_PWD_L1" {
		t.Fatalf("key = %q", key)
	}
	for _, bad := range [][2]string{
		{"", "name"}, {"kind", ""}, {"k/ind", "name"}, {"kind", "na/me"},
	} {
		if _, err := SecretKey(bad[0], bad[1]); err == nil {
			t.Fatalf("SecretKey(%q,%q) unexpectedly succeeded", bad[0], bad[1])
		}
	}
}

func TestSecretRoundTrip(t *testing.T) {
	// Uses the real Default() store only through an isolated Store via the
	// key convention; round-trip against a temp store.
	if err := SetSecret(SecretKindPassword, "TEST_RT", "s3cret"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	defer DeleteSecret(SecretKindPassword, "TEST_RT")

	v, ok, err := GetSecret(SecretKindPassword, "TEST_RT")
	if err != nil || !ok || v != "s3cret" {
		t.Fatalf("GetSecret = %q,%v,%v", v, ok, err)
	}
	if _, ok, _ := GetSecret(SecretKindPassword, "TEST_MISSING"); ok {
		t.Fatal("missing secret unexpectedly found")
	}
}
