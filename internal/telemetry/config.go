package telemetry

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	envEnabled   = "GOEN_OTEL_ENABLED"
	envEndpoint  = "GOEN_OTEL_EXPORTER_OTLP_ENDPOINT"
	envService   = "GOEN_OTEL_SERVICE_NAME"
	envDiag      = "GOEN_OTEL_DIAGNOSTICS"
	envShutdown  = "GOEN_OTEL_SHUTDOWN_TIMEOUT"
	envBatchSize = "GOEN_OTEL_EXPORT_BATCH_SIZE"
	envQueueSize = "GOEN_OTEL_EXPORT_QUEUE_SIZE"
)

// Config is opt-in telemetry wiring from the environment.
type Config struct {
	Enabled         bool
	Endpoint        string
	ServiceName     string
	Diagnostics     bool
	ShutdownTimeout time.Duration
	ExportBatchSize int
	ExportQueueSize int
}

// LoadConfig reads GOEN_OTEL_* variables. Telemetry stays off unless
// GOEN_OTEL_ENABLED=1; a missing exporter endpoint is not an error.
func LoadConfig() Config {
	cfg := Config{
		ServiceName:     envOr(envService, "goen"),
		ShutdownTimeout: 5 * time.Second,
		ExportBatchSize: 512,
		ExportQueueSize: 2048,
	}
	cfg.Enabled = os.Getenv(envEnabled) == "1"
	cfg.Endpoint = strings.TrimSpace(os.Getenv(envEndpoint))
	cfg.Diagnostics = os.Getenv(envDiag) == "1"
	if v := strings.TrimSpace(os.Getenv(envShutdown)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.ShutdownTimeout = d
		}
	}
	if v := strings.TrimSpace(os.Getenv(envBatchSize)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ExportBatchSize = n
		}
	}
	if v := strings.TrimSpace(os.Getenv(envQueueSize)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ExportQueueSize = n
		}
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
