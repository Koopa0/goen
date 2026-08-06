// Package contact accepts and stores messages sent from 聯絡我們.
package contact

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/email"

	"github.com/koopa0/goen/internal/i18n"
)

// Subject is one topic the form offers: the value STORED and the label read.
//
// The value stays Chinese and is the canonical code — it is what
// contact_messages holds and what /admin/messages reads, and the back office is
// Chinese by decision. Translating it at write time would put whichever language
// the visitor happened to have into a permanent row.
type Subject struct {
	Value    string
	LabelKey i18n.Key
}

// Subjects is the closed set of topics the form offers. This slice is the only
// definition of that set: the template renders it and [Validate] checks
// against it, so a topic cannot be offered without also being accepted.
var Subjects = []Subject{
	// i18n-exempt: the STORED value, per the note on Subject.
	{Value: "訂單問題", LabelKey: i18n.KeySubjectOrder},
	// i18n-exempt: the STORED value, per the note on Subject.
	{Value: "退換貨", LabelKey: i18n.KeySubjectReturns},
	// i18n-exempt: the STORED value, per the note on Subject.
	{Value: "保固維修", LabelKey: i18n.KeySubjectWarranty},
	// i18n-exempt: the STORED value, per the note on Subject.
	{Value: "商品諮詢", LabelKey: i18n.KeySubjectProduct},
	// i18n-exempt: the STORED value, per the note on Subject.
	{Value: "合作提案", LabelKey: i18n.KeySubjectPartnership},
}

// offersSubject reports whether v is one of the topics on the form.
func offersSubject(v string) bool {
	for _, s := range Subjects {
		if s.Value == v {
			return true
		}
	}
	return false
}

// Field length bounds, counted in runes so a Chinese message is measured the
// way the person writing it would count it.
const (
	maxName     = 80
	maxOrderRef = 32
	minMessage  = 5
	maxMessage  = 2000
)

// Message is one submission of the contact form.
type Message struct {
	Name     string
	Email    string
	Subject  string
	OrderRef string
	Body     string
}

// Clean trims surrounding whitespace from every field and folds the address to
// lower case, which is how it is stored and compared.
func Clean(m Message) Message {
	return Message{
		Name:     strings.TrimSpace(m.Name),
		Email:    email.Clean(m.Email),
		Subject:  strings.TrimSpace(m.Subject),
		OrderRef: strings.TrimSpace(m.OrderRef),
		Body:     strings.TrimSpace(m.Body),
	}
}

// Validate reports every problem with m keyed by the form field name, so the
// page can mark each control it rejected. An empty map means m may be stored.
// Callers pass the result of [Clean]; validating raw input would reject a
// value the trimmed form accepts.
// It takes a context because several of its messages carry a bound — "at most
// %d characters" — so they are sentences built from a message plus data rather
// than pure mappings, and the words have to be chosen where the reader is known.
func Validate(ctx context.Context, m Message) map[string]string {
	errs := problems{}

	// Control characters are checked first because they make a field unusable
	// whatever its length: a newline in a name reaches the support queue and
	// the notification mail as a forged second line. The body is exempt from
	// the newline rule, where line breaks are the point.
	errs.set("name", controlCharProblem(ctx, m.Name, false))
	errs.set("email", controlCharProblem(ctx, m.Email, false))
	errs.set("subject", controlCharProblem(ctx, m.Subject, false))
	errs.set("order_ref", controlCharProblem(ctx, m.OrderRef, false))
	errs.set("message", controlCharProblem(ctx, m.Body, true))

	switch n := utf8.RuneCountInString(m.Name); {
	case n == 0:
		errs.set("name", i18n.T(ctx, i18n.KeyNameRequired2))
	case n > maxName:
		errs.set("name", fmt.Sprintf(i18n.T(ctx, i18n.KeyNameTooLong2), maxName))
	}

	switch {
	case m.Email == "":
		errs.set("email", i18n.T(ctx, i18n.KeyEmailRequired))
	case len(m.Email) > email.Max:
		errs.set("email", fmt.Sprintf(i18n.T(ctx, i18n.KeyEmailTooLong), email.Max))
	case !email.Valid(m.Email):
		errs.set("email", i18n.T(ctx, i18n.KeyEmailMalformed))
	}

	if !offersSubject(m.Subject) {
		errs.set("subject", i18n.T(ctx, i18n.KeySubjectRequired))
	}

	if utf8.RuneCountInString(m.OrderRef) > maxOrderRef {
		errs.set("order_ref", fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderRefTooLong), maxOrderRef))
	}

	switch n := utf8.RuneCountInString(m.Body); {
	case n == 0:
		errs.set("message", i18n.T(ctx, i18n.KeyMessageRequired))
	case n < minMessage:
		errs.set("message", fmt.Sprintf(i18n.T(ctx, i18n.KeyMessageTooShort), minMessage))
	case n > maxMessage:
		errs.set("message", fmt.Sprintf(i18n.T(ctx, i18n.KeyMessageTooLong), maxMessage))
	}

	return errs
}

// problems collects one message per field. The first message wins, so a check
// that runs earlier because it matters more is not overwritten by a later one
// describing the same broken value in weaker terms.
type problems map[string]string

func (p problems) set(field, msg string) {
	if msg == "" {
		return
	}
	if _, taken := p[field]; !taken {
		p[field] = msg
	}
}

// controlCharProblem returns the message for a value carrying a character that
// has no business in typed text, or "" when it is clean.
func controlCharProblem(ctx context.Context, s string, allowNewlines bool) string {
	if !hasControlChars(s, allowNewlines) {
		return ""
	}
	if allowNewlines {
		return i18n.T(ctx, i18n.KeyNoControlChars)
	}
	return i18n.T(ctx, i18n.KeyNoNewlines)
}

// hasControlChars reports whether s carries a character that has no business
// in typed text. allowNewlines keeps \n and \r legal for a multi-line field.
func hasControlChars(s string, allowNewlines bool) bool {
	for _, r := range s {
		if allowNewlines && (r == '\n' || r == '\r') {
			continue
		}
		// Tab is a control character but reads as whitespace, and the presence
		// checks already treat a tab-only field as empty.
		if r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
