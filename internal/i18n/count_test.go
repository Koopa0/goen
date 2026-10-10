package i18n

import (
	"regexp"
	"testing"
)

func TestCountReadsTheSingularOnlyForOne(t *testing.T) {
	cases := []struct {
		locale Locale
		key    Key
		n      int64
		shown  any
		want   string
	}{
		{En, "pdp.reviews.stars", 1, "1", "1 star"},
		{En, "pdp.reviews.stars", 2, "2", "2 stars"},
		{En, "pdp.reviews.stars", 0, "0", "0 stars"},
		{En, "pdp.reviews.count", 1, "1", "1 review"},
		{En, "campaign.products", 1, "1", "1 product"},
		{En, "campaign.products", 1000, "1,000", "1,000 products"},
		{En, "deals.count", 1, "1", "1 product reduced"},
		{ZhHant, "pdp.reviews.stars", 1, "1", "1 星"},
	}
	for _, c := range cases {
		if got := Count(WithLocale(t.Context(), c.locale), c.key, c.n, c.shown); got != c.want {
			t.Errorf("Count(%s, %s, %d) = %q, want %q", c.locale, c.key, c.n, got, c.want)
		}
	}
}

// aCountedNoun is a number placeholder followed by the plural noun it counts.
var aCountedNoun = regexp.MustCompile(`%[sd] (items|products|reviews|stars|days|hours|minutes|seconds|sub-categories|versions|postal codes|variants|points|accounts|months|colours|characters)\b`)

// pluralOnlyOnPurpose is every message that counts something yet keeps one
// English form, and why that is right.
var pluralOnlyOnPurpose = map[Key]string{
	"compare.full":                "the number is MaxCompare, which is five",
	"compare.overflow":            "the number is more than the cap, so at least six",
	"cart.reorder.partial":        "two counts decide the verb and the noun together; it needs its own wording",
	"admin.taxonomy.both":         "two counts in one sentence; it needs its own wording",
	"shipping.hold.body":          "a configured payment window in minutes, long enough that it is never 1",
	"checkout.submit.note":        "the fixed payment-start window is 29 minutes, never 1",
	"order.payment.checking":      "the page's fixed refresh interval and retry cap, never 1",
	"admin.message.days":          "the caller words one day and today separately, so it is never 1",
	"valid.contact.name.long":     "contact.maxName is fixed at 80 characters",
	"valid.contact.orderref":      "contact.maxOrderRef is fixed at 32 characters",
	"valid.contact.message.short": "contact.minMessage is fixed at 5 characters",
	"valid.contact.message.long":  "contact.maxMessage is fixed at 2000 characters",
	"field.email.toolong":         "email.Max is fixed at 254 characters",
	"valid.password.short":        "account.MinPasswordRunes is fixed at 10 characters",
	"pdp.reviews.bodyhint":        "product.MinReviewBodyRunes and MaxReviewBodyRunes are fixed at 5 and 2000 characters",
	"warranty.serial.toolong":     "warranty.MaxSerialRunes is fixed at 60 characters",
	"points.sub":                  "the number placeholder is a member multiplier modifying \"points rate\", not a count of points",
}

func TestACountedMessageHasASingular(t *testing.T) {
	counted := 0
	for k, m := range messages {
		if !aCountedNoun.MatchString(m.En) {
			continue
		}
		counted++
		_, isCount := singulars[k]
		if why, ok := pluralOnlyOnPurpose[k]; ok {
			if isCount {
				t.Errorf("%s is a countKey but is listed as plural-only (%s); delete the entry", k, why)
			}
			continue
		}
		if !isCount {
			t.Errorf("%s counts something in English (%q) but is not registered through countKey, so a count of one reads in the plural", k, m.En)
		}
	}
	if counted == 0 {
		t.Fatal("no message matched; this test would pass on nothing")
	}
}
