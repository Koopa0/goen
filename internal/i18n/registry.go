package i18n

import (
	"context"
	"fmt"
)

// Key is a message identifier.
type Key string

// Message is one string in every locale goen speaks.
type Message struct {
	ZhHant string
	En     string
}

// in returns the message for l.
func (m Message) in(l Locale) string {
	switch l {
	case ZhHant:
		return m.ZhHant
	case En:
		return m.En
	}
	panic("i18n: unknown locale: " + string(l))
}

var messages = map[Key]Message{}

// key registers a message and returns its identifier.
func key(id string, m Message) Key {
	if id == "" {
		panic("i18n: a message needs an id")
	}
	if m.ZhHant == "" || m.En == "" {
		panic("i18n: " + id + " is missing a translation")
	}
	k := Key(id)
	if existing, taken := messages[k]; taken {
		panic(fmt.Sprintf("i18n: %s is declared twice: %q and %q", id, existing.ZhHant, m.ZhHant))
	}
	messages[k] = m
	return k
}

// T is the message for this request's locale; a missing key renders as itself.
func T(ctx context.Context, k Key) string {
	if m, ok := messages[k]; ok {
		return m.in(FromContext(ctx))
	}
	return string(k)
}

// Locales is every locale goen speaks.
func Locales() []Locale { return []Locale{ZhHant, En} }
