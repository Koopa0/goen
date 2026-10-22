package widget

// Photo has an empty URL when there is no photograph.
type Photo struct {
	URL    string
	Srcset string
	Alt    string
}

func (p Photo) Shown() bool { return p.URL != "" }
