package pages

import "strconv"

// AdminNewsletterView is the mailing list and what has been sent to it.
//
// The list was write-only for as long as it existed: the footer collected
// addresses and the shop had no page that could see them, let alone write to
// them. This is the other half of the double opt-in work — consent came first
// because a send cannot be correct without it.
type AdminNewsletterView struct {
	Active       int64
	Unsubscribed int64
	Awaiting     int64
	Issues       []AdminNewsletterIssue
	// Draft carries a refused compose form's values back into it.
	Draft  AdminNewsletterDraft
	Errors map[string]string
	Notice string
}

// AdminNewsletterDraft is what the compose form holds.
type AdminNewsletterDraft struct {
	Subject string
	Body    string
}

// AdminNewsletterIssue is one issue as the back office sees it.
type AdminNewsletterIssue struct {
	ID         string
	Subject    string
	Body       string
	Sent       bool
	SentAt     string
	Recipients int32
	SentBy     string
}

// ActiveText and the two beside it are the three figures a person running a
// newsletter asks for, as text.
func (v AdminNewsletterView) ActiveText() string { return strconv.FormatInt(v.Active, 10) }

// UnsubscribedText is how many have left.
func (v AdminNewsletterView) UnsubscribedText() string {
	return strconv.FormatInt(v.Unsubscribed, 10)
}

// AwaitingText is how many have been asked and not answered.
func (v AdminNewsletterView) AwaitingText() string { return strconv.FormatInt(v.Awaiting, 10) }

// Empty reports whether nothing has been composed yet.
func (v AdminNewsletterView) Empty() bool { return len(v.Issues) == 0 }

// CanSend reports whether there is anybody to send to.
//
// A send to an empty list is not an error, but the button says so rather than
// letting somebody press it and read "0 recipients" afterwards.
func (v AdminNewsletterView) CanSend() bool { return v.Active > 0 }

// Err returns the message for a field, or "".
func (v AdminNewsletterView) Err(field string) string { return v.Errors[field] }

// HasErr reports whether a field was refused, for aria-invalid.
func (v AdminNewsletterView) HasErr(field string) bool { return v.Errors[field] != "" }

// Invalid is the aria-invalid value for a field.
func (v AdminNewsletterView) Invalid(field string) string {
	if v.HasErr(field) {
		return "true"
	}
	return "false"
}

// RecipientsText is how many copies an issue went to.
func (i AdminNewsletterIssue) RecipientsText() string {
	return strconv.FormatInt(int64(i.Recipients), 10)
}

// SendAction is where the send form posts. One issue, one URL: the id is in the
// path and nothing else about the request decides what goes out.
func (i AdminNewsletterIssue) SendAction() string { return "/admin/newsletter/" + i.ID + "/send" }

// Preview is the issue's opening, for a list that has to stay scannable.
//
// Bounded in RUNES rather than bytes, so a Chinese letter is cut where a reader
// would cut it and not a third of the way in.
func (i AdminNewsletterIssue) Preview() string {
	const limit = 80
	r := []rune(i.Body)
	if len(r) <= limit {
		return i.Body
	}
	return string(r[:limit]) + "…"
}
