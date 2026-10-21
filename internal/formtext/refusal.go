package formtext

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/i18n"
)

// FieldRefusal is a refusal of one form field, worded by its catalogue key.
type FieldRefusal struct {
	Field      string
	MessageKey i18n.Key
	// Args fill the message's verbs, for a message that states a limit.
	Args []any
}

// FieldMessages keeps the first message per field so a control shows one
// reason rather than a pile, formatting a message with its refusal's Args.
func FieldMessages(ctx context.Context, errs []FieldRefusal) map[string]string {
	if len(errs) == 0 {
		return nil
	}
	out := make(map[string]string, len(errs))
	for _, e := range errs {
		if _, seen := out[e.Field]; seen {
			continue
		}
		msg := i18n.T(ctx, e.MessageKey)
		// Only a message that states a limit carries a verb; formatting the
		// others appends %!(EXTRA …) beside the field.
		if len(e.Args) > 0 {
			msg = fmt.Sprintf(msg, e.Args...)
		}
		out[e.Field] = msg
	}
	return out
}
