package i18n

import (
	"strings"
	"testing"
)

// TestMessagesProductVocabulary checks that product messages distinguish
// variants, options and specifications.
//
// A variant (款式) is one purchasable combination, an option (選項) is a
// choice that builds the combinations, and a specification (規格) is a table of
// facts about the product. #1837 separated the three because the Chinese copy
// had used them for one another. What this holds is the term each name-like
// message must use and the other two it must not, so rewording a sentence does
// not break it and a message that calls an option a specification does.
func TestMessagesProductVocabulary(t *testing.T) {
	t.Parallel()

	const variant, option, spec = "款式", "選項", "規格"
	for _, group := range []struct {
		term   string
		others []string
		keys   []Key
	}{
		{variant, []string{option, spec}, []Key{
			KeyAdminProdVariants, KeyAdminProdVariantAdd, KeyAuditVariantRetire,
			KeyAuditVariantCreate, KeyAuditEntityProductVariants,
		}},
		{option, []string{variant, spec}, []Key{
			KeyAdminProdOptions, KeyAdminProdOptionAdd, KeyAdminProdOptionName,
			KeyAdminProdOptionNameEn, KeyAdminProdNoValues, KeyAdminProdValueOption,
			KeyAdminProdValue, KeyAdminProdValueEn, KeyAdminProdValueAdd,
			KeyAuditOptionAdd, KeyAuditOptionValueAdd, KeyAuditFieldOptionValue,
		}},
		{spec, []string{variant, option}, []Key{
			KeySectionSpecs, KeyAdminProdSpecs, KeyAuditEntityProductSpecs,
		}},
	} {
		for _, k := range group.keys {
			zh := messages[k].ZhHant
			if !strings.Contains(zh, group.term) {
				t.Errorf("%s zh-Hant = %q, want it to say %s", k, zh, group.term)
			}
			for _, other := range group.others {
				if strings.Contains(zh, other) {
					t.Errorf("%s zh-Hant = %q, which names %s but must not say %s", k, zh, group.term, other)
				}
			}
		}
	}

	// The refusals that talk about one purchasable combination say variant in
	// both languages; the English once said item.
	for _, k := range []Key{KeyAddRefused, KeyRestockUnavailable, KeyAdminRetNoVariant} {
		m := messages[k]
		if !strings.Contains(m.ZhHant, variant) {
			t.Errorf("%s zh-Hant = %q, want it to say %s", k, m.ZhHant, variant)
		}
		if !strings.Contains(strings.ToLower(m.En), "variant") {
			t.Errorf("%s en = %q, want it to say variant", k, m.En)
		}
	}
	for _, k := range []Key{KeySectionSpecs, KeyAdminProdSpecs, KeyAuditEntityProductSpecs} {
		if en := messages[k].En; !strings.Contains(strings.ToLower(en), "specification") {
			t.Errorf("%s en = %q, want it to say specification", k, en)
		}
	}
}
