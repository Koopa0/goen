package outbound

import (
	"context"
	"errors"
	"fmt"
	"testing"

	stripe "github.com/stripe/stripe-go/v86"
)

func TestClassifyOutcomes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		mutate bool
		err    error
		want   Outcome
	}{
		{name: "success", want: OutcomeSucceeded},
		{name: "cancelled", err: context.Canceled, want: OutcomeCancelled},
		{name: "lookup timeout", err: context.DeadlineExceeded, want: OutcomeTransport},
		{name: "mutation timeout", mutate: true, err: context.DeadlineExceeded, want: OutcomeAmbiguous},
		{
			name: "stripe refusal",
			err:  &stripe.Error{HTTPStatusCode: 400},
			want: OutcomeRefused,
		},
		{
			name: "stripe server on lookup",
			err:  &stripe.Error{HTTPStatusCode: 500},
			want: OutcomeTransport,
		},
		{
			name:   "stripe server on mutation",
			mutate: true,
			err:    &stripe.Error{HTTPStatusCode: 500},
			want:   OutcomeAmbiguous,
		},
		{name: "admission", err: errAdmissionSaturated, want: OutcomeAdmissionRefused},
		{
			name:   "wrapped connection reset on mutation",
			mutate: true,
			err: fmt.Errorf("create refund: %w",
				errors.New("Post: read tcp 127.0.0.1:1->127.0.0.1:2: read: connection reset by peer")),
			want: OutcomeAmbiguous,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Classify(t.Context(), tt.mutate, tt.err); got != tt.want {
				t.Errorf("Classify() = %v, want %v", got, tt.want)
			}
		})
	}
}
