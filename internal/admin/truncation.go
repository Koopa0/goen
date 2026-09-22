package admin

// pageOf bounds a list read and says whether it was bounded.
//
// Every admin list reads PageSize rows and shows them. Nothing has ever said so
// on the page, which means a staff member cannot tell fifty messages from fifty
// of nine hundred — and the fifty they are looking at are the oldest unhandled
// ones, so the difference is the whole of what they are for.
//
// The query asks for one row more than the page shows. If that row came back,
// there is more than a page and the list says so; the extra row itself is
// dropped here so nothing downstream can count it. That last part is the reason
// this is a function rather than a comparison written out at each call site:
// the bug it prevents is a heading that reads 51.
func pageOf[T any](rows []T, size int) (page []T, more bool) {
	if len(rows) > size {
		return rows[:size], true
	}
	return rows, false
}

// PageLimit is what a list query asks for: the page, plus the one row that
// proves there is another.
const PageLimit = PageSize + 1
