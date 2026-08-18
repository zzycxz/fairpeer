package netdev

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/netdev/transport"
)

// Assessment mode (NETDEV_SPEC §6.2): gated by the engagement envelope —
// id + scopes + expiry + approver in the USER config. Weak-credential checking
// is tiered (Appendix B-9): "basic" runs a fixed small candidate set within
// the per-device budget; "dictionary" consumes a USER-SUPPLIED dictionary
// (fairpeer ships none) and needs the explicit larger budget. Every attempt
// is a full SSH auth dial and is audited; devices lock accounts after N
// failures, so budgets are hard caps, not suggestions.

// AssessmentActive validates the engagement envelope. nil = active.
func AssessmentActive(nd config.NetDevConfig) error {
	if strings.TrimSpace(nd.Assessment.EngagementID) == "" {
		return errors.New("assessment mode requires an engagement envelope ([netdev.assessment] engagement_id)")
	}
	exp := strings.TrimSpace(nd.Assessment.Expires)
	if exp == "" {
		return errors.New("assessment engagement has no expiry — set [netdev.assessment] expires")
	}
	t, err := time.ParseInLocation("2006-01-02", exp, time.Local)
	if err != nil {
		return fmt.Errorf("assessment expires %q: want YYYY-MM-DD", exp)
	}
	if time.Now().After(t.AddDate(0, 0, 1)) {
		return fmt.Errorf("assessment engagement %s expired on %s", nd.Assessment.EngagementID, exp)
	}
	return nil
}

// Weak-credential tiers.
const (
	WeakTierBasic     = "basic"      // fixed vendor-default/empty/username set
	WeakTierDict      = "dictionary" // user-supplied dictionary file
	weakBudgetBasic   = 3
	weakBudgetDictMax = 10 // hard cap even for dictionary tier (lockout guard)
)

// basicCandidates is the fixed basic-tier set (<= weakBudgetBasic entries).
func basicCandidates(username string) []string {
	set := []string{"", username, "admin"}
	out := make([]string, 0, weakBudgetBasic)
	seen := map[string]bool{}
	for _, c := range set {
		if !seen[c] && len(out) < weakBudgetBasic {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// WeakCredResult reports one device's check.
type WeakCredResult struct {
	Device   string `json:"device"`
	Tier     string `json:"tier"`
	Weak     bool   `json:"weak"`
	Attempts int    `json:"attempts"`
	Budget   int    `json:"budget"`
	Detail   string `json:"detail,omitempty"`
}

// WeakCredCheck tests a device's login against the tier's candidate set.
// Gated on the engagement envelope; every attempt audited (password NEVER
// logged — only the attempt ordinal).
func (m *Manager) WeakCredCheck(ctx context.Context, deviceName, tier, dictPath string) (WeakCredResult, error) {
	if err := AssessmentActive(m.cfg.NetDev); err != nil {
		return WeakCredResult{}, fmt.Errorf("assess mode gate: %w", err)
	}
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		return WeakCredResult{}, fmt.Errorf("device %q not in inventory", deviceName)
	}

	var candidates []string
	budget := weakBudgetBasic
	switch tier {
	case "", WeakTierBasic:
		tier = WeakTierBasic
		candidates = basicCandidates(strings.TrimSpace(d.Username))
	case WeakTierDict:
		tier = WeakTierDict
		lines, err := readDict(dictPath)
		if err != nil {
			return WeakCredResult{}, err
		}
		candidates = lines
		budget = weakBudgetDictMax
		if len(candidates) > budget {
			candidates = candidates[:budget]
		}
	default:
		return WeakCredResult{}, fmt.Errorf("tier must be basic|dictionary")
	}

	res := WeakCredResult{Device: deviceName, Tier: tier, Budget: budget}
	for i, cand := range candidates {
		res.Attempts = i + 1
		ok, err := m.dialAuth(ctx, d, cand)
		status := AuditOK
		detail := "attempt " + fmt.Sprint(i+1) + " rejected"
		if err != nil {
			status = AuditFailure
			detail = "attempt " + fmt.Sprint(i+1) + " transport error: " + err.Error()
		} else if ok {
			res.Weak = true
			res.Detail = fmt.Sprintf("weak credential confirmed after %d attempt(s) — change it via a proposal", res.Attempts)
			_ = AppendAudit(Audit{Device: deviceName, Command: "weak-cred-check (" + tier + ") attempt " + fmt.Sprint(i+1) + ": CONFIRMED", Class: "assess", Status: AuditDeviceError})
			return res, nil
		}
		_ = AppendAudit(Audit{Device: deviceName, Command: "weak-cred-check (" + tier + ") attempt " + fmt.Sprint(i+1), Class: "assess", Status: status, Error: detail})
	}
	res.Detail = fmt.Sprintf("no weak credential in %d attempt(s) (budget %d)", res.Attempts, budget)
	return res, nil
}

// dialAuth attempts one SSH login with the candidate password. Host keys ride
// the normal TOFU policy (HostKeyPrompt / managed file).
func (m *Manager) dialAuth(ctx context.Context, d config.NetDevDevice, password string) (bool, error) {
	lookup := m.lookupEntry()
	resolved, err := transport.ResolveHost(lookup, d.Name, nil)
	if err != nil {
		return false, err
	}
	// Devices behind bastions must be checked through their route too — the
	// jump hosts authenticate with their OWN stored credentials, never with
	// the candidate under test.
	jumps, err := transport.ResolveJumpHosts(lookup, d.Via, nil)
	if err != nil {
		return false, err
	}
	hops := make([]transport.JumpHostOptions, 0, len(jumps))
	for i, j := range jumps {
		hopCfg := m.hopByRaw(d.Via[i])
		hops = append(hops, transport.JumpHostOptions{Host: j, Auth: transport.AuthOptions{
			Password:   secretReader(SecretKindPassword, hopCfg.PasswordEnv),
			Passphrase: secretReader(SecretKindPassphrase, hopCfg.PassphraseEnv),
		}})
	}
	client, err := transport.New(transport.Options{
		Host:        resolved,
		Auth:        transport.AuthOptions{Password: func() (string, error) { return password, nil }},
		JumpHosts:   hops,
		HostKeys:    &transport.HostKeyPolicy{Prompt: HostKeyPrompt, ManagedPath: transport.ManagedKnownHostsOverride},
		DialTimeout: 8 * time.Second,
	})
	if err != nil {
		return false, err
	}
	if err := client.Start(ctx); err != nil {
		if errors.Is(err, transport.ErrAuthFailed) {
			client.Close()
			return false, nil // candidate rejected — expected outcome
		}
		client.Close()
		return false, nil // transient errors count as a spent attempt (conservative)
	}
	client.Close()
	return true, nil // LOGIN SUCCEEDED with the candidate — weak
}

func readDict(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("dictionary: %w", err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("dictionary %s has no candidates", path)
	}
	return out, sc.Err()
}
