package site

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/ui/frame"
)

// ctxErrDB fails every statement with the caller's context error, as pgx does
// once the request's context is done.
type ctxErrDB struct{}

func (ctxErrDB) Exec(ctx context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, ctx.Err()
}

func (ctxErrDB) Query(ctx context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, ctx.Err()
}

func (ctxErrDB) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	return ctxErrRow{ctx.Err()}
}

type ctxErrRow struct{ err error }

func (r ctxErrRow) Scan(...any) error { return r.err }

// failingDeals answers DealsOnOffer with err, or with the context's error
// when err is nil.
type failingDeals struct{ err error }

func (c failingDeals) DealsOnOffer(ctx context.Context) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	return false, ctx.Err()
}

// TestHandler_WithBannerAbandoned: the banner and nav reads fail with
// context.Canceled when the visitor leaves; that is no server fault, and the
// page behind them has no reader.
func TestHandler_WithBannerAbandoned(t *testing.T) {
	t.Parallel()

	for name, wrap := range map[string]func(*Handler, http.Handler) http.Handler{
		"banner": (*Handler).WithBanner,
		"nav":    (*Handler).WithTopNav,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logs := &bytes.Buffer{}
			reached := false
			h := &Handler{
				log: slog.New(slog.NewTextHandler(logs, nil)), content: NewStore(ctxErrDB{}),
				deals: failingDeals{}, secure: true,
			}
			chrome := wrap(h, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			chrome.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody))

			if logs.Len() != 0 {
				t.Errorf("an abandoned request logged %q", logs.String())
			}
			if reached {
				t.Error("an abandoned request went on to render the page")
			}
		})
	}
}

// TestHandler_DealsOnOfferFailed: a failed read hides 優惠 and is logged: a link
// to a page with nothing on it is the worse mistake.
func TestHandler_DealsOnOfferFailed(t *testing.T) {
	t.Parallel()

	logs := &bytes.Buffer{}
	h := &Handler{log: slog.New(slog.NewTextHandler(logs, nil)), deals: failingDeals{err: errors.New("down")}}
	got := h.dealsOnOffer(t.Context())
	if got || frame.HasDeals(withNav(t.Context(), nil, got)) {
		t.Error("a failed deals read still offers the link")
	}
	if logs.Len() == 0 {
		t.Error("a failed deals read was not logged")
	}
}

// TestDismissDigest: the dismissal cookie carries a digest, not the banner's
// id.
func TestDismissDigest(t *testing.T) {
	t.Parallel()

	const id = "019fa72c-dd04-7383-acc5-4c32f889c790"
	digest := dismissDigest(id)

	if strings.Contains(digest, id) || digest == id {
		t.Errorf("the cookie value %q contains the banner id", digest)
	}
	if len(digest) > 32 {
		t.Errorf("the cookie value is %d characters; it travels on every request",
			len(digest))
	}
	if again := dismissDigest(id); again != digest {
		t.Error("the digest is not stable")
	}
	if other := dismissDigest("019fa72c-dd04-7383-acc5-4c32f889c791"); other == digest {
		t.Error("two banners share a digest")
	}
}
