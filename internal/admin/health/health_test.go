package health

import (
	"math"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestWorkerAgeSecondsSaturateInsteadOfWrappingHealthy(t *testing.T) {
	t.Parallel()
	got := durationFromSeconds(math.MaxInt64)
	if got != time.Duration(math.MaxInt64) {
		t.Fatalf("durationFromSeconds(MaxInt64) = %v, want saturation at %v",
			got, time.Duration(math.MaxInt64))
	}
	view := admin.WorkerHealthView{
		OutboxPending:        1,
		OutboxOldest:         got,
		OutboxStaleAfter:     OutboxStaleAfter,
		CopurchaseEverBuilt:  true,
		CopurchaseAge:        got,
		CopurchaseStaleAfter: CopurchaseStaleAfter,
	}
	if view.OutboxHealthy() || view.RecommendHealthy() {
		t.Fatal("a timestamp beyond Go's duration range wrapped into a healthy worker")
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if strings.Contains(view.OutboxText(ctx), "-") || strings.Contains(view.RecommendText(ctx), "-") {
		t.Fatalf("saturated ages rendered negative: %q / %q",
			view.OutboxText(ctx), view.RecommendText(ctx))
	}
}

func TestEveryRedirectTheReconcileFormMakesCarriesAMessage(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`/admin/health\?([a-z]+)=1`).FindAllStringSubmatch(string(src), -1) {
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
