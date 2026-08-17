// Package recommend keeps the co-purchase projection current.
//
// Computed per request, "what did people who bought this also buy" costs 136 ms
// for the product everybody buys, because the work is proportional to that
// product's order history. From the projection it is 0.04 ms, and an hour-old
// answer to that question is the same answer.
package recommend

import "time"

// Interval is how often the projection is rebuilt.
const Interval = 15 * time.Minute
