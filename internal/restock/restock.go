// Package restock deletes restock alerts once they have done their job. An
// alert is an address and a variant, kept to tell its owner when the variant is
// back; the notice itself is produced by the stock writer's transaction, so
// what is left here is the retention of the row afterwards.
package restock

import "time"

// NotifiedRetain is how long a restock alert stays after its notice was queued.
// Nothing reads the row again: the notice carries its own copy of the address in
// the outbox, which deletes it 30 days after delivery or creation
// (outbox.Retain). The alert is kept no longer than that copy, so the address is
// not held in one table after the other has let it go. The privacy page states
// this number; a test binds the two.
const NotifiedRetain = 30 * 24 * time.Hour

// SweepInterval matches the other daily retention sweeps.
const SweepInterval = 24 * time.Hour

// SweepBatch bounds one delete; the first pass after a deploy may find a long backlog.
const SweepBatch = 500
