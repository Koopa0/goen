package catalog

import (
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
	outcome := string(action)
	if action == compareClear {
		selected = nil
	} else {
		loaded, err := h.store.Compare(r.Context(), selected)
		if err != nil {
			h.serverError(w, r)
			return
		}
		selected = loaded.Slugs()
		slug := r.PostFormValue("slug")
		switch action {
		case compareAdd:
			candidate, err := h.store.Compare(r.Context(), []string{slug})
			if err != nil {
				h.serverError(w, r)
				return
			}
			switch {
			case candidate.Empty():
				outcome = "unavailable"
			case slices.Contains(selected, slug):
			case len(selected) == comparison.Max:
				outcome = "full"
			default:
				selected = append(selected, slug)
			}
		case compareRemove:
			selected = slices.DeleteFunc(selected, func(s string) bool { return s == slug })
		case compareSave, compareClear:
		}
	}
	if outcome != "unavailable" && outcome != "full" {
		comparison.Write(w, selected, h.secureComparison)
	}
	next := comparisonReturn(r.PostFormValue("next"), outcome)
	//nolint:gosec // G710: comparisonReturn accepts only local product or comparison paths.
	http.Redirect(w, r, next, http.StatusSeeOther)
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
