package stock

import (
	"os"
	"regexp"
	"testing"
)

func TestEveryRedirectTheStockFormsMakeCarriesAMessage(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`[?&"]([a-z-]+)=1`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
	}
	// The stock list's writes name their notice through stockBack.
	for _, m := range regexp.MustCompile(`stockBack\(r, "([a-z-]+)"\)`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
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
