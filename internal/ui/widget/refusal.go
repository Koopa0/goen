package widget

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// RefusalField lists controls in their visual order, independently of map order.
type RefusalField struct{ Key, ID string }

// Refusal is what a refused form says at its head: which form, how many fields, and where the first is.
type Refusal struct {
	Form  i18n.Key
	Count int
	First string
}

// SummarizeRefusal counts the fields of a form that its errors name, and finds the first of them in visual order.
func SummarizeRefusal(form i18n.Key, errors map[string]string, fields ...RefusalField) Refusal {
	out := Refusal{Form: form}
	for _, field := range fields {
		if errors[field.Key] == "" {
			continue
		}
		out.Count++
		if out.First == "" {
			out.First = field.ID
		}
	}
	return out
}

func (v Refusal) Text(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyAdminRefusalSummary, int64(v.Count), v.Count, i18n.T(ctx, v.Form))
}
