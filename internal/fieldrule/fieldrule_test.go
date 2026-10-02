package fieldrule_test

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/fieldrule"
	"github.com/koopa0/goen/internal/invoice"
)

// matches reports whether the browser would accept value for rule on its
// pattern and length. The Check, which only the browser can run, is not part of
// it; the tax ID checksum is tested against the server separately.
func matches(rule fieldrule.Rule, value string) bool {
	if !regexp.MustCompile("^(?:" + rule.Pattern + ")$").MatchString(value) {
		return false
	}
	return rule.MaxRunes == 0 || utf8.RuneCountInString(strings.TrimSpace(value)) <= rule.MaxRunes
}

// hasField reports whether a validator named field among its refusals.
func hasField(errs []account.FieldError, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}

// checkoutAddress is an address that is valid but for what a test sets.
func checkoutAddress() cart.Address {
	return cart.Address{
		To: cart.ToAddress, Email: "a@b.co", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
}

func accountAddress() account.Address {
	return account.Address{
		Name: "王小明", Phone: "0912345678", PostalCode: "110",
		City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
}

// server is what each rule's real validator says, through the normalisation the
// handler applies first.
var server = map[string]func(string) bool{
	"email": func(s string) bool { return email.Valid(strings.TrimSpace(s)) },
	"phone": func(s string) bool {
		a, b := checkoutAddress(), accountAddress()
		a.Phone, b.Phone = s, s
		a.Trim()
		b.Trim()
		// One contract, two forms: they must agree with each other as well.
		checkout := !hasField(a.Validate(), "phone")
		if saved := !hasField(b.Validate(), "phone"); saved != checkout {
			panic("checkout and the account address book disagree about phone " + s)
		}
		return checkout
	},
	"postal_code": func(s string) bool {
		a, b := checkoutAddress(), accountAddress()
		a.PostalCode, b.PostalCode = s, s
		a.Trim()
		b.Trim()
		checkout := !hasField(a.Validate(), "postal_code")
		if saved := !hasField(b.Validate(), "postal_code"); saved != checkout {
			panic("checkout and the account address book disagree about postal code " + s)
		}
		return checkout
	},
	"invoice_carrier": func(s string) bool {
		i := cart.Invoice{Type: invoice.PreferenceMobile, Carrier: s}
		return !hasField(i.Validate(), "invoice_carrier")
	},
	"invoice_donation_code": func(s string) bool {
		i := cart.Invoice{Type: invoice.PreferenceDonate, DonationCode: s}
		return !hasField(i.Validate(), "invoice_donation_code")
	},
	"invoice_tax_id": func(s string) bool {
		i := cart.Invoice{Type: invoice.PreferenceCompany, CompanyName: "公司", TaxID: s}
		return !hasField(i.Validate(), "invoice_tax_id")
	},
}

// cases are inputs per rule. reject marks the ones the browser must refuse
// outright; every other input is held only to "never stricter than the server".
type input struct {
	in     string
	reject bool
}

func ok(in ...string) []input {
	out := make([]input, len(in))
	for i, s := range in {
		out[i] = input{in: s}
	}
	return out
}

func bad(in ...string) []input {
	out := make([]input, len(in))
	for i, s := range in {
		out[i] = input{in: s, reject: true}
	}
	return out
}

func cat(parts ...[]input) []input {
	var out []input
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var tables = map[string][]input{
	"email": cat(
		ok("a@b.co", "user.name+tag@example.com", " a@b.co ", "a@b.co\n", "用戶@例え.jp", "a@b_c.co",
			"ａ@b.co", "a\u00a0b@c.co", "a@b\u00a0c.co", "a@[1.2.3.4]", "a(b)@c.co", "a@b..co", "a.@b.co", ".a@b.co", "\"a b\"@c.co", "a@b.c"),
		bad("a@b", "a@b.", "a@.co", "@b.co", "a@@b.co", "a b@c.co", "a@b c.co", "Name <a@b.co>",
			"a@b.co,c@d.co", "a@b.co;", "user@localhost", "plain", "", "   "),
	),
	"phone": cat(
		ok("0912345678", "(02) 2345-6789", "+886 912 345 678", "０９１２３４５６７８", "09１２345678",
			"０９１２－３４５－６７８", " 0912345678 ", "12345678", "123456789012345",
			strings.Repeat("1", 15)+strings.Repeat("-", 15), "＋８８６　９１２　３４５　６７８"),
		bad("1234567", "1234567890123456", "0912abc678", "0912.345.678", "0912\t345678", "", "   ",
			strings.Repeat("1", 15)+strings.Repeat("-", 16)),
	),
	"postal_code": cat(
		ok("110", "10617", "100000", "１１０", "１１０ ", " 110", "110 ", "11　0"),
		bad("11", "1100000", "11a", "", "abc", "11 0"),
	),
	"invoice_carrier": cat(
		ok("/ABC1234", "/abc1234", "/ＡＢＣ１２３４", "／ABC1234", "/AB+-.12", "/ıSC1234", " /ABC1234 "),
		bad("/ABC123", "/ABC12345", "ABC1234", "/ABC 234", "", "/ABC123!"),
	),
	"invoice_donation_code": cat(
		ok("123", "1234567", "012", " 123 "),
		bad("12", "12345678", "abc", "12 3", "", "０１２３"),
	),
	"invoice_tax_id": cat(
		// Shape the browser accepts whether or not the checksum holds; the
		// checksum is the browser's mirrored check, listed in goen.js.
		ok("04595257", "04595252", "00490978", "10000270", "０４５９５２５７", " 04595257 ", "04595250", "00000000"),
		bad("0459525", "045952570", "0459525a", "", "0459 5257"),
	),
}

// TestNoRuleIsStricterThanItsValidator holds every client rule to the server's
// own validator: whatever the server accepts, the browser must accept, and the
// obviously malformed must be refused by both.
func TestNoRuleIsStricterThanItsValidator(t *testing.T) {
	t.Parallel()
	for _, rule := range fieldrule.All {
		valid, ok := server[rule.Name]
		rows, has := tables[rule.Name]
		if !ok || !has {
			t.Errorf("rule %s has no server validator or no table", rule.Name)
			continue
		}
		for _, row := range rows {
			serverOK, clientOK := valid(row.in), matches(rule, row.in)
			if serverOK && !clientOK {
				t.Errorf("%s: the server accepts %q and the browser would refuse it", rule.Name, row.in)
			}
			if row.reject && (serverOK || clientOK) {
				t.Errorf("%s: %q is malformed but the server accepts = %t, the browser accepts = %t",
					rule.Name, row.in, serverOK, clientOK)
			}
		}
	}
}

// Every value the table says the server accepts is also the shape a rule with
// no extra Check agrees on exactly: a rule that needs no checksum is as strict
// as the server on the table, so a loosened pattern shows up here.
func TestRulesWithoutAChecksumAgreeWithTheServerOnTheTable(t *testing.T) {
	t.Parallel()
	for _, rule := range fieldrule.All {
		if rule.Check != "" || rule.Name == "email" {
			continue
		}
		for _, row := range tables[rule.Name] {
			if serverOK, clientOK := server[rule.Name](row.in), matches(rule, row.in); serverOK != clientOK {
				t.Errorf("%s: %q server accepts = %t, browser accepts = %t", rule.Name, row.in, serverOK, clientOK)
			}
		}
	}
}

// The tax ID checksum is mirrored in assets/js/goen.js. These are the vectors
// the browser is checked against by hand; here they pin the server's verdict so
// the two cannot drift unnoticed.
func TestTaxIDVectorsTheBrowserMirrors(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]bool{
		"04595252": true, "04595257": true, "04595263": true, "04595268": true,
		"00490978": true, "00530573": true, "10000270": true, // seventh digit 7
		"04595250": false, "04595251": false, "04595253": false, "00000000": false,
	} {
		if got := invoice.ValidTaxID(id); got != want {
			t.Errorf("ValidTaxID(%s) = %t, want %t: update assets/js/goen.js's taxid check", id, got, want)
		}
	}
}

// classes returns the contents of each character class in a pattern.
func classes(pattern string) []string {
	var out []string
	rs := []rune(pattern)
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\\':
			i++
		case '[':
			start := i + 1
			for i++; i < len(rs) && rs[i] != ']'; i++ {
				if rs[i] == '\\' {
					i++
				}
			}
			if i < len(rs) {
				out = append(out, string(rs[start:i]))
			}
		}
	}
	return out
}

