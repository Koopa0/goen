// Package pages renders goen's page bodies. A page never emits document
// chrome of its own: it supplies content to [layouts.Base] and declares the
// view models handlers fill in.
//
// The package comment lives here rather than beside a template because templ
// writes its output into generated files, which the linters skip.
package pages
