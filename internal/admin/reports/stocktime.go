package reports

import "time"

// movement is one change to a variant's stock, as the ledger records it.
type movement struct {
	at    time.Time
	delta int32
}

// timeInStock is how much of [from, to) a variant had stock above its safety
// level, that is, something a sale may take. Its stock at from is the stock
// now less every movement since; moves are in time order and all at or after
// from.
func timeInStock(stock, safety int32, from, to time.Time, moves []movement) time.Duration {
	level := int64(stock)
	for _, m := range moves {
		level -= int64(m.delta)
	}
	var total time.Duration
	since := from
	for _, m := range moves {
		if level > int64(safety) {
			total += m.at.Sub(since)
		}
		level += int64(m.delta)
		since = m.at
	}
	if level > int64(safety) {
		total += to.Sub(since)
	}
	return total
}
