// Package backinstock is a shopper's request to be emailed when a sold-out
// variant is back in stock; it holds the request and the retention sweep of
// answered requests; it does not hold receiving stock (admin/stock), putting
// returned units back (admin/returns), or safety-stock warnings and days of
// cover (inventory).
//
// A request is an address and a variant. The notice itself is produced by the
// stock writer's transaction, which claims the waiting requests and queues one
// mail each; what is left here is asking, and deleting the row afterwards. The
// product page's form and the route it posts to are catalog's.
package backinstock

import "time"

// NotifiedRetain is how long a request stays after its notice was queued.
// Nothing reads the row again: the notice carries its own copy of the address in
// the outbox, which deletes it 30 days after delivery or creation
// (outbox.Retain). The request is kept no longer than that copy, so the address
// is not held in one table after the other has let it go. The privacy page
// states this number; a test binds the two.
const NotifiedRetain = 30 * 24 * time.Hour

// SweepInterval matches the other daily retention sweeps.
const SweepInterval = 24 * time.Hour

// SweepBatch bounds one delete; the first pass after a deploy may find a long backlog.
const SweepBatch = 500
