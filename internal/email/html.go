package email

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
)

// send attaches the HTML part and hands the letter to the sender. Every letter
// goes through here, so none reaches a customer without its header.
func (n Notifier) send(ctx context.Context, m *Message) error {
	html, err := n.html(ctx, m.Body)
	if err != nil {
		return err
	}
	m.HTML = html
	return n.sender.Send(ctx, m)
}

// html lays the text out as the HTML part. It is built from the text rather
// than written beside it, so the two parts cannot say different things.
func (n Notifier) html(ctx context.Context, text string) (string, error) {
	origin := strings.TrimRight(n.baseURL, "/")
	var b strings.Builder
	err := letterHTML(i18n.FromContext(ctx).Tag(), origin+assets.URL(assets.EmailHeader),
		paragraphs(text, origin)).Render(ctx, &b)
	if err != nil {
		return "", fmt.Errorf("email: lay out the HTML part: %w", err)
	}
	return b.String(), nil
}

// segment is a run of a letter's text, or one link in it.
type segment struct {
	text string
	link bool
}

// paragraph is one blank-line-separated block of a letter, line by line.
type paragraph [][]segment

// paragraphs splits a letter into its blocks, and each line into text and the
// links goen wrote into it.
func paragraphs(text, origin string) []paragraph {
	var out []paragraph
	var block paragraph
	for line := range strings.SplitSeq(crlf(text), "\r\n") {
		if strings.TrimSpace(line) == "" {
			if block != nil {
				out = append(out, block)
				block = nil
			}
			continue
		}
		block = append(block, segments(line, origin))
	}
	if block != nil {
		out = append(out, block)
	}
	return out
}

// mailedURL is a URL as a letter spells one: it ends at whitespace or at a
// character RFC 3986 does not allow, so a fullwidth colon or a Chinese full
// stop beside it stays text.
var mailedURL = regexp.MustCompile(`https?://[A-Za-z0-9\-._~:/?#\[\]@!$&'*+,;=%]+`)

// segments marks the URLs in line that point at goen itself. Only those become
// links: a name, a carrier or a product name is somebody else's text, and a URL
// typed into one must not arrive as a link goen's own letter asks to be
// followed.
func segments(line, origin string) []segment {
	var out []segment
	rest := 0
	for _, at := range mailedURL.FindAllStringIndex(line, -1) {
		// A sentence's own punctuation after a URL is not part of it.
		end := at[0] + len(strings.TrimRight(line[at[0]:at[1]], ".,;:!?'"))
		u := line[at[0]:end]
		if !strings.HasPrefix(u, origin+"/") {
			continue
		}
		if at[0] > rest {
			out = append(out, segment{text: line[rest:at[0]]})
		}
		out = append(out, segment{text: u, link: true})
		rest = end
	}
	if rest < len(line) {
		out = append(out, segment{text: line[rest:]})
	}
	return out
}
