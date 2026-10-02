package admin

// CategoryView is one category's edit page: its header photograph.
type CategoryView struct {
	Slug   string
	Name   string
	Tone   string // the category's own; "" inherits its department's
	Image  Header
	Notice string
	// Errors names the fields a refused image form got wrong, by field name.
	Errors map[string]string
}

// HasErr reports whether a field of the image form was refused.
func (v CategoryView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v CategoryView) Err(f string) string { return v.Errors[f] }

// ImageAction is where the photograph upload form posts.
func (v CategoryView) ImageAction() string { return "/admin/categories/" + v.Slug + "/image" }

// ImageRemoveAction is where the remove form posts.
func (v CategoryView) ImageRemoveAction() string {
	return "/admin/categories/" + v.Slug + "/image/remove"
}
