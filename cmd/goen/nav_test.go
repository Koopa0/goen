package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type brokenDB struct{}

func (brokenDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("down")
}

func (brokenDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("down")
}

func (brokenDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return ctxErrRow{errors.New("down")}
}

// A failed read hides 優惠 and is logged: a link to a page with nothing on it is the worse mistake.
func TestAFailedDealsReadHidesTheLinkAndIsLogged(t *testing.T) {
	t.Parallel()

	logs := &bytes.Buffer{}
	got := dealsOnOffer(t.Context(), catalog.NewStore(brokenDB{}), slog.New(slog.NewTextHandler(logs, nil)))
	if got || layouts.HasDeals(withNav(t.Context(), nil, got)) {
		t.Error("a failed deals read still offers the link")
	}
	if logs.Len() == 0 {
		t.Error("a failed deals read was not logged")
	}
}