// TestPatternsAreWrittenForTheBrowsersVFlag holds each pattern to what the v flag
// reads inside a character class. A pattern the browser cannot compile is
// ignored silently, so the field it guards would never be refused. Go's regexp
// accepts all of these, which is why only a check on the source can catch them.
func TestPatternsAreWrittenForTheBrowsersVFlag(t *testing.T) {
	t.Parallel()
	doubled := []string{"&&", "!!", "##", "$$", "%%", "**", "++", ",,", "..", "::", ";;", "<<", "==", ">>", "??", "@@", "^^", "``", "~~"}
	for _, rule := range fieldrule.All {
		for _, class := range classes(rule.Pattern) {
			rs := []rune(strings.TrimPrefix(class, "^"))
			for i := 0; i < len(rs); i++ {
				switch rs[i] {
				case '\\':
					i++
				case '(', ')', '[', ']', '{', '}', '/', '|':
					t.Errorf("%s: %q is unescaped inside the class [%s]; the v flag refuses it", rule.Name, rs[i], class)
				case '-':
					if i == 0 || i == len(rs)-1 {
						t.Errorf("%s: a bare hyphen at the end of the class [%s]; the v flag needs it escaped", rule.Name, class)
					}
				}
			}
			for _, d := range doubled {
				if strings.Contains(class, d) {
					t.Errorf("%s: %q inside the class [%s] is a reserved double punctuator under the v flag", rule.Name, d, class)
				}
			}
		}
	}
}
