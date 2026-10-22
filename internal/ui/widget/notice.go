package widget

import (
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
)

// NoticeEntry is a sentence a redirect can ask a page to show, with how the change it reports ended.
type NoticeEntry struct {
	Outcome Outcome
	Key     i18n.Key
}

func NoticeDone(k i18n.Key) NoticeEntry    { return NoticeEntry{Outcome: OutcomeDone, Key: k} }
func NoticeRefused(k i18n.Key) NoticeEntry { return NoticeEntry{Outcome: OutcomeRefused, Key: k} }
func NoticeFailed(k i18n.Key) NoticeEntry  { return NoticeEntry{Outcome: OutcomeFailed, Key: k} }

// NoticeFrom is what a redirect asks a page to show: the entry whose name is a
// query parameter set to 1. A redirect sets one; if several are set, which one
// shows is unspecified.
func NoticeFrom(r *http.Request, entries map[string]NoticeEntry) Result {
	q := r.URL.Query()
	for name, e := range entries {
		if q.Get(name) == "1" {
			return Result{Outcome: e.Outcome, Text: i18n.T(r.Context(), e.Key)}
		}
	}
	return Result{}
}
