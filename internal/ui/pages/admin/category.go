package admin

type CategoryView struct {
	Slug   string
	Name   string
	Tone   string // the category's own; "" inherits its department's
	Image  Header
	Notice string
	Errors map[string]string
}

func (v CategoryView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v CategoryView) Err(f string) string { return v.Errors[f] }

func (v CategoryView) ImageAction() string { return "/admin/categories/" + v.Slug + "/image" }

func (v CategoryView) ImageRemoveAction() string {
	return "/admin/categories/" + v.Slug + "/image/remove"
}
