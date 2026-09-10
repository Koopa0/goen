package email_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/email"
)

func TestClean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "folds case", in: "Me@Example.COM", want: "me@example.com"},
		{name: "trims surrounding space", in: "  me@example.com \t", want: "me@example.com"},
		{name: "leaves a clean address alone", in: "me@example.com", want: "me@example.com"},
		{name: "empty stays empty", in: "   ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := email.Clean(tt.in); got != tt.want {
				t.Errorf("Clean(%q) = %q; want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "ordinary address", in: "me@example.com", want: true},
		{name: "subdomain and plus tag", in: "me+goen@mail.example.co.jp", want: true},
		{name: "empty", in: "", want: false},
		{name: "no at sign", in: "example.com", want: false},
		{name: "doubled at sign", in: "me@@example.com", want: false},
		{name: "no domain", in: "me@", want: false},
		{name: "display name is not a bare address", in: "王小明 <me@example.com>", want: false},
		{name: "angle brackets alone are not a bare address", in: "<me@example.com>", want: false},
		{name: "interior space", in: "me @example.com", want: false},
		{
			name: "at the length limit",
			in:   strings.Repeat("a", 254-len("@example.com")) + "@example.com",
			want: true,
		},
		{
			name: "one character past the limit",
			in:   strings.Repeat("a", 255-len("@example.com")) + "@example.com",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := email.Valid(tt.in); got != tt.want {
				t.Errorf("Valid(%q) = %v; want %v", tt.in, got, tt.want)
			}
		})
	}
}
