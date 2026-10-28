// Package pagestatus is the HTTP status a page is being rendered for, carried in
// the render's context so the page can say in its own head what the status line
// says; it holds With and From; it does not hold writing the response, which is
// package respond, or the page's frame, which is package ui/frame.
package pagestatus

import "context"

type key struct{}

// With returns ctx carrying the status the page rendered under it answers with.
func With(ctx context.Context, status int) context.Context {
	return context.WithValue(ctx, key{}, status)
}

// From is the status carried by ctx, or 0 when none is.
func From(ctx context.Context) int {
	if status, ok := ctx.Value(key{}).(int); ok {
		return status
	}
	return 0
}
