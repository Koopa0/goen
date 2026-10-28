package products

// Section is a part of the product editor whose form posts to the section's own
// fragment, "#sec-" and its name. A browser keeps that fragment through the 303 but never sends it to
// the server, so the redirect names the section in its query and the page puts
// the notice under that section's heading, where the reader has been taken.
type Section string

const (
	SectionDetails  Section = "details"
	SectionLabel    Section = "label"
	SectionInvoice  Section = "invoice"
	SectionOptions  Section = "options"
	SectionVariants Section = "variants"
	SectionSpecs    Section = "specs"
)

var sections = [...]Section{SectionDetails, SectionLabel, SectionInvoice, SectionOptions, SectionVariants, SectionSpecs}

// Query is the query a redirect appends to say which section the notice belongs to.
func (s Section) Query() string { return "&in=" + string(s) }

// sectionOf reads the in parameter of a request; a name that is not a section is
// none, and the notice stays at the head of the page.
func sectionOf(raw string) Section {
	for _, s := range sections {
		if string(s) == raw {
			return s
		}
	}
	return ""
}
