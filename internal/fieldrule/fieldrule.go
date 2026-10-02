// Package fieldrule is the one statement of what a form field accepts, in the
// form a browser can check while the shopper fills the form in.
//
// The server stays authoritative: every rule here is a pattern the server's own
// validator also accepts, never a stricter one, and the external test holds
// each rule to the real validators on a table of good and bad inputs. The
// browser's half is assets/js/goen.js, which reads these attributes and shows
// the same message the 422 page would.
//
// A pattern is written for two engines at once: Go's RE2 (the test) and the
// browser's pattern attribute, which compiles with the v flag. So it uses no
// lookaround, spells whitespace out (JS \s and RE2 \s differ), and escapes
// every punctuation mark inside a class.
package fieldrule

import (
	"context"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
)

// Bounds the server enforces, repeated where the browser needs them.
const (
	// PhoneMaxRunes is the longest phone number, after trimming.
	PhoneMaxRunes = 30
	// EmailMaxBytes is email.Max, the SMTP forward-path limit.
	EmailMaxBytes = 254
)

const (
	// ws is what strings.TrimSpace removes. JS \s differs from it at both ends.
	ws = "[\t\n\v\f\r \u0085   -     　]*"
	// digit is a digit as web.FoldWidth leaves it: ASCII or full-width.
	digit = "[0-9０-９]"
	// punct is what a phone number may carry between its digits, in ASCII or
	// the full-width forms FoldWidth maps onto them (and the ideographic space).
	punct = "[\\-\\(\\)\\+ 　＋－（）]*"
	// The classes below name ASCII whitespace outright: JS \s also matches
	// U+00A0 and the rest, which RE2 does not, and net/mail accepts them in a
	// local part, so a \s here would make the browser stricter than the server.
	//
	// atext are the characters of an address's local part and of a domain label
	// that net/mail would take apart. It is deliberately lenient: it never
	// refuses what email.Valid accepts, and a few things it accepts email.Valid
	// refuses (a leading dot, two dots), which the server answers.
	atext = "[^\t\n\v\f\r @\"\\(\\),:;<>\\[\\]\\\\]"
	label = "[^\t\n\v\f\r @\"\\(\\),:;<>\\[\\]\\\\\\.]"
)

type Rule struct {
	// Name is the data-rule value, which the browser script uses as a hook.
	Name      string
	InputMode string
	// Pattern matches the whole raw value, whitespace around it included. It is
	// the pattern attribute and the Go test's regexp.
	Pattern string
	// MaxRunes bounds the trimmed value in runes, or is 0. A pattern cannot
	// count characters alongside the digits it counts, so the browser does.
	MaxRunes int
	// Check names an extra browser-side check a pattern cannot express, or "".
	Check   string
	Message i18n.Key
}

var (
	// Email accepts a bare address with a dotted domain. email.Valid is the
	// server's judgment; this is the part of it a pattern can state.
	Email = Rule{
		Name:      "email",
		InputMode: "email",
		Pattern:   ws + atext + "+@(?:" + label + "+(?:\\." + label + "+)+|\\[[^\t\n\v\f\r \\[\\]\\\\]*\\.[^\t\n\v\f\r \\[\\]\\\\]*\\])" + ws,
		Message:   i18n.KeyCheckoutEmailMalformed,
	}

	// Phone is eight to fifteen digits with the punctuation phone numbers carry.
	// It asks for no keyboard: type=tel already brings the phone pad, and some
	// numeric inputmodes leave out the + that +886 starts with.
	Phone = Rule{
		Name:     "phone",
		Pattern:  ws + punct + "(?:" + digit + punct + "){8,15}" + ws,
		MaxRunes: PhoneMaxRunes,
		Message:  i18n.KeyPhoneMalformed,
	}

	// PostalCode is three to six digits, with no guess at the city.
	PostalCode = Rule{
		Name:      "postal_code",
		InputMode: "numeric",
		Pattern:   ws + digit + "{3,6}" + ws,
		Message:   i18n.KeyPostalCodeMalformed,
	}

	// MobileBarcode is a 手機條碼: a slash and seven characters. The server folds
	// width and upper-cases before it looks, so lower case and full-width pass.
	// ı and ſ are there because strings.ToUpper turns them into I and S.
	MobileBarcode = Rule{
		Name:    "invoice_carrier",
		Pattern: ws + "[\\/／][0-9A-Za-zıſ０-９Ａ-Ｚａ-ｚ\\+＋\\-－\\.．]{7}" + ws,
		Message: i18n.KeyMobileBarcodeMalformed,
	}

	// DonationCode is three to seven ASCII digits. The server does not fold
	// width for it, so full-width digits are refused here too.
	DonationCode = Rule{
		Name:      "invoice_donation_code",
		InputMode: "numeric",
		Pattern:   ws + "[0-9]{3,7}" + ws,
		Message:   i18n.KeyDonationCodeMalformed,
	}

	// TaxID is eight digits whose weighted sum the 財政部 accepts. The shape is
	// the pattern; the sum is mirrored in the browser script as "taxid", because
	// no pattern can state it.
	TaxID = Rule{
		Name:      "invoice_tax_id",
		InputMode: "numeric",
		Pattern:   ws + digit + "{8}" + ws,
		Check:     "taxid",
		Message:   i18n.KeyTaxIDMalformed,
	}
)

var All = []Rule{Email, Phone, PostalCode, MobileBarcode, DonationCode, TaxID}

// Attrs are the attributes that make an input carry this rule: the pattern the
// browser enforces natively, the keyboard, and what the script needs to say the
// same thing the server would. message overrides the rule's default wording
// where a form's server says something else; errorID names the element that
// form's 422 page puts the message in, when it is not "<id>-error".
func (r Rule) Attrs(ctx context.Context, base templ.Attributes, o ...Option) templ.Attributes {
	opt := options{message: r.Message}
	for _, f := range o {
		f(&opt)
	}
	out := templ.Attributes{}
	for k, v := range base {
		out[k] = v
	}
	out["pattern"] = r.Pattern
	out["data-rule"] = r.Name
	out["data-rule-message"] = i18n.T(ctx, opt.message)
	if r.InputMode != "" {
		out["inputmode"] = r.InputMode
	}
	if r.MaxRunes > 0 {
		out["data-rule-max"] = r.MaxRunes
	}
	if r.Check != "" {
		out["data-rule-check"] = r.Check
	}
	if opt.errorID != "" {
		out["data-rule-error"] = opt.errorID
	}
	return out
}

type options struct {
	message i18n.Key
	errorID string
}

type Option func(*options)

// Message makes the form say key where its server does.
func Message(key i18n.Key) Option { return func(o *options) { o.message = key } }

func ErrorID(id string) Option { return func(o *options) { o.errorID = id } }
