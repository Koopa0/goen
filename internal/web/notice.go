package web

import (
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

// NoticeEntry is a sentence a redirect can ask a page to show, with how the change it reports ended.
type NoticeEntry struct {
	Outcome components.Outcome
	Key     i18n.Key
}

func Done(k i18n.Key) NoticeEntry    { return NoticeEntry{Outcome: components.OutcomeDone, Key: k} }
func Refused(k i18n.Key) NoticeEntry { return NoticeEntry{Outcome: components.OutcomeRefused, Key: k} }
func Failed(k i18n.Key) NoticeEntry  { return NoticeEntry{Outcome: components.OutcomeFailed, Key: k} }

// Notice is what a redirect asks a page to show: the entry whose name is a
// query parameter set to 1. A redirect sets one; if several are set, which one
// shows is unspecified.
func Notice(r *http.Request, entries map[string]NoticeEntry) components.Result {
	q := r.URL.Query()
	for name, e := range entries {
		if q.Get(name) == "1" {
			return components.Result{Outcome: e.Outcome, Text: i18n.T(r.Context(), e.Key)}
		}
	}
	return components.Result{}
}
