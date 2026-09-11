package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultMaxRetries is the default number of times SendWithRetry re-attempts
// the connection + header phase after the initial try (so up to N+1 total
// attempts).
const DefaultMaxRetries = 10

// defaultMaxBackoff caps the exponential backoff.
const defaultMaxBackoff = 15 * time.Second

// RetryPolicy is the package-wide retry configuration (P1-A3). MaxRetries <= 0
// falls back to DefaultMaxRetries; MaxBackoff <= 0 to defaultMaxBackoff.
// Mode "always" lifts the attempt cap entirely for retryable failures — for
// unattended multi-hour runs, a half-dead gateway then gets waited out instead
// of killing the turn once the budget is spent (the runtime-proven failure
// mode: budget exhausted → TurnDone error). Values are read once per attempt,
// so a config reload applies to in-flight requests on their next retry.
type RetryPolicy struct {
	MaxRetries int
	MaxBackoff time.Duration
	Mode       string // "" | "normal" | "always"
}

var (
	policyMu    sync.RWMutex
	policyCur   = RetryPolicy{MaxRetries: DefaultMaxRetries, MaxBackoff: defaultMaxBackoff, Mode: "normal"}
	retryPolicy = func() RetryPolicy {
		policyMu.RLock()
		defer policyMu.RUnlock()
		return policyCur
	}
)

// SetRetryPolicy installs the package-wide retry configuration. Zero value
// restores defaults.
func SetRetryPolicy(p RetryPolicy) {
	if p.MaxRetries <= 0 {
		p.MaxRetries = DefaultMaxRetries
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = defaultMaxBackoff
	}
	if strings.EqualFold(p.Mode, "always") {
		p.Mode = "always"
	} else {
		p.Mode = "normal"
	}
	policyMu.Lock()
	policyCur = p
	policyMu.Unlock()
}

func maxRetries() int {
	if p := retryPolicy(); p.Mode == "always" {
		// A very large but finite cap keeps attempt counters sane in UI strings.
		return 1_000_000
	}
	if n := retryPolicy().MaxRetries; n > 0 {
		return n
	}
	return DefaultMaxRetries
}

func maxBackoffCap() time.Duration {
	if d := retryPolicy().MaxBackoff; d > 0 {
		return d
	}
	return defaultMaxBackoff
}

// maxAuthRetries is the number of times a 401/403 is retried when the key has
// previously authenticated successfully (transient auth failures under load).
const maxAuthRetries = 2

// SendOptions configures SendWithRetry's behaviour.
type SendOptions struct {
	ProvName   string // provider instance name for error messages
	KeyEnv     string // api_key_env for AuthError
	KeyPresent bool   // a non-empty key was configured
	RetryAuth  bool   // retry 401/403 up to maxAuthRetries (key previously worked)
}

// RetryInfo describes a backoff about to happen: Attempt is the 1-based retry
// number (of Max) and Delay is how long SendWithRetry will wait before it.
type RetryInfo struct {
	Attempt int
	Max     int
	Delay   time.Duration
	Err     error
}

type RetryNotify func(RetryInfo)

type retryNotifyKey struct{}

// WithRetryNotify attaches a callback that SendWithRetry invokes before each
// backoff sleep, so the agent can surface a transient "retrying (n/m)" status.
func WithRetryNotify(ctx context.Context, fn RetryNotify) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, retryNotifyKey{}, fn)
}

func retryNotifyFromContext(ctx context.Context) RetryNotify {
	fn, _ := ctx.Value(retryNotifyKey{}).(RetryNotify)
	return fn
}

// APIError reports a non-OK HTTP status that isn't an auth failure. Status
// carries the code so the display layer can map it to an actionable, localized
// message; Body is a trimmed snippet of the response.
type APIError struct {
	Provider string
	Status   int
	Body     string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s: status %d", e.Provider, e.Status)
	}
	return fmt.Sprintf("%s: status %d: %s", e.Provider, e.Status, e.Body)
}

// RetryableStatus reports whether a backoff can plausibly recover from status s:
// 408 (request timeout), 429 (rate limit) and 5xx (incl. Anthropic's 529). Other
// 4xx (400/401/402/422, …) are caller/config problems retrying can't fix —
// except the relay-drop 400s caught by IsTransientGatewayBody.
func RetryableStatus(s int) bool {
	return s == http.StatusRequestTimeout || s == http.StatusTooManyRequests || (s >= 500 && s <= 599)
}

