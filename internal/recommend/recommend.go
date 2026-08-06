// Package recommend keeps the co-purchase projection current.
//
// # Why a projection at all
//
// "What did people who bought this also buy" computed per request costs 3 ms
// for a product nobody buys and 136 ms for the one everybody does, because the
// work is proportional to that product's order history — which only grows. The
// measurement that said the query was fine was taken against the cheap product;
// the one against the popular product said the opposite. Read from a
// projection, the same answer costs 0.04 ms.
//
// # Why staleness is acceptable HERE
//
// An hour-old answer to "what goes with this" is the same answer. An hour-old
// stock count is an oversell. That asymmetry is the whole argument: goen
// computes stock, price and availability live and projects only the things
// where being slightly behind changes nothing a customer can act on.
package recommend

import "time"

// Interval is how often the projection is rebuilt.
//
// Fifteen minutes. Short enough that a product launched this morning starts
// appearing in recommendations today, long enough that the rebuild — 584 ms at
// 15,000 orders — is a rounding error on the database's day.
const Interval = 15 * time.Minute
