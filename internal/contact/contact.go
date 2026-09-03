// Package contact accepts and stores messages sent from the contact form.
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

// subject is one topic the form offers: the value STORED and the label read.
// Translating the value at write time would put the visitor's language in a row.
type subject struct {
	Value    string
	LabelKey i18n.Key
}

// subjects is the closed set of topics the form offers. The database repeats
// these stored values in contact_messages_subject_known, so a lower-level
// writer cannot widen the set.
var subjects = [...]subject{
	// i18n-exempt: the STORED value, per the note on subject.
	{Value: "訂單問題", LabelKey: i18n.KeySubjectOrder},
	// i18n-exempt: the STORED value, per the note on subject.
	{Value: "退換貨", LabelKey: i18n.KeySubjectReturns},
	// i18n-exempt: the STORED value, per the note on subject.
	{Value: "保固維修", LabelKey: i18n.KeySubjectWarranty},
	// i18n-exempt: the STORED value, per the note on subject.
	{Value: "商品諮詢", LabelKey: i18n.KeySubjectProduct},
	// i18n-exempt: the STORED value, per the note on subject.
	{Value: "合作提案", LabelKey: i18n.KeySubjectPartnership},
}

func offersSubject(v string) bool {
	for _, s := range subjects {
		if s.Value == v {
			return true
		}
	}
	return false
}

// Field length bounds, counted in RUNES.
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

// Validate reports every problem with m keyed by the form field name; an empty
// map means m may be stored. Callers pass the result of [Clean].
func Validate(ctx context.Context, m Message) map[string]string {
	errs := problems{}

	// Checked first: a newline in a name reaches the notification mail as a
	// forged second line, whatever the field's length.
	errs.set("name", singleLineProblem(ctx, m.Name))
	errs.set("email", singleLineProblem(ctx, m.Email))
	errs.set("subject", singleLineProblem(ctx, m.Subject))
	errs.set("order_ref", singleLineProblem(ctx, m.OrderRef))
	errs.set("message", multiLineProblem(ctx, m.Body))

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

// problems collects one message per field, first message wins.
type problems map[string]string

func (p problems) set(field, msg string) {
	if msg == "" {
		return
	}
	if _, taken := p[field]; !taken {
		p[field] = msg
	}
}

// singleLineProblem refuses every control character, the newline included:
// these fields reach records and log lines where a break forges a boundary.
func singleLineProblem(ctx context.Context, s string) string {
	if !strings.ContainsFunc(s, forbiddenControl) {
		return ""
	}
	return i18n.T(ctx, i18n.KeyNoNewlines)
}

// multiLineProblem applies to the message body, where line breaks are content.
func multiLineProblem(ctx context.Context, s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool {
		return forbiddenControl(r) && r != '\n' && r != '\r'
	}) {
		return ""
	}
	return i18n.T(ctx, i18n.KeyNoControlChars)
}

// forbiddenControl leaves the tab legal: it reads as whitespace, and the
// presence checks already treat a tab-only field as empty.
func forbiddenControl(r rune) bool { return unicode.IsControl(r) && r != '\t' }
