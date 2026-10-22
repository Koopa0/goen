package email_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/email"
)

// TestMask checks that Mask does not reveal the local part of an address or its
// length.
func TestMask(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ address, want string }{
		{address: "  ADA@EXAMPLE.COM ", want: "a***@example.com"},
		{address: "a@example.com", want: "a***@example.com"},
		{address: "a.very.long.mailbox@example.com", want: "a***@example.com"},
		{address: "+private@example.com", want: "***@example.com"},
		{address: `"private mailbox"@example.com`, want: ""},
		{address: "not-an-address", want: ""},
		{address: "Ada <ada@example.com>", want: ""},
		{address: "a@example.com\r\nBcc: hidden@example.com", want: ""},
		{address: strings.Repeat("a", email.MaxBytes) + "@example.com", want: ""},
	} {
		t.Run(tt.address, func(t *testing.T) {
			t.Parallel()
			if got := email.Mask(tt.address); got != tt.want {
				t.Errorf("Mask(%q) = %q, want %q", tt.address, got, tt.want)
			}
		})
	}
}

// TestReadMasked checks that the confirmation query accepts only a masked
// address.
func TestReadMasked(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ value, want string }{
		{value: "a***@example.com", want: "a***@example.com"},
		{value: "***@example.com", want: "***@example.com"},
		{value: "  A***@EXAMPLE.COM  ", want: "a***@example.com"},
		{value: "ada@example.com"},
		{value: "ab***@example.com"},
		{value: "a****@example.com"},
		{value: "Ada <a***@example.com>"},
		{value: "a***@localhost"},
		{value: "a***@example.com\x00"},
		{value: "a***@example.com\r\nBcc: x@example.com"},
		{value: `<script>***@example.com`},
		{value: strings.Repeat("a", email.MaxBytes+4)},
	} {
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()
			if got := email.ReadMasked(tt.value); got != tt.want {
				t.Errorf("ReadMasked(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func FuzzConfirmationDisplay(f *testing.F) {
	for _, seed := range []string{"ada@example.com", "a***@example.com", "", "a***@localhost", "a@example.com\x00"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, address string) {
		masked := email.Mask(address)
		if masked != "" && email.ReadMasked(masked) != masked {
			t.Errorf("masked address %q did not survive its confirmation redirect", masked)
		}
		if got := email.ReadMasked(address); got != "" && len(got) > email.MaxBytes+3 {
			t.Errorf("display address length = %d, want bounded", len(got))
		}
	})
}
