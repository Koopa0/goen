// Package telemetry exports opt-in OpenTelemetry for commerce latency and saturation.
package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

var (
	enabled            bool
	diagnosticsEnabled bool
)

// Runtime reports whether export providers are installed.
func Runtime() bool { return enabled }

// DiagnosticsEnabled reports whether staff-only pprof routes are mounted.
func DiagnosticsEnabled() bool { return diagnosticsEnabled }

// Setup installs global trace and metric providers when cfg.Enabled is true.
// Checkout and other request paths keep working when export is disabled or the
// collector is unreachable; shutdown is bounded by cfg.ShutdownTimeout.
func Setup(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	if !cfg.Enabled {
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{}, propagation.Baggage{},
		))
		if err := initAllInstruments(otel.Meter("github.com/koopa0/goen/internal/telemetry")); err != nil {
			return nil, err
		}
		diagnosticsEnabled = cfg.Diagnostics
		return func(context.Context) error { return nil }, nil
	}

	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.ServiceName),
	)

	traceExporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(cfg.Endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter,
			sdktrace.WithMaxExportBatchSize(cfg.ExportBatchSize),
			sdktrace.WithMaxQueueSize(cfg.ExportQueueSize),
		),
	)
	otel.SetTracerProvider(traceProvider)

	metricExporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(cfg.Endpoint),
		otlpmetrichttp.WithInsecure(),
	)
	if err != nil {
		if shutdownErr := traceProvider.Shutdown(ctx); shutdownErr != nil {
			return nil, errors.Join(err, shutdownErr)
		}
		return nil, err
	}
	metricReader := sdkmetric.NewPeriodicReader(metricExporter,
		sdkmetric.WithInterval(5*time.Second),
	)
	metricProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(metricReader),
	)
	otel.SetMeterProvider(metricProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	m := otel.Meter("github.com/koopa0/goen/internal/telemetry")
	if err := initAllInstruments(m); err != nil {
		var errs []error
		if shutdownErr := traceProvider.Shutdown(ctx); shutdownErr != nil {
			errs = append(errs, shutdownErr)
		}
		if shutdownErr := metricProvider.Shutdown(ctx); shutdownErr != nil {
			errs = append(errs, shutdownErr)
		}
		errs = append(errs, err)
		return nil, errors.Join(errs...)
	}

	enabled = true
	diagnosticsEnabled = cfg.Diagnostics
	shutdown := func(shutdownCtx context.Context) error {
		var errs []error
		if err := traceProvider.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, err)
		}
		if err := metricProvider.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	}
	return shutdown, nil
}
