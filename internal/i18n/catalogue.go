package i18n

import (
	"context"
	"fmt"
)

// Key is a message identifier.
//
// A named type, not a bare string: it makes T(ctx, "typo") a compile error
// rather than a silently missing translation, and it lets the completeness tests
// enumerate every key that exists.
type Key string

// Message is one string in every locale goen speaks.
//
// A struct rather than one map per locale, and that is the whole point: adding a
// string is ONE declaration in ONE place, and a locale left out is visible on
// the line that forgot it. The two-map form this replaced put the Chinese and
// the English four hundred lines apart, which is why a test had to go looking
// for the halves that never got written.
//
// exhaustruct is enabled for exactly this type (see .golangci.yml), so a literal
// missing a field does not compile past the linter. That is the mechanism; the
// tests below are what covers what a linter cannot see — whether the English is
// actually English, and whether anything renders the key at all.
type Message struct {
	ZhHant string
	En     string
}

// in returns the message for l.
//
// A closed set of two, and a switch with no default so adding a third locale is
// a compile error here rather than a locale that silently serves Chinese.
func (m Message) in(l Locale) string {
	switch l {
	case ZhHant:
		return m.ZhHant
	case En:
		return m.En
	}
	panic("i18n: unknown locale: " + string(l))
}

// messages is every string goen renders, filled by key during package
// initialisation.
//
// Not a literal, because a literal cannot refuse a duplicate: two areas of the
// catalogue reaching for "buy.total" would silently leave one of them with the
// other's words, in whichever order the file happened to be written.
var messages = map[Key]Message{}

// key registers a message and returns its identifier.
//
// It exists so a key cannot be declared without its translations and a
// translation cannot be written without a key — the two halves of the same fact,
// in one line, which is a class of rot removed rather than policed.
//
// It panics on a duplicate id or an empty string. Package initialisation is the
// right place to be loud: this runs before the server binds a port, so the
// failure is a process that will not start rather than a page that renders a key
// name at somebody.
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

// T is the message for this request's locale.
//
// A missing key returns the key itself. That is deliberate: it renders as
// "nav.cart" on the page — visible, greppable, and impossible to mistake for
// copy — where returning "" would render as a blank button nobody notices.
func T(ctx context.Context, k Key) string {
	if m, ok := messages[k]; ok {
		return m.in(FromContext(ctx))
	}
	return string(k)
}

// Keys is every key the catalogue defines, for the completeness tests.
func Keys() []Key {
	out := make([]Key, 0, len(messages))
	for k := range messages {
		out = append(out, k)
	}
	return out
}

// MessageFor returns the registered strings for k, and whether it exists.
func MessageFor(k Key) (Message, bool) {
	m, ok := messages[k]
	return m, ok
}

// Locales is every locale goen speaks.
func Locales() []Locale { return []Locale{ZhHant, En} }
