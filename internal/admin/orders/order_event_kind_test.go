package orders

import (
	"errors"
	"testing"

	"github.com/koopa0/goen/internal/ui/pages"
)

func TestEventKindForRefusesAStatusWithNoKind(t *testing.T) {
	kind, err := eventKindFor(pages.FulfillmentPending)
	if !errors.Is(err, ErrRefused) || kind != "" {
		t.Fatalf("pending gave (%q, %v), want an ErrRefused and no kind", kind, err)
	}
	if kind, err := eventKindFor(pages.FulfillmentPicking); err != nil || kind != "picking" {
		t.Fatalf("picking gave (%q, %v)", kind, err)
	}
}
