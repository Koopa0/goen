package repocheck_test

import (
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// TestTemplateTakes runs the view-model walk over fixtures: what a template takes
// is a view model, and nothing else in the package is. The real guard
// (TestEveryViewModelFieldIsAssigned) cannot show the second half, since every
// exported struct in today's view packages is one a template reaches.
func TestTemplateTakes(t *testing.T) {
	t.Parallel()

	const (
		feature = goenModule + "/internal/feature"
		shared  = goenModule + "/internal/shared"
		domain  = goenModule + "/internal/domain"
		chart   = goenModule + "/internal/ui/chart"
	)
	tests := []struct {
		name string
		// generated and written are the files per package, as import path -> source.
		generated map[string]string
		written   map[string]string
		packages  []string
		want      []string
	}{
		{
			name: "a type no template takes is not a view model",
			generated: map[string]string{feature: `package feature
import "github.com/a-h/templ"
func Page(v View) templ.Component { return nil }`},
			written: map[string]string{feature: `package feature
type View struct{ Name string }
type Choice struct{ Key, Value string }`},
			packages: []string{feature},
			want:     []string{feature + ".View"},
		},
		{
			name: "pointer, slice, array, map key and value, channel and func",
			generated: map[string]string{feature: `package feature
import "github.com/a-h/templ"
func Page(p *Ptr, s []Slice, a [3]Array, m map[Key]Value, c chan Chan, f func(Arg) Result, g Generic[Arg2]) templ.Component { return nil }`},
			written: map[string]string{feature: `package feature
type Ptr struct{}
type Slice struct{}
type Array struct{}
type Key struct{}
type Value struct{}
type Chan struct{}
type Arg struct{}
type Result struct{}
type Arg2 struct{}
type Generic[T any] struct{ Item T }
type Untaken struct{}`},
			packages: []string{feature},
			want: []string{
				feature + ".Ptr", feature + ".Slice", feature + ".Array", feature + ".Key", feature + ".Value",
				feature + ".Chan", feature + ".Arg", feature + ".Result", feature + ".Arg2", feature + ".Generic",
			},
		},
		{
			name: "a field, an embedded struct, an unexported struct and a named slice are followed",
			generated: map[string]string{feature: `package feature
import "github.com/a-h/templ"
func Page(v View) templ.Component { return nil }`},
			written: map[string]string{feature: `package feature
type View struct {
	Embedded
	Inner  Inner
	hidden hidden
	Rows   Rows
}
type Embedded struct{}
type Inner struct{ Deep *Deep }
type Deep struct{ Deeper []Deeper }
type Deeper struct{}
type hidden struct{ Shown Shown }
type Shown struct{}
type Rows []Row
type Row struct{}
type Untaken struct{ Embedded }`},
			packages: []string{feature},
			want: []string{
				feature + ".View", feature + ".Embedded", feature + ".Inner", feature + ".Deep",
				feature + ".Deeper", feature + ".Shown", feature + ".Row",
			},
		},
		{
			name: "a method result is read through the receiver, a function result through the call",
			generated: map[string]string{feature: `package feature
import "github.com/a-h/templ"
func Page(v View) templ.Component {
	_ = fromContext(ctx)
	_ = v.Chips(ctx)
	return nil
}`},
			written: map[string]string{feature: `package feature
type View struct{}
type Chip struct{}
type Banner struct{}
type Stray struct{}
func (v View) Chips(ctx int) []Chip { return nil }
func fromContext(ctx int) Banner { return Banner{} }
func unused() Stray { return Stray{} }`},
			packages: []string{feature},
			want:     []string{feature + ".View", feature + ".Chip", feature + ".Banner"},
		},
		{
			name: "the receiver of a component is what it takes",
			generated: map[string]string{feature: `package feature
import "github.com/a-h/templ"
func (r Recv) Render() templ.Component { return nil }
func helper(v NotTaken) string { return "" }`},
			written: map[string]string{feature: `package feature
type Recv struct{}
type NotTaken struct{}`},
			packages: []string{feature},
			want:     []string{feature + ".Recv"},
		},
		{
			name: "across packages: a view package's struct is found; a domain struct and a package outside the list are not",
			generated: map[string]string{feature: `package feature
import (
	"github.com/a-h/templ"
	s "github.com/koopa0/goen/internal/shared"
	"github.com/koopa0/goen/internal/domain"
)
func Page(v s.Shared, d domain.Order) templ.Component { return nil }`,
				chart: `package chart
import "github.com/a-h/templ"
func Plot(s Series) templ.Component { return nil }`,
			},
			written: map[string]string{
				chart: `package chart
type Series struct{ Points []int }`,
				feature: `package feature
type Choice struct{}`,
				shared: `package shared
import "github.com/koopa0/goen/internal/domain"
type Shared struct{ Order domain.Order }`,
				domain: `package domain
type Order struct{ Total int }`,
			},
			packages: []string{feature, shared},
			want:     []string{shared + ".Shared"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parse := func(files map[string]string) []source {
				out := make([]source, 0, len(files))
				for pkg, body := range files {
					file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", body, parser.SkipObjectResolution)
					if err != nil {
						t.Fatalf("parse the fixture for %s: %v", pkg, err)
					}
					out = append(out, source{file: file, pkg: pkg, path: "fixture.go"})
				}
				return out
			}
			var components []component
			for _, src := range parse(tt.generated) {
				components = append(components, componentsOf(src)...)
			}
			taken := templateTakes(parse(tt.written), components, tt.packages)

			var got []string
			for key := range taken {
				got = append(got, key)
			}
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("view models = %v, want %v", got, want)
			}
		})
	}
}
