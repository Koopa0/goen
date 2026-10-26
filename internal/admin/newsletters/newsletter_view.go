package newsletters

import (
	"strconv"

	"github.com/koopa0/goen/internal/ui/widget"
)

type View struct {
	Active        int64
	EnglishActive int64
	Unsubscribed  int64
	Awaiting      int64
	Issues        []Issue
	Draft         Draft
	Errors        map[string]string
	Notice        widget.Result
}

type Draft struct {
	Subject string
	Body    string
}

type Issue struct {
	ID         string
	Subject    string
	Body       string
	Sent       bool
	SentAt     string
	Recipients int32
	SentBy     string
}

func (v View) ActiveText() string { return strconv.FormatInt(v.Active, 10) }

func (v View) UnsubscribedText() string {
	return strconv.FormatInt(v.Unsubscribed, 10)
}

func (v View) AwaitingText() string { return strconv.FormatInt(v.Awaiting, 10) }

func (v View) Empty() bool { return len(v.Issues) == 0 }

func (v View) CanSend() bool { return v.Active > 0 }

func (v View) Err(field string) string { return v.Errors[field] }

func (v View) HasErr(field string) bool { return v.Errors[field] != "" }

func (i Issue) RecipientsText() string {
	return strconv.FormatInt(int64(i.Recipients), 10)
}

func (i Issue) SendAction() string { return "/admin/newsletters/" + i.ID + "/send" }

func (i Issue) Preview() string {
	const limit = 80
	r := []rune(i.Body)
	if len(r) <= limit {
		return i.Body
	}
	return string(r[:limit]) + "…"
}
