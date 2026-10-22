package requestid_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/web/requestid"
)

// The echoed request ID is the one on the context. It binds the two ends of one
// identifier: the header a caller correlates on, and the context value the
// audit trail stamps onto a back-office row. They are one key, so a row and its
// log lines can be put together.
func TestHandler(t *testing.T) {
	tests := []struct {
		name     string
		supplied string
		want     string
	}{
		{name: "an id-shaped header is honoured", supplied: "abc-123", want: "abc-123"},
		{name: "one carrying a newline is replaced", supplied: "forged\nlog line"},
		{name: "an absent header is generated"},
		{name: "one of 64 characters is honoured", supplied: strings.Repeat("a", 64), want: strings.Repeat("a", 64)},
		{name: "one of 65 characters is replaced", supplied: strings.Repeat("a", 65)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var onContext string
			h := requestid.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				onContext = requestid.From(r.Context())
			}))
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tt.supplied != "" {
				req.Header.Set("X-Request-Id", tt.supplied)
			}
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)

			echoed := res.Header().Get("X-Request-Id")
			switch {
			case echoed == "":
				t.Fatal("no X-Request-Id echoed")
			case tt.want != "" && echoed != tt.want:
				t.Errorf("echoed X-Request-Id = %q, want %q", echoed, tt.want)
			case tt.want == "" && echoed == tt.supplied:
				t.Errorf("echoed X-Request-Id = %q, want the supplied one replaced", echoed)
			}
			if onContext != echoed {
				t.Errorf("requestid.From on the context = %q, echoed %q", onContext, echoed)
			}
		})
	}
}
