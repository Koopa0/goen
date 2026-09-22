package catalog

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/koopa0/goen/internal/comparison"
	"github.com/koopa0/goen/internal/web"
)

type comparisonAction string

const (
	compareAdd    comparisonAction = "added"
	compareRemove comparisonAction = "removed"
	compareClear  comparisonAction = "cleared"
	compareSave   comparisonAction = "saved"
)

func (h *Handler) AddComparison(w http.ResponseWriter, r *http.Request) {
	h.changeComparison(w, r, compareAdd)
}
func (h *Handler) RemoveComparison(w http.ResponseWriter, r *http.Request) {
	h.changeComparison(w, r, compareRemove)
}
func (h *Handler) ClearComparison(w http.ResponseWriter, r *http.Request) {
	h.changeComparison(w, r, compareClear)
}
func (h *Handler) SaveComparison(w http.ResponseWriter, r *http.Request) {
	h.changeComparison(w, r, compareSave)
}

func (h *Handler) changeComparison(w http.ResponseWriter, r *http.Request, action comparisonAction) {
	w.Header().Set("Cache-Control", "private, no-store")
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400", http.StatusBadRequest)
		return
	}
	selected := comparison.Read(r)
	if r.PostFormValue("snapshot") == "1" {
		selected, _ = comparison.Normalize(r.PostForm["p"])
	}
	selected, outcome, err := h.updatedComparison(r.Context(), selected, r.PostFormValue("slug"), action)
	if err != nil {
		h.serverError(w, r)
		return
	}
	if outcome != "unavailable" && outcome != "full" {
		comparison.Write(w, selected, h.secureComparison)
	}
	next := comparisonReturn(r.PostFormValue("next"), outcome)
	//nolint:gosec // G710: comparisonReturn accepts only local product or comparison paths.
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (h *Handler) updatedComparison(ctx context.Context, selected []string, slug string, action comparisonAction) ([]string, string, error) {
	if action == compareClear {
		return nil, string(action), nil
	}
	loaded, err := h.store.Compare(ctx, selected)
	if err != nil {
		return nil, "", err
	}
	selected = loaded.Slugs()
	switch action {
	case compareAdd:
		candidate, err := h.store.Compare(ctx, []string{slug})
		if err != nil {
			return nil, "", err
		}
		switch {
		case candidate.Empty():
			return selected, "unavailable", nil
		case slices.Contains(selected, slug):
		case len(selected) == comparison.Max:
			return selected, "full", nil
		default:
			selected = append(selected, slug)
		}
	case compareRemove:
		selected = slices.DeleteFunc(selected, func(s string) bool { return s == slug })
	case compareSave, compareClear:
	}
	return selected, string(action), nil
}

func comparisonReturn(raw, outcome string) string {
	path, ok := web.SitePath(raw)
	if !ok {
		path = "/compare"
	}
	target, err := url.Parse(path)
	if err != nil || (target.Path != "/compare" && !strings.HasPrefix(target.Path, "/p/")) {
		target = &url.URL{Path: "/compare"}
	}
	q := target.Query()
	q.Del("p")
	q.Set("compare", outcome)
	target.RawQuery = q.Encode()
	if strings.HasPrefix(target.Path, "/p/") {
		target.Fragment = "buybox"
	}
	return target.String()
}
