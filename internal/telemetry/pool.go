package telemetry

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// PoolRole is a bounded database pool label.
type PoolRole string

const (
	PoolStore       PoolRole = "store"
	PoolAdmin       PoolRole = "admin"
	PoolMaintenance PoolRole = "maintenance"
)

var (
	poolAcquired  metric.Int64ObservableGauge
	poolIdle      metric.Int64ObservableGauge
	poolTotal     metric.Int64ObservableGauge
	poolMax       metric.Int64ObservableGauge
	poolCanceled  metric.Int64ObservableGauge
	poolAcquireMs metric.Float64ObservableGauge
)

type poolObservation struct {
	role PoolRole
	pool *pgxpool.Pool
}

var observedPools []poolObservation

// RegisterPool exports pgxpool statistics for one role.
func RegisterPool(role PoolRole, pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	observedPools = append(observedPools, poolObservation{role: role, pool: pool})
}

func initPoolInstruments(m metric.Meter) error {
	var err error
	poolAcquired, err = m.Int64ObservableGauge(
		"goen.db.pool.acquired",
		metric.WithDescription("Connections currently checked out of the pool"),
	)
	if err != nil {
		return err
	}
	poolIdle, err = m.Int64ObservableGauge(
		"goen.db.pool.idle",
		metric.WithDescription("Idle connections in the pool"),
	)
	if err != nil {
		return err
	}
	poolTotal, err = m.Int64ObservableGauge(
		"goen.db.pool.total",
		metric.WithDescription("Total connections in the pool"),
	)
	if err != nil {
		return err
	}
	poolMax, err = m.Int64ObservableGauge(
		"goen.db.pool.max",
		metric.WithDescription("Configured maximum connections for the pool"),
	)
	if err != nil {
		return err
	}
	poolCanceled, err = m.Int64ObservableGauge(
		"goen.db.pool.acquire_canceled",
		metric.WithDescription("Pool acquisitions canceled while waiting"),
	)
	if err != nil {
		return err
	}
	poolAcquireMs, err = m.Float64ObservableGauge(
		"goen.db.pool.acquire_duration_seconds",
		metric.WithDescription("Cumulative time spent waiting to acquire a connection"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		for _, p := range observedPools {
			stat := p.pool.Stat()
			attrs := metric.WithAttributes(attribute.String("db.role", string(p.role)))
			o.ObserveInt64(poolAcquired, int64(stat.AcquiredConns()), attrs)
			o.ObserveInt64(poolIdle, int64(stat.IdleConns()), attrs)
			o.ObserveInt64(poolTotal, int64(stat.TotalConns()), attrs)
			o.ObserveInt64(poolMax, int64(stat.MaxConns()), attrs)
			o.ObserveInt64(poolCanceled, stat.CanceledAcquireCount(), attrs)
			o.ObserveFloat64(poolAcquireMs, stat.AcquireDuration().Seconds(), attrs)
		}
		return nil
	}, poolAcquired, poolIdle, poolTotal, poolMax, poolCanceled, poolAcquireMs)
	return err
}

// noopMeterProvider installs instruments without exporting when telemetry is off.
func initAllInstruments(m metric.Meter) error {
	if err := initQueryInstruments(m); err != nil {
		return err
	}
	if err := initPoolInstruments(m); err != nil {
		return err
	}
	if err := initProviderInstruments(m); err != nil {
		return err
	}
	if err := initHTTPInstruments(m); err != nil {
		return err
	}
	if err := initOutboxInstruments(m); err != nil {
		return err
	}
	return initCacheInstruments(m)
}
