package formtext_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web/formtext"
)

// FieldMessages keeps the first message per field.
func TestFieldMessagesFirstPerField(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	got := formtext.FieldMessages(ctx, []formtext.FieldRefusal{
		{Field: "email", MessageKey: i18n.KeyCheckoutEmailRequired},
		{Field: "email", MessageKey: i18n.KeyCheckoutEmailMalformed},
		{Field: "confirm", MessageKey: i18n.KeyPasswordMismatch},
	})
	if len(got) != 2 {
		t.Fatalf("messages = %v, want one per field", got)
	}
	if want := i18n.T(ctx, i18n.KeyCheckoutEmailRequired); got["email"] != want {
		t.Errorf("email = %q, want the first refusal's %q", got["email"], want)
	}
	if want := i18n.T(ctx, i18n.KeyPasswordMismatch); got["confirm"] != want {
		t.Errorf("confirm = %q, want %q", got["confirm"], want)
	}
}

// FieldMessages is nil for no refusals.
func TestFieldMessagesNoRefusals(t *testing.T) {
	t.Parallel()

	if got := formtext.FieldMessages(t.Context(), nil); got != nil {
		t.Errorf("no refusals gave %v, want nil", got)
	}
	if got := formtext.FieldMessages(t.Context(), []formtext.FieldRefusal{}); got != nil {
		t.Errorf("empty refusals gave %v, want nil", got)
	}
}

// FieldMessages formats a message with its args.
func TestFieldMessagesFormatsArgs(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		got := formtext.FieldMessages(ctx, []formtext.FieldRefusal{
			{Field: "password", MessageKey: i18n.KeyPasswordTooShort, Args: []any{10}},
		})["password"]
		if !strings.Contains(got, "10") {
			t.Errorf("%s: the too-short message is %q and does not state 10", locale, got)
		}
		if strings.Contains(got, "%") {
			t.Errorf("%s: the too-short message is %q and still holds a verb", locale, got)
		}
	}
}

// FieldMessages leaves a message without args unformatted.
func TestFieldMessagesNoArgs(t *testing.T) {
	t.Parallel()

	keys := []i18n.Key{
		i18n.KeyCheckoutEmailRequired,
		i18n.KeyCheckoutEmailMalformed,
		i18n.KeyCheckoutEmailTooLong,
		i18n.KeyPasswordRequired,
		i18n.KeyPasswordTooLong,
		i18n.KeyPasswordMismatch,
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, key := range keys {
			got := formtext.FieldMessages(ctx, []formtext.FieldRefusal{{Field: "f", MessageKey: key}})["f"]
			if got != i18n.T(ctx, key) {
				t.Errorf("%s %s: rendered %q, want the catalogue text %q",
					locale, key, got, i18n.T(ctx, key))
			}
			for _, verb := range []string{"%!", "%d", "%s"} {
				if strings.Contains(got, verb) {
					t.Errorf("%s %s: renders %q, which holds %s", locale, key, got, verb)
				}
			}
		}
	}
}
