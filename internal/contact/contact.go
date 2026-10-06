// Package contact accepts and stores messages sent from the contact form.
package contact

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/contactsubject"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
)

const (
	maxName     = 80
	maxOrderRef = 32
	minMessage  = 5
	maxMessage  = 2000
)

type Message struct {
	Name     string
	Email    string
	Subject  contactsubject.Subject
	OrderRef string
	Body     string
}

func Clean(m Message) Message {
	return Message{
		Name:     strings.TrimSpace(m.Name),
		Email:    email.Clean(m.Email),
		Subject:  contactsubject.Subject(strings.TrimSpace(string(m.Subject))),
		OrderRef: strings.TrimSpace(m.OrderRef),
		Body:     strings.TrimSpace(m.Body),
	}
}

func Validate(ctx context.Context, m Message) map[string]string {
	errs := problems{}

	// Checked first: a newline in a name reaches the notification mail as a
	// forged second line, whatever the field's length.
	errs.set("name", singleLineProblem(ctx, m.Name))
	errs.set("email", singleLineProblem(ctx, m.Email))
	errs.set("subject", singleLineProblem(ctx, string(m.Subject)))
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

	if !m.Subject.Known() {
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

type problems map[string]string

func (p problems) set(field, msg string) {
	if msg == "" {
		return
	}
	if _, taken := p[field]; !taken {
		p[field] = msg
	}
}

// singleLineProblem refuses every control character, newline included: these
// fields reach records and log lines where a break forges a boundary.
func singleLineProblem(ctx context.Context, s string) string {
	if !strings.ContainsFunc(s, forbiddenControl) {
		return ""
	}
	return i18n.T(ctx, i18n.KeyNoNewlines)
}

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
