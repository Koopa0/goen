package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuditStorefrontRequestBudgetSetsDeadline(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	h := withStorefrontRequestBudget(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxCh <- r.Context()
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/c/audio", http.NoBody)
	h.ServeHTTP(httptest.NewRecorder(), req)

	observed := <-ctxCh
	deadline, ok := observed.Deadline()
	if !ok {
		t.Fatal("storefront request context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > storeRequestBudget {
		t.Fatalf("deadline had %v remaining, want a live budget no more than %v away",
			remaining, storeRequestBudget)
	}
}

func TestAuditStatelessRoutesSkipRequestBudget(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	h := withStorefrontRequestBudget(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxCh <- r.Context()
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", http.NoBody)
	h.ServeHTTP(httptest.NewRecorder(), req)

	observed := <-ctxCh
	if _, ok := observed.Deadline(); ok {
		t.Fatal("stateless request context has a deadline")
	}
}
