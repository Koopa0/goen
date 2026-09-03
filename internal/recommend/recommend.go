// Package recommend keeps the co-purchase projection current. Computed per
// request, "what did people who bought this also buy" costs time proportional
// to that product's order history, and an hour-old answer is the same answer.
package recommend

import "time"

// Interval is how often the projection is rebuilt.
const Interval = 15 * time.Minute
