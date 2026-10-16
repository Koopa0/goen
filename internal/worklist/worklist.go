// Package worklist names the work in the back office that waits for a person,
// as one desk reports it for another to list: the health desk judges what needs
// a person and the dashboard lists it. The desks reach one another only through
// interfaces the consumer declares, so the value those interfaces promise lives
// here, where no desk has to import another.
package worklist

import "github.com/koopa0/goen/internal/i18n"

// Task is one kind of work that waits for a person: what it is, how much of
// it, and where it is done.
type Task struct {
	Label i18n.Key
	Count int64
	Href  string
	// HasAge is false for work with no start time to measure from.
	HasAge     bool
	AgeSeconds int64
	// Alert marks work that is wrong rather than merely waiting.
	Alert bool
}
