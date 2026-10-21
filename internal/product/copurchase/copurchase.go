// Package copurchase is the worker that keeps the co-purchase projection
// (product_copurchases) current; it holds the call to refresh_copurchases()
// and its interval; it does not hold the "bought together" read or any other
// recommendation a shopper sees (catalog).
//
// Computed per request, "what did people who bought this also buy" costs time
// proportional to that product's order history, and an hour-old answer is the
// same answer.
package copurchase

import "time"

const Interval = 15 * time.Minute
