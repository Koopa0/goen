package health

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubPinger is a Pinger that answers however the test says.
type stubPinger struct{ err error }

func (p stubPinger) Ping(context.Context) error { return p.err }

// TestReadinessFailsWhenAnyPoolIsUnreachable holds the probe against every pool
// goen serves from, not just the first.
func TestReadinessFailsWhenAnyPoolIsUnreachable(t *testing.T) {
	down := errors.New("dial tcp 127.0.0.1:59999: connect: connection refused")

	tests := []struct {
		name       string
		storefront error
		admin      error
		wantStatus int
		wantBody   string
	}{
		{
			name: "both reachable", wantStatus: http.StatusOK, wantBody: "ready",
		},
		{
			name: "storefront down", storefront: down,
			wantStatus: http.StatusServiceUnavailable, wantBody: "storefront",
		},
		{
			// The case that shipped: everything the probe looked at was fine.
			name: "admin down", admin: down,
			wantStatus: http.StatusServiceUnavailable, wantBody: "admin",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(slog.New(slog.DiscardHandler),
				Dependency{Name: "storefront", DB: stubPinger{err: tt.storefront}},
				Dependency{Name: "admin", DB: stubPinger{err: tt.admin}},
			)

			w := httptest.NewRecorder()
			h.Ready(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", http.NoBody))

			if w.Code != tt.wantStatus {
				t.Errorf("Ready() status = %d, want %d", w.Code, tt.wantStatus)
			}
			// The body NAMES the pool.
			if body := w.Body.String(); !strings.Contains(body, tt.wantBody) {
				t.Errorf("Ready() body = %q, want it to mention %q", body, tt.wantBody)
			}
		})
	}
}

// TestLivenessIgnoresTheDatabase keeps the two probes different questions:
// answering a brief outage as liveness turns a blip into a restart loop.
func TestLivenessIgnoresTheDatabase(t *testing.T) {
	h := NewHandler(slog.New(slog.DiscardHandler),
		Dependency{Name: "storefront", DB: stubPinger{err: errors.New("down")}},
	)

	w := httptest.NewRecorder()
	h.Live(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", http.NoBody))

	if w.Code != http.StatusOK {
		t.Errorf("Live() status = %d with the database down, want %d — a liveness "+
			"probe that tests the database restarts a healthy process", w.Code, http.StatusOK)
	}
}