// gatewayTransientBodies are relay-failure texts that arrive with a 400 even
// though the caller's request is fine: the gateway lost ITS upstream connection
// mid-request. Observed in the wild on xiaomimimo's OpenAI-compatible gateway
// ({"code":"400","message":"Request failed","param":"Connection prematurely
// closed BEFORE response"}), 2026-09-04. Extend the list as new gateways
// misbehave the same way.
var gatewayTransientBodies = []string{
	"connection prematurely closed",
	"connection reset by peer",
}

// IsTransientGatewayBody reports whether an error body carries one of the
// known relay-drop signatures — retryable regardless of the HTTP status it
// rode in on.
func IsTransientGatewayBody(body string) bool {
	b := strings.ToLower(body)
	for _, sig := range gatewayTransientBodies {
		if strings.Contains(b, sig) {
			return true
		}
	}
	return false
}

func transientErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// IsConnReset reports whether err is a connection-level drop (peer reset,
// truncated body, closed socket) as opposed to a protocol or caller error. A
// stream cut this way mid-body can be replayed from scratch, unlike a decode or
// 4xx error. The common trigger is a local proxy (v2rayN/sing-box) idle-closing
// the long-lived SSE connection during a reasoner's first-token gap.
func IsConnReset(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func backoffDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > maxBackoffCap() {
			return maxBackoffCap()
		}
		return retryAfter
	}
	d := time.Duration(1<<(attempt-1)) * 500 * time.Millisecond
	if d > maxBackoffCap() {
		d = maxBackoffCap()
	}
	return d + time.Duration(rand.Intn(250))*time.Millisecond
}

func parseRetryAfter(resp *http.Response) time.Duration {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// SendWithRetry POSTs a streaming request built by newReq and returns the OK
// response. It retries the connection+header phase up to MaxRetries times on
// transient network errors and retryable statuses with capped exponential
// backoff + jitter, honoring Retry-After. 401/403 become *AuthError; other
// non-OK statuses become *APIError. A RetryNotify in ctx fires before each
// sleep. Retries cover only the header phase — once the body streams, mid-stream
// failures are not retried (the model has already emitted tokens).
func SendWithRetry(ctx context.Context, httpClient *http.Client, opts SendOptions, newReq func(context.Context) (*http.Request, error)) (*http.Response, error) {
	provName := opts.ProvName
	keyEnv := opts.KeyEnv
	notify := retryNotifyFromContext(ctx)
	var lastErr error
	var retryAfter time.Duration
	authRetries := 0

	for attempt := 0; attempt <= maxRetries(); attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt, retryAfter)
			if notify != nil {
				notify(RetryInfo{Attempt: attempt, Max: maxRetries(), Delay: delay, Err: lastErr})
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		retryAfter = 0

		req, err := newReq(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: build request: %w", provName, err)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			if !transientErr(err) {
				return nil, fmt.Errorf("%s: request failed: %w", provName, err)
			}
			lastErr = fmt.Errorf("%s: request failed: %w", provName, err)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}

		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		retryAfter = parseRetryAfter(resp)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			// Transient auth retry: if the key has previously authenticated
			// successfully, retry up to maxAuthRetries times before giving up.
			// Gateways occasionally return transient 401s under load.
			if opts.RetryAuth && authRetries < maxAuthRetries {
				authRetries++
				lastErr = &AuthError{Provider: provName, KeyEnv: keyEnv, Status: resp.StatusCode, HasKey: opts.KeyPresent}
				continue
			}
			return nil, &AuthError{Provider: provName, KeyEnv: keyEnv, Status: resp.StatusCode, HasKey: opts.KeyPresent}
		}
		apiErr := &APIError{Provider: provName, Status: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
		// Relay-drop 400: the gateway's upstream died, not our request — same
		// class as a 502, so it takes the retry path instead of failing fast.
		if !RetryableStatus(resp.StatusCode) && !(resp.StatusCode == http.StatusBadRequest && IsTransientGatewayBody(apiErr.Body)) {
			return nil, apiErr
		}
		lastErr = apiErr
	}
	return nil, lastErr
}
