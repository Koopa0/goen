package invoice

import (
	"net/url"
	"testing"
)

func fuzzGateway(tb testing.TB) *Gateway {
	tb.Helper()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, "")
	if err != nil {
		tb.Fatalf("gateway: %v", err)
	}
	return g
}

// FuzzEnvelopeRoundTrip: what goen seals for ECPay, goen opens unchanged.
func FuzzEnvelopeRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"", "{}", `{"MerchantID":"2000132","RelateNumber":"A1"}`,
		"中文 品名 !()*~'", "a=b&c=d%20e+f", string([]byte{0xff, 0xfe, 0}),
		"0123456789abcdef", "0123456789abcde",
	} {
		f.Add(seed)
	}
	g := fuzzGateway(f)

	f.Fuzz(func(t *testing.T, plain string) {
		sealed, err := g.seal([]byte(plain))
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		opened, err := g.open(sealed)
		if err != nil {
			t.Fatalf("open(seal(%q)): %v", plain, err)
		}
		if string(opened) != plain {
			t.Fatalf("open(seal(%q)) = %q", plain, opened)
		}
	})
}

// FuzzOpenRefusesWithoutPanicking: the reply is a third party's bytes, so open
// answers an error for what it cannot read and never a slice bound the sender
// picked.
func FuzzOpenRefusesWithoutPanicking(f *testing.F) {
	g := fuzzGateway(f)
	valid, err := g.seal([]byte(`{"RtnCode":1}`))
	if err != nil {
		f.Fatalf("seal: %v", err)
	}
	for _, seed := range []string{
		"", "=", "!!!!", valid, valid[:len(valid)-4], valid + "AAAA",
		"AAAAAAAAAAAAAAAAAAAAAA==", "////////////////////////",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, sealed string) {
		opened, err := g.open(sealed)
		if err != nil && opened != nil {
			t.Fatalf("open returned %q beside the error %v", opened, err)
		}
	})
}

// FuzzPKCS7UnpadNeverExceedsItsInput: the last byte is the sender's, so the
// padding it names must fit inside the block before it is used as a length.
func FuzzPKCS7UnpadNeverExceedsItsInput(f *testing.F) {
	for _, seed := range [][]byte{
		nil, {0}, {1}, make([]byte, 16), append(make([]byte, 15), 16), append(make([]byte, 15), 255),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		out, err := pkcs7Unpad(b, 16)
		if err != nil {
			return
		}
		if len(out) >= len(b) || len(out) < len(b)-16 {
			t.Fatalf("unpadded %d bytes to %d", len(b), len(out))
		}
	})
}

// FuzzDotNetURLEncodeInverts: Go's own decoder reads back what the .NET-style
// encoder wrote, which is what the AES envelope depends on.
func FuzzDotNetURLEncodeInverts(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "a b", "!()*", "~", "%", "%zz", "中文", string([]byte{0xff}), "+&=",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		got, err := url.QueryUnescape(dotNetURLEncode(s))
		if err != nil || got != s {
			t.Fatalf("QueryUnescape(dotNetURLEncode(%q)) = %q, %v", s, got, err)
		}
	})
}
