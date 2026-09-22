package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Provider names a bounded outbound integration surface.
type Provider string

const (
	ProviderStripe Provider = "stripe"
	ProviderECPay  Provider = "ecpay"
	ProviderSMTP   Provider = "smtp"
)

// ProviderOutcome is a bounded result label for outbound calls.
type ProviderOutcome string

const (
	OutcomeSuccess   ProviderOutcome = "success"
	OutcomeError     ProviderOutcome = "error"
	OutcomeTimeout   ProviderOutcome = "timeout"
	OutcomeCancelled ProviderOutcome = "cancelled"
	OutcomeAmbiguous ProviderOutcome = "ambiguous"
)

var (
	providerDuration metric.Float64Histogram
	providerRequests metric.Int64Counter
)

func initProviderInstruments(m metric.Meter) error {
	var err error
	providerDuration, err = m.Float64Histogram(
		"goen.provider.duration",
		metric.WithDescription("Outbound provider call duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("provider duration histogram: %w", err)
	}
	providerRequests, err = m.Int64Counter(
		"goen.provider.requests",
		metric.WithDescription("Outbound provider calls by outcome"),
	)
	if err != nil {
		return fmt.Errorf("provider requests counter: %w", err)
	}
	return nil
}

// ProviderCall wraps one outbound provider operation with a span and metrics.
type ProviderCall struct {
	provider  Provider
	operation string
	start     time.Time
	span      trace.Span
}

// BeginProvider starts a provider span. Operation must be a bounded name such
// as checkout.create, not a provider object id.
func BeginProvider(ctx context.Context, provider Provider, operation string) (context.Context, *ProviderCall) {
	tracer := otel.Tracer("github.com/koopa0/goen/internal/telemetry")
	ctx, span := tracer.Start(ctx, string(provider)+"."+operation, //nolint:spancheck // ProviderCall.End closes the span
		trace.WithAttributes(
			attribute.String("provider.name", string(provider)),
			attribute.String("provider.operation", operation),
		),
	)
	// span ends in ProviderCall.End.
	return ctx, &ProviderCall{ //nolint:spancheck // End closes the span
		provider:  provider,
		operation: operation,
		start:     time.Now(),
		span:      span,
	}
}

// End records duration and outcome, then ends the span.
func (c *ProviderCall) End(ctx context.Context, err error) {
	if c == nil {
		return
	}
	outcome := classifyProviderOutcome(ctx, err)
	attrs := []attribute.KeyValue{
		attribute.String("provider.name", string(c.provider)),
		attribute.String("provider.operation", c.operation),
		attribute.String("provider.outcome", string(outcome)),
	}
	if providerDuration != nil {
		providerDuration.Record(ctx, time.Since(c.start).Seconds(), metric.WithAttributes(attrs...))
	}
	if providerRequests != nil {
		providerRequests.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
	if c.span != nil {
		if err != nil {
			// Provider errors can include request bodies, addresses and credentials.
			c.span.SetStatus(codes.Error, string(outcome))
		}
		c.span.SetAttributes(attribute.String("provider.outcome", string(outcome)))
		c.span.End()
	}
}

func classifyProviderOutcome(ctx context.Context, err error) ProviderOutcome {
	if err == nil {
		return OutcomeSuccess
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimeout
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return OutcomeCancelled
	}
	if errors.Is(err, ErrAmbiguousProvider) {
		return OutcomeAmbiguous
	}
	return OutcomeError
}

// ErrAmbiguousProvider marks a response the caller cannot classify cleanly.
var ErrAmbiguousProvider = errors.New("telemetry: ambiguous provider outcome")
