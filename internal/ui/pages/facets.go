package pages

import (
	"crypto/sha256"
	"fmt"
	"strconv"
)

type FacetKind int

const (
	FacetBrand FacetKind = iota
	FacetVariantOption
)

type FacetGroup struct {
	Kind    FacetKind
	Name    string
	Label   string
	Options []FacetOption
}

func (g *FacetGroup) Param() string {
	if g.Kind == FacetVariantOption {
		return "opt"
	}
	return "brand"
}

func (g *FacetGroup) CountID(position int) string {
	if g.Kind == FacetBrand {
		return "brand-count-" + g.Options[position].Value
	}
	// Option names can contain spaces or punctuation; htmx needs CSS-safe IDs.
	sum := sha256.Sum256([]byte(g.Name))
	return fmt.Sprintf("option-count-%x-%s", sum[:8], strconv.Itoa(position))
}
