package taxonomy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

func shown(t *testing.T, target string) string {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	return widget.NoticeFrom(r, notices).Text
}

// TestAddedTargetSaysTheKind: a category added and a brand added each show
// their own sentence.
func TestAddedTargetSaysTheKind(t *testing.T) {
	t.Parallel()
	for kind, want := range map[string]i18n.Key{
		"categories": i18n.KeyAdminNoticeCategoryAdded,
		"brands":     i18n.KeyAdminNoticeBrandAdded,
	} {
		if got, want := shown(t, addedTarget(kind)), i18n.T(t.Context(), want); got != want {
			t.Errorf("addedTarget(%q) shows %q, want %q", kind, got, want)
		}
	}
}

// TestEditedTargetSaysTheKindAndTheAction: each of saving and deleting, for a
// brand and for a category, shows its own sentence.
func TestEditedTargetSaysTheKindAndTheAction(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		kind    string
		deleted bool
		want    i18n.Key
	}{
		{"brand", false, i18n.KeyAdminNoticeBrandSaved},
		{"brand", true, i18n.KeyAdminNoticeBrandDeleted},
		{"category", false, i18n.KeyAdminNoticeCategorySaved},
		{"category", true, i18n.KeyAdminNoticeCategoryDeleted},
	} {
		if got, want := shown(t, editedTarget(tt.kind, tt.deleted)), i18n.T(t.Context(), tt.want); got != want {
			t.Errorf("editedTarget(%q, %v) shows %q, want %q", tt.kind, tt.deleted, got, want)
		}
	}
}
