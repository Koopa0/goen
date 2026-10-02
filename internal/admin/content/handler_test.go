package content

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/media"
)

func TestEveryRedirectTheContentFormsMakeCarriesAMessage(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`[?&"]([a-z]+)=1`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
	}
	// The upload refusals are written by media.UploadQuery, not as literals.
	for _, refusal := range []error{media.ErrTooLarge, media.ErrNotAnImage, media.ErrLosslessWebP, media.ErrBusy, errors.New("any other")} {
		name, _, _ := strings.Cut(media.UploadQuery(refusal), "=")
		sent[name] = true
	}
	if len(sent) == 0 {
		t.Fatal("no redirect parameter found; the parser stopped matching")
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
