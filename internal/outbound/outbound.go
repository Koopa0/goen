// Package outbound bounds commerce provider calls: admission, deadlines,
// cancellation-aware HTTP and a shared outcome vocabulary for telemetry (#332).
package outbound

import "time"

// Dependency names a third-party commerce integration.
type Dependency string

const (
	Stripe Dependency = "stripe"
	ECPay  Dependency = "ecpay"
	Google Dependency = "google"
)

// Class is the operation shape that selects one budget and retry posture.
type Class uint8

const (
	// ForegroundLookup is a read or session resume on a request path.
	ForegroundLookup Class = 1
	// FinancialMutation may move money or open a payable session.
	FinancialMutation Class = 2
	// AsyncReconcile is background cleanup, listing or reconciliation.
	AsyncReconcile Class = 3
)

// Budget returns the wall-clock bound for one logical operation, including
// admission wait, every HTTP attempt and SDK backoff.
func Budget(class Class) time.Duration {
	switch class {
	case ForegroundLookup:
		return 10 * time.Second
	case FinancialMutation:
		return 20 * time.Second
	case AsyncReconcile:
		return 15 * time.Second
	default:
		return 10 * time.Second
	}
}

// maxActive returns how many in-flight calls one dependency may hold.
func maxActive(dep Dependency) int {
	switch dep {
	case Stripe:
		return 8
	case ECPay:
		return 4
	case Google:
		return 4
	default:
		return 4
	}
}
