package campaign

import "strconv"

// DealsPageSize is how many discounted products /deals shows on one page.
const DealsPageSize = 24

// maxPage stops a URL from making the database count its way past the end of
// the deals or the listed campaigns.
const maxPage = 500

// parsePage reads a 1-based page number; anything unparseable or below 1 is
// the first page, and anything past maxPage is maxPage.
func parsePage(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	if n > maxPage {
		return maxPage
	}
	return n
}

func offsetFor(page int) int32 {
	if page <= 1 {
		return 0
	}
	if page > maxPage {
		page = maxPage
	}
	return int32((page - 1) * DealsPageSize)
}
