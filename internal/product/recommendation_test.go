package product

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/web"
)

func TestRecommendationTimeoutLeavesTheParentAndPageBudgetIntact(t *testing.T) {
	for _, parentBudget := range []time.Duration{0, 100 * time.Millisecond, time.Second} {
		t.Run(parentBudget.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent := t.Context()
				if parentBudget > 0 {
					var cancel context.CancelFunc
					parent, cancel = context.WithTimeout(parent, parentBudget)
					defer cancel()
				}
				start := time.Now()
				child, cancel := recommendationContext(parent)
				defer cancel()
				<-child.Done()
				if !errors.Is(child.Err(), context.DeadlineExceeded) || parent.Err() != nil {
					t.Fatalf("recommendationContext(parent with %s left): child err = %v, want DeadlineExceeded; parent err = %v, want nil", parentBudget, child.Err(), parent.Err())
				}
				if elapsed := time.Since(start); elapsed > 150*time.Millisecond || (parentBudget > 0 && elapsed >= parentBudget/2) {
					t.Errorf("optional read consumed %s of parent budget %s, want at most 150ms and less than half the parent budget", elapsed, parentBudget)
				}
			})
		})
	}
}

func TestRecommendationReadsKeepParentCancellation(t *testing.T) {
	parent, stop := context.WithCancel(t.Context())
	child, cancel := recommendationContext(parent)
	defer cancel()
	stop()
	if !errors.Is(child.Err(), context.Canceled) || !errors.Is(parent.Err(), context.Canceled) {
		t.Errorf("cancellation = child %v parent %v, want context.Canceled for both", child.Err(), parent.Err())
	}
}

func TestRecommendationDiagnosticsKeepCorrelationWithoutRawDatabaseErrors(t *testing.T) {
	var log bytes.Buffer
	s := NewStore(ctxErrDB{}, slog.New(slog.NewJSONHandler(&log, nil)))
	ctx := web.WithRequestID(t.Context(), "req-recommendations")
	id := uuid.New()
	if err := s.omitFailedRecommendation(ctx, readRelatedProducts, id, errors.New("SELECT customer_email: customer@example.com")); err != nil {
		t.Fatal(err)
	}
	line := log.String()
	for _, wanted := range []string{"related_products", "query_failed", id.String(), "req-recommendations"} {
		if !strings.Contains(line, wanted) {
			t.Errorf("diagnostic lacks %q: %s", wanted, line)
		}
	}
	if strings.Contains(line, "SELECT") || strings.Contains(line, "customer@example.com") {
		t.Errorf("diagnostic leaked the raw database error: %s", line)
	}
	log.Reset()
	parent, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.omitFailedRecommendation(parent, readBoughtTogether, id, context.Canceled); !errors.Is(err, context.Canceled) || log.Len() != 0 {
		t.Errorf("parent cancellation = %v, want context.Canceled; diagnostics %q, want empty", err, log.String())
	}
}

func TestFailedRecommendationNamesTheReadWhenItsRequestEnds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout bool
		want    error
	}{
		{name: "cancelled", want: context.Canceled},
		{name: "deadline", timeout: true, want: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			if tc.timeout {
				cancel()
				parent, cancel = context.WithTimeout(t.Context(), 0)
			}
			defer cancel()
			cancel()
			id := uuid.New()
			var diagnostic bytes.Buffer
			s := NewStore(ctxErrDB{}, slog.New(slog.NewJSONHandler(&diagnostic, nil)))
			err := s.omitFailedRecommendation(parent, readRelatedProducts, id, tc.want)
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), "read related_products of product "+id.String()) {
				t.Fatalf("ended request error = %v, want read related_products of product %s wrapping %v", err, id, tc.want)
			}
			if diagnostic.Len() != 0 {
				t.Fatalf("optional-failure diagnostic = %q, want empty for ended requests", diagnostic.String())
			}
		})
	}
}
