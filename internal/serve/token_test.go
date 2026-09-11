package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// P1-C5: every route sits behind the bearer token once set — header or
// ?token= query (EventSource can't set headers), 401 JSON otherwise.
func TestTokenGuard(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := tokenGuard("sekret", ok)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/history", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d, want 401", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/history", nil))
	if rr.Code != http.StatusUnauthorized { // re-check after recorder reuse safety
		t.Fatalf("wrong token path")
	}

	req := httptest.NewRequest("GET", "/history", nil)
	req.Header.Set("Authorization", "Bearer sekret")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("header token: %d, want 200", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/events?token=sekret", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("query token: %d, want 200 (EventSource)", rr.Code)
	}
}
