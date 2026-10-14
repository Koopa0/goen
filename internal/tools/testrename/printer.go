package main

import (
	"fmt"
	"io"
)

// printer writes to a stream and remembers the first failure, so a command
// reports it once at the end instead of after every line.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

func (p *printer) println(args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintln(p.w, args...)
}
