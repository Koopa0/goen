package probe

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubPinger struct{ err error }

func (p stubPinger) Ping(context.Context) error { return p.err }

func TestReadinessDependsOnTheStorefrontPool(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{"reachable", nil, http.StatusOK, "ready\n"},
		{"unreachable", errors.New("connection refused"), http.StatusServiceUnavailable, "storefront database unreachable\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := NewHandler(slog.New(slog.DiscardHandler), stubPinger{err: tc.err})
			res := httptest.NewRecorder()
			h.Ready(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
			if res.Code != tc.wantStatus || res.Body.String() != tc.wantBody {
				t.Errorf("Ready = %d %q, want %d %q", res.Code, res.Body.String(), tc.wantStatus, tc.wantBody)
			}
			if res.Header().Get("Cache-Control") != "no-store" || res.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
				t.Errorf("Ready headers = %v, want uncached plain text", res.Header())
			}
		})
	}
}

func TestLivenessIgnoresTheDatabase(t *testing.T) {
	t.Parallel()
	h := NewHandler(slog.New(slog.DiscardHandler), stubPinger{err: errors.New("down")})
	res := httptest.NewRecorder()
	h.Live(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	if res.Code != http.StatusOK || res.Body.String() != "ok\n" {
		t.Errorf("Live with the database down = %d %q, want 200 ok", res.Code, res.Body.String())
	}
}
