// Package contact accepts and stores messages sent from 聯絡我們.
package contact

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/email"
)

// Subjects is the closed set of topics the form offers. This slice is the only
// definition of that set: the template renders it and [Validate] checks
// against it, so a topic cannot be offered without also being accepted.
var Subjects = []string{"訂單問題", "退換貨", "保固維修", "商品諮詢", "合作提案"}

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
func Validate(m Message) map[string]string {
	errs := problems{}

	// Control characters are checked first because they make a field unusable
	// whatever its length: a newline in a name reaches the support queue and
	// the notification mail as a forged second line. The body is exempt from
	// the newline rule, where line breaks are the point.
	errs.set("name", controlCharProblem(m.Name, false))
	errs.set("email", controlCharProblem(m.Email, false))
	errs.set("subject", controlCharProblem(m.Subject, false))
	errs.set("order_ref", controlCharProblem(m.OrderRef, false))
	errs.set("message", controlCharProblem(m.Body, true))

	switch n := utf8.RuneCountInString(m.Name); {
	case n == 0:
		errs.set("name", "請填寫姓名")
	case n > maxName:
		errs.set("name", fmt.Sprintf("姓名請控制在 %d 個字以內", maxName))
	}

	switch {
	case m.Email == "":
		errs.set("email", "請填寫 Email")
	case len(m.Email) > email.Max:
		errs.set("email", fmt.Sprintf("Email 請控制在 %d 個字元以內", email.Max))
	case !email.Valid(m.Email):
		errs.set("email", "Email 格式看起來不正確")
	}

	if !slices.Contains(Subjects, m.Subject) {
		errs.set("subject", "請選擇一個主題")
	}

	if utf8.RuneCountInString(m.OrderRef) > maxOrderRef {
		errs.set("order_ref", fmt.Sprintf("訂單編號請控制在 %d 個字元以內", maxOrderRef))
	}

	switch n := utf8.RuneCountInString(m.Body); {
	case n == 0:
		errs.set("message", "請填寫訊息內容")
	case n < minMessage:
		errs.set("message", fmt.Sprintf("訊息內容請至少 %d 個字,讓我們知道發生什麼事", minMessage))
	case n > maxMessage:
		errs.set("message", fmt.Sprintf("訊息內容請控制在 %d 個字以內", maxMessage))
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
func controlCharProblem(s string, allowNewlines bool) string {
	if !hasControlChars(s, allowNewlines) {
		return ""
	}
	if allowNewlines {
		return "不能包含控制字元"
	}
	return "不能包含換行或控制字元"
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
