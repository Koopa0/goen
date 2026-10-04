package products

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/media"
)

func TestEveryRedirectTheProductFormsMakeCarriesAMessage(t *testing.T) {
	t.Parallel()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package: %v", err)
	}
	// "?name=1", and a bare "name=1" returned by attachReason.
	param := regexp.MustCompile(`[?"]([a-z]+)=1`)
	sent := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(name) //nolint:gosec // G304: this package's own source files
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		for _, m := range param.FindAllStringSubmatch(string(src), -1) {
			sent[m[1]] = true
		}
	}
	// The upload refusals are written by media.UploadQuery, not as literals.
	for _, refusal := range []error{media.ErrTooLarge, media.ErrNotAnImage, media.ErrLosslessWebP, media.ErrBusy} {
		name, _, _ := strings.Cut(media.UploadQuery(refusal), "=")
		sent[name] = true
	}
	if len(sent) < 8 {
		t.Fatalf("only %d redirect parameters found; the parser stopped matching", len(sent))
	}
	for name := range sent {
		if _, ok := notices[name]; !ok {
			t.Errorf("?%s=1 carries no message: the page renders nothing after the button", name)
		}
	}
	for name := range notices {
		if !sent[name] {
			t.Errorf("notice %q names a parameter no redirect writes", name)
		}
	}
}
