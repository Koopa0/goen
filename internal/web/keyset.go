package web

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
)

const KeysetParam = "after"

// PageSize is the rows a list page shows; a list query asks for PageLimit, the
// page plus the one row that proves there is another.
const (
	PageSize  = 50
	PageLimit = PageSize + 1
)

// maxKeysetToken bounds what a reader may hand back; a real token is a few
// hundred bytes.
const maxKeysetToken = 4096

// PageOf bounds a list read and says whether it was bounded.
//
// The query asks for one row more than the page shows. If that row came back,
// there is more than a page; the extra row itself is dropped here so nothing
// downstream can count it. That last part is the reason this is a function
// rather than a comparison written out at each call site: the bug it prevents
// is a heading that reads 51.
func PageOf[T any](rows []T, size int) (page []T, more bool) {
	if len(rows) > size {
		return rows[:size], true
	}
	return rows, false
}

// ScopeURL is a list's own address with its non-empty filters. It is the scope
// a keyset position is bound to, so a position taken under one filter is
// refused under another.
func ScopeURL(path string, pairs ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			q.Set(pairs[i], pairs[i+1])
		}
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// NextKeysetURL is the scope with a position token attached. The token is the
// position (a JSON object of the ordering values) bound to the scope, so it is
// worthless anywhere else. ok is false only when the scope is not a URL.
func NextKeysetURL(scope, position string) (next string, ok bool) {
	u, err := url.Parse(scope)
	if err != nil {
		return "", false
	}
	q := u.Query()
	q.Set(KeysetParam, base64.RawURLEncoding.EncodeToString([]byte(scope+"\n"+position)))
	u.RawQuery = q.Encode()
	return u.String(), true
}

// ReadKeyset decodes a token made by NextKeysetURL into the caller's position
// type. ok is false, and the caller starts at the first page, for anything that
// is not a well-formed token minted for exactly this scope. The caller still
// checks the decoded values against what its own query can accept.
func ReadKeyset[T any](scope, token string) (position T, ok bool) {
	var zero T
	if token == "" || len(token) > maxKeysetToken {
		return zero, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return zero, false
	}
	saved, body, found := strings.Cut(string(raw), "\n")
	if !found || saved != scope {
		return zero, false
	}
	if json.Unmarshal([]byte(body), &position) != nil {
		return zero, false
	}
	return position, true
}

// ResumeKeyset is ReadKeyset for the optional position argument of a list read.
// ok is false for the first page, for a token this list did not mint, and for a
// position the list's own check refuses: a zero identifier is no row.
func ResumeKeyset[T any](scope string, after []string, acceptable func(T) bool) (from T, ok bool) {
	var zero T
	if len(after) == 0 {
		return zero, false
	}
	p, valid := ReadKeyset[T](scope, after[0])
	if !valid || !acceptable(p) {
		return zero, false
	}
	return p, true
}

// Bound is where one page of a keyset list sits, for the navigation beside it.
type Bound struct {
	// PastEnd marks a later page that came back empty because its rows are gone; the
	// list's own empty state would be a lie there.
	PastEnd bool
	First   string
	Next    string
}

// PageBound trims a read made with PageLimit to its page and builds the
// navigation beside it. resumed is whether the request carried a valid position;
// key names the last row's position, which each query builds in SQL as
// PageCursor.
func PageBound[T any](scope string, resumed bool, rows []T, size int, key func(*T) string) ([]T, Bound) {
	rows, more := PageOf(rows, size)
	var b Bound
	if resumed {
		b.First = scope
	}
	if len(rows) == 0 {
		b.PastEnd = resumed
		return rows, b
	}
	if more {
		b.Next, _ = NextKeysetURL(scope, key(&rows[len(rows)-1]))
	}
	return rows, b
}
