package i18n

import (
	"context"
	"fmt"
)

type Key string

type Message struct {
	ZhHant string
	En     string
}

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

// withoutEnglish holds the keys declared by zhOnly.
var withoutEnglish = map[Key]bool{}

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

// zhOnly is a word English has no use for, such as the counter 件 after a figure
// that its label already names; English reads it as empty.
func zhOnly(id, zhHant string) Key {
	if id == "" || zhHant == "" {
		panic("i18n: a zh-only message needs an id and a word")
	}
	k := Key(id)
	if existing, taken := messages[k]; taken {
		panic(fmt.Sprintf("i18n: %s is declared twice: %q and %q", id, existing.ZhHant, zhHant))
	}
	messages[k] = Message{ZhHant: zhHant}
	withoutEnglish[k] = true
	return k
}

// T is the message for this request's locale; a missing key renders as itself.
func T(ctx context.Context, k Key) string {
	if m, ok := messages[k]; ok {
		return m.in(FromContext(ctx))
	}
	return string(k)
}

func Locales() []Locale { return []Locale{ZhHant, En} }
