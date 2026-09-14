//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ratelimit"
)

// TestStorefrontPoolWaitRespectsRequestBudget exhausts the production storefront
// pool and proves a real catalog request finishes before the HTTP write
// deadline because pool acquisition shares the request budget.
func TestStorefrontPoolWaitRespectsRequestBudget(t *testing.T) {
	p, err := openPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	ap, err := openAdminPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer ap.Close()

	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	proxies, err := ratelimit.ParseProxies("")
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(
		&config{Addr: "127.0.0.1:0", SecureCookies: false},
		&RouterConfig{
			Pool: p, AdminPool: ap, Payments: gateway,
			Refunder: admin.NewRefunder(""), BaseURL: "http://127.0.0.1",
		},
		proxies, slog.New(slog.DiscardHandler),
	)

	held := make([]*pgxpool.Conn, 0, int(p.Config().MaxConns))
	for range int(p.Config().MaxConns) {
		c, acquireErr := p.Acquire(t.Context())
		if acquireErr != nil {
			t.Fatal(acquireErr)
		}
		held = append(held, c)
	}
	release := func() {
		for _, c := range held {
			c.Release()
		}
		held = nil
	}
	defer release()

	requestCtxCh := make(chan context.Context, 1)
	started := make(chan struct{})
	finished := make(chan struct{})
	actual := srv.Handler
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCtxCh <- r.Context()
		close(started)
		defer close(finished)
		actual.ServeHTTP(w, r)
	})

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve(ln)

	clientCtx, clientCancel := context.WithCancel(t.Context())
	defer clientCancel()

	req, err := http.NewRequestWithContext(
		clientCtx, http.MethodGet, "http://"+ln.Addr().String()+"/c/audio", http.NoBody,
	)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		resp, doErr := http.DefaultClient.Do(req)
		if doErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		result <- doErr
	}()

	<-started
	requestCtx := <-requestCtxCh
	_, outerHasDeadline := requestCtx.Deadline()
	t.Logf(
		"production WriteTimeout=%s, store request budget=%s, store statement timeout=%s, acquired=%d, outerRequestHasDeadline=%v",
		srv.WriteTimeout, storeRequestBudget, storeStatementTimeout,
		p.Stat().AcquiredConns(), outerHasDeadline,
	)

	deadline := storeRequestBudget + time.Second
	select {
	case <-finished:
		t.Log("request completed within the store request budget")
	case <-time.After(deadline):
		t.Fatalf(
			"real /c/audio handler still waits for pool after %s; context.Err=%v; acquired=%d",
			deadline, requestCtx.Err(), p.Stat().AcquiredConns(),
		)
	}

	clientCancel()
	release()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Error("handler did not drain after cancellation")
	}
	select {
	case err := <-result:
		t.Logf("client cleanup: %v", err)
	case <-time.After(3 * time.Second):
		t.Error("client did not drain")
	}
}
