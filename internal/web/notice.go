package web

import (
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

// Message is a sentence a redirect can ask a page to show, with how the change it reports ended.
type Message struct {
	Outcome components.Outcome
	Key     i18n.Key
}

func Done(k i18n.Key) Message    { return Message{Outcome: components.OutcomeDone, Key: k} }
func Refused(k i18n.Key) Message { return Message{Outcome: components.OutcomeRefused, Key: k} }
func Failed(k i18n.Key) Message  { return Message{Outcome: components.OutcomeFailed, Key: k} }

// Notice is what a redirect asks a page to show: the message for the first
// parameter of the request that is set to 1.
func Notice(r *http.Request, messages map[string]Message) components.Result {
	q := r.URL.Query()
	for name, m := range messages {
		if q.Get(name) == "1" {
			return components.Result{Outcome: m.Outcome, Text: i18n.T(r.Context(), m.Key)}
		}
	}
	return components.Result{}
}
