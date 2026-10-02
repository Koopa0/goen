package admin

import "strconv"

type NewsletterView struct {
	Active       int64
	Unsubscribed int64
	Awaiting     int64
	Issues       []NewsletterIssue
	Draft        NewsletterDraft
	Errors       map[string]string
	Notice       string
}

type NewsletterDraft struct {
	Subject string
	Body    string
}

type NewsletterIssue struct {
	ID         string
	Subject    string
	Body       string
	Sent       bool
	SentAt     string
	Recipients int32
	SentBy     string
}

func (v NewsletterView) ActiveText() string { return strconv.FormatInt(v.Active, 10) }

func (v NewsletterView) UnsubscribedText() string {
	return strconv.FormatInt(v.Unsubscribed, 10)
}

func (v NewsletterView) AwaitingText() string { return strconv.FormatInt(v.Awaiting, 10) }

func (v NewsletterView) Empty() bool { return len(v.Issues) == 0 }

func (v NewsletterView) CanSend() bool { return v.Active > 0 }

func (v NewsletterView) Err(field string) string { return v.Errors[field] }

func (v NewsletterView) HasErr(field string) bool { return v.Errors[field] != "" }

func (i NewsletterIssue) RecipientsText() string {
	return strconv.FormatInt(int64(i.Recipients), 10)
}

func (i NewsletterIssue) SendAction() string { return "/admin/newsletter/" + i.ID + "/send" }

func (i NewsletterIssue) Preview() string {
	const limit = 80
	r := []rune(i.Body)
	if len(r) <= limit {
		return i.Body
	}
	return string(r[:limit]) + "…"
}
