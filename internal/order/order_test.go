package order

import "testing"

// TestFulfillmentStatus_NextKnown checks that Next stays inside the closed set
// of known statuses.
func TestFulfillmentStatus_NextKnown(t *testing.T) {
	t.Parallel()
	for _, s := range FulfillmentStatuses {
		for _, next := range s.Next() {
			if !next.Known() {
				t.Errorf("%q.Next() contains unknown state %q", s, next)
			}
		}
	}
}

func TestValidNumber(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"GO-260929-000001", true}, // the all-digit numbers of before keep matching
		{"GO-261014-ABCDEF", true},
		{"GO-261014-0123YZ", true},
		{"GO-261014-HJKMNP", true},
		{"GO-261014-QRSTVW", true},
		{"", false},
		{"GO-260929-00001", false},
		{"GO-260929-0000011", false},
		{"XX-260929-000001", false},
		{"GO_260929-000001", false},
		{"GO-26092a-000001", false}, // the day is digits
		{"GO-26I014-000001", false}, // and no look-alike is one
		{"GO-260929-00000/", false},
		{"GO-261014-abcdef", false}, // upper case only: NormalizeNumber upper-cases
		{"GO-261014-ABCDEI", false}, // the four letters Crockford leaves out
		{"GO-261014-ABCDEL", false},
		{"GO-261014-ABCDEO", false},
		{"GO-261014-ABCDEU", false},
		{"go-261014-ABCDEF", false},
		{"GO-261014-ABCDE\n", false},
		{"GO-261014-ABCDEé", false}, // two bytes, so also the wrong length
	} {
		if got := ValidNumber(tt.in); got != tt.want {
			t.Errorf("ValidNumber(%q) = %t, want %t", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeNumber(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		in   string
		want string // "" when refused
	}{
		{"typed in lower case", "go-261014-abcdef", "GO-261014-ABCDEF"},
		{"surrounded by space", " GO-261014-ABCDEF ", "GO-261014-ABCDEF"},
		{"surrounded by a tab and a newline", "\tGO-261014-ABCDEF\n", "GO-261014-ABCDEF"},
		{"already normal", "GO-261014-ABCDEF", "GO-261014-ABCDEF"},
		{"an all-digit number of before", "GO-260929-000001", "GO-260929-000001"},
		{"look-alikes after the prefix", "GO-26IO14-0OIL1K", "GO-261014-00111K"},
		{"look-alikes in lower case", "go-26io14-0oil1k", "GO-261014-00111K"},
		{"the prefix O is not a zero", "G0-261014-ABCDEF", ""},
		{"the prefix is not another letter pair", "GQ-261014-ABCDEF", ""},
		{"the prefix keeps its O when the rest maps", "GO-OO1014-OOOOOO", "GO-001014-000000"},
		{"a letter Crockford leaves out", "GO-261014-ABCDEU", ""},
		{"too short", "GO-261014-ABCDE", ""},
		{"too long", "GO-261014-ABCDEFG", ""},
		{"no dash", "GO-261014_ABCDEF", ""},
		{"empty", "", ""},
		{"only space", "   ", ""},
		{"a dotless i is not an I", "GO-261014-ABCDı", ""},
		// A Chinese keyboard left in full-width mode types the number itself.
		{"typed in full width", "ＧＯ－２６１０１４－ＡＢＣＤＥＦ", "GO-261014-ABCDEF"},
		{"typed in full width lower case", "ｇｏ－２６１０１４－ａｂｃｄｅｆ", "GO-261014-ABCDEF"},
		{"half and full width mixed", "ＧＯ-261014－ABCDＥＦ", "GO-261014-ABCDEF"},
		{"surrounded by ideographic space", "\u3000ＧＯ－２６１０１４－ＡＢＣＤＥＦ\u3000", "GO-261014-ABCDEF"},
		{"full-width look-alikes after the prefix", "ＧＯ－２６ＩＯ１４－０ＯＩＬ１Ｋ", "GO-261014-00111K"},
		{"a full-width zero is not the prefix O", "Ｇ０－２６１０１４－ＡＢＣＤＥＦ", ""},
		{"an ideographic space is not a dash", "ＧＯ－２６１０１４\u3000ＡＢＣＤＥＦ", ""},
		// The fold is not NFKC, and look-alikes of the number's characters stay refused.
		{"Cyrillic look-alikes", "GO-261014-АВСDEF", ""},
		{"a Unicode hyphen is not a dash", "GO\u2010261014-ABCDEF", ""},
		{"a Roman numeral is not an I", "GO-261014-ABCDEⅠ", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := NormalizeNumber(tt.in)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("NormalizeNumber(%q) = %q, %t; want %q, %t", tt.in, got, ok, tt.want, tt.want != "")
			}
			if ok && !ValidNumber(got) {
				t.Errorf("NormalizeNumber(%q) = %q, which ValidNumber refuses", tt.in, got)
			}
		})
	}
}

func TestPackingOverdue(t *testing.T) {
	t.Parallel()
	for days, want := range map[int]bool{0: false, 1: false, 2: false, 3: true, 10: true} {
		if got := PackingOverdue(days); got != want {
			t.Errorf("PackingOverdue(%d) = %t, want %t", days, got, want)
		}
	}
}
