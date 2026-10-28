package respond

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/web/pagestatus"
)

// A component of an abandoned request is silent: a departed caller stops the
// render with context.Canceled, which is not a server fault and has no reader for a 500.
func TestComponentAbandonedRequest(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/p/x", http.NoBody)
	w := httptest.NewRecorder()
	logs := &bytes.Buffer{}

	Component(w, r, slog.New(slog.NewTextHandler(logs, nil)), http.StatusOK,
		templ.ComponentFunc(func(ctx context.Context, _ io.Writer) error { return ctx.Err() }))

	if logs.Len() != 0 {
		t.Errorf("an abandoned request logged %q", logs.String())
	}
	if w.Flushed || w.Body.Len() != 0 || w.Code != http.StatusOK {
		t.Errorf("an abandoned request was answered: code %d body %q", w.Code, w.Body.String())
	}
}

// A component failure of a live request is still a 500: this keeps the guard to
// the departed caller: a render that fails for goen's own reason is an error and a 500.
func TestComponentLiveRequestError(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/p/x", http.NoBody)
	w := httptest.NewRecorder()
	logs := &bytes.Buffer{}

	Component(w, r, slog.New(slog.NewTextHandler(logs, nil)), http.StatusOK,
		templ.ComponentFunc(func(context.Context, io.Writer) error { return errors.New("boom") }))

	if w.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("code %d, log %q; want a 500 and an ERROR line", w.Code, logs.String())
	}
}

// A page reads the status Component answers with from its render context.
func TestComponentCarriesItsStatus(t *testing.T) {
	t.Parallel()

	var seen int
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", http.NoBody)
	Component(httptest.NewRecorder(), r, slog.New(slog.DiscardHandler), http.StatusUnprocessableEntity,
		templ.ComponentFunc(func(ctx context.Context, _ io.Writer) error {
			seen = pagestatus.From(ctx)
			return nil
		}))
	if seen != http.StatusUnprocessableEntity {
		t.Errorf("status the page saw = %d, want %d", seen, http.StatusUnprocessableEntity)
	}
}
