package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	httpDuration metric.Float64Histogram
	httpRequests metric.Int64Counter
)

func initHTTPInstruments(m metric.Meter) error {
	var err error
	httpDuration, err = m.Float64Histogram(
		"goen.http.server.duration",
		metric.WithDescription("HTTP server request duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("http duration histogram: %w", err)
	}
	httpRequests, err = m.Int64Counter(
		"goen.http.server.requests",
		metric.WithDescription("HTTP server requests by route template and status class"),
	)
	if err != nil {
		return fmt.Errorf("http requests counter: %w", err)
	}
	return nil
}

type routeCapture struct {
	pattern string
}

type routeCaptureKey struct{}

func newRouteCaptureContext(ctx context.Context) (context.Context, *routeCapture) {
	capture := &routeCapture{}
	return context.WithValue(ctx, routeCaptureKey{}, capture), capture
}

func httpRoutePattern(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	capture, ok := r.Context().Value(routeCaptureKey{}).(*routeCapture)
	if !ok || capture.pattern == "" {
		return ""
	}
	return capture.pattern
}

// CaptureHTTPRoute publishes the mux pattern for outer telemetry spans.
// Visitor middleware replaces Request values with WithContext copies, so the
// matched template must be captured beside the mux and read through context.
func CaptureHTTPRoute(next http.Handler) http.Handler {
	if next == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Pattern == "" {
			return
		}
		capture, ok := r.Context().Value(routeCaptureKey{}).(*routeCapture)
		if ok {
			capture.pattern = r.Pattern
		}
	})
}

// HTTP wraps a handler with route-template spans and metrics.
func HTTP(next http.Handler) http.Handler {
	if next == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statelessTelemetryPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx, _ := newRouteCaptureContext(r.Context())
		route := RouteLabel(r.Method, httpRoutePattern(r))
		ctx, span := Tracer().Start(ctx, route,
			trace.WithAttributes(
				attribute.String("http.route", route),
				attribute.String("http.method", r.Method),
			),
		)
		r = r.WithContext(ctx)
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		pattern := httpRoutePattern(r)
		route = RouteLabel(r.Method, pattern)
		span.SetName(route)
		span.SetAttributes(attribute.String("http.route", route))
		status := rec.statusCode()
		class := statusClass(status)
		span.SetAttributes(
			attribute.Int("http.status_code", status),
			attribute.String("http.status_class", class),
		)
		if status >= 500 {
			span.SetStatus(codes.Error, class)
		}
		span.End()
		RecordHTTPRequest(ctx, r.Method, pattern, status, time.Since(start).Seconds())
	})
}

func statelessTelemetryPath(path string) bool {
	switch {
	case path == "/healthz", path == "/readyz":
		return true
	case len(path) >= len("/static/") && path[:len("/static/")] == "/static/":
		return true
	case len(path) >= len("/media/") && path[:len("/media/")] == "/media/":
		return true
	default:
		return false
	}
}

type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) statusCode() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return "other"
	}
}

// RecordHTTPRequest emits route-template metrics for handlers that bypass HTTP middleware.
func RecordHTTPRequest(ctx context.Context, method, pattern string, status int, dur float64) {
	if httpDuration == nil && httpRequests == nil {
		return
	}
	route := RouteLabel(method, pattern)
	attrs := []attribute.KeyValue{
		attribute.String("http.route", route),
		attribute.String("http.status_class", statusClass(status)),
	}
	if httpDuration != nil {
		httpDuration.Record(ctx, dur, metric.WithAttributes(attrs...))
	}
	if httpRequests != nil {
		httpRequests.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
}

// CorrelatedHandler adds trace and span identifiers to every slog record.
func CorrelatedHandler(next slog.Handler) slog.Handler {
	if next == nil {
		return nil
	}
	return &correlatedHandler{next: next}
}

type correlatedHandler struct {
	next slog.Handler
}

func (h *correlatedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *correlatedHandler) Handle(ctx context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler passes Record by value
	r = r.Clone()
	if span := trace.SpanFromContext(ctx); span != nil {
		sc := span.SpanContext()
		if sc.HasTraceID() {
			r.AddAttrs(slog.String("trace_id", sc.TraceID().String()))
		}
		if sc.HasSpanID() {
			r.AddAttrs(slog.String("span_id", sc.SpanID().String()))
		}
	}
	return h.next.Handle(ctx, r)
}

func (h *correlatedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &correlatedHandler{next: h.next.WithAttrs(attrs)}
}

func (h *correlatedHandler) WithGroup(name string) slog.Handler {
	return &correlatedHandler{next: h.next.WithGroup(name)}
}

// Tracer returns the application tracer.
func Tracer() trace.Tracer {
	return otel.Tracer("github.com/koopa0/goen/internal/telemetry")
}
