package bearer_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/bearer"
)

// TestDigestGoldenVector pins the stored encoding: sessions, remembered
// browsers, carts, reset and verification links, order-access grants and
// newsletter confirmations all
// store digests made this way, so a change here orphans every one of them.
func TestDigestGoldenVector(t *testing.T) {
	t.Parallel()

	const want = "aaededf2b48f6bb3d45c19a441100d83a7168956e28cd01c2fc83f824e7e91d3"
	if got := hex.EncodeToString(bearer.Digest("goen-bearer-golden-vector")); got != want {
		t.Errorf("Digest(golden vector) = %s, want %s", got, want)
	}
}

func TestDigestIsNotTheToken(t *testing.T) {
	t.Parallel()

	tok := bearer.New()
	h := bearer.Digest(tok)
	if len(h) != 32 {
		t.Errorf("Digest length = %d, want 32", len(h))
	}
	if strings.Contains(string(h), tok) {
		t.Error("the digest contains the token; the database would hold live credentials")
	}
	// Captured first: comparing two calls in one expression is a tautology.
	first := bytes.Clone(h)
	if !bytes.Equal(bearer.Digest(tok), first) {
		t.Error("Digest is not deterministic; a returning visitor would lose their session")
	}
	if bytes.Equal(bearer.Digest(tok+"x"), first) {
		t.Error("two different tokens digest the same")
	}
}

func TestNewIsUnpredictable(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 64)
	for range 64 {
		tok := bearer.New()
		if len(tok) != 43 {
			t.Fatalf("token %q is %d characters, want 43", tok, len(tok))
		}
		raw, err := base64.RawURLEncoding.DecodeString(tok)
		if err != nil {
			t.Fatalf("token %q is not unpadded base64url: %v", tok, err)
		}
		if len(raw) != 32 {
			t.Fatalf("token decodes to %d bytes, want 32", len(raw))
		}
		if seen[tok] {
			t.Fatal("New repeated a token")
		}
		seen[tok] = true
	}
}
