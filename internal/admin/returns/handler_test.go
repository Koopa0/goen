package returns

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestNoticesCoverRedirects: every redirect the return forms make carries a
// message.
func TestNoticesCoverRedirects(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`answerAt\(r, "([a-z]+)"\)`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
	}
	for _, kind := range []returns.DecisionKind{returns.DecisionApprove, returns.DecisionException, returns.DecisionReject} {
		sent[decidedNotice(kind)] = true
	}
	// A literal address would escape this check and ignore a return's own page.
	if literal := regexp.MustCompile(`"/admin/returns\?[^"]*"`).FindString(string(src)); literal != "" {
		t.Errorf("handler.go redirects to %s; answer through answerAt", literal)
	}
	for name := range sent {
		if _, ok := notices[name]; !ok {
			t.Errorf("?%s=1 carries no message: the page renders nothing after the button", name)
		}
	}
	for name := range notices {
		if !sent[name] {
			t.Errorf("notice %q names a parameter no redirect writes", name)
		}
	}
}

// TestDecidedNoticeSaysTheDecision: each decision the form allows has its own
// notice, and none of them says money moved.
func TestDecidedNoticeSaysTheDecision(t *testing.T) {
	t.Parallel()
	for kind, want := range map[returns.DecisionKind]string{
		returns.DecisionApprove:   "approved",
		returns.DecisionException: "exception",
		returns.DecisionReject:    "declined",
	} {
		got := decidedNotice(kind)
		if got != want {
			t.Errorf("decidedNotice(%q) = %q, want %q", kind, got, want)
		}
		entry, ok := notices[got]
		if !ok || entry.Outcome != widget.OutcomeDone {
			t.Errorf("decidedNotice(%q) names ?%s=1, which is not a done notice", kind, got)
		}
	}
}

// TestAnswerAt: a form answers to the queue, or to its return's own page when
// it was posted from there, and the page's address comes from the path id.
func TestAnswerAt(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	tests := []struct {
		name   string
		from   string
		pathID string
		notice string
		want   string
	}{
		{"queue with a notice", "", id.String(), "approved", "/admin/returns?approved=1"},
		{"queue without a notice", "", id.String(), "", "/admin/returns"},
		{"own page with a notice", "return", id.String(), "approved", "/admin/returns?approved=1&request=" + id.String()},
		{"own page without a notice", "return", id.String(), "", "/admin/returns?request=" + id.String()},
		{"a flag that is not ours", "elsewhere", id.String(), "closed", "/admin/returns?closed=1"},
		{"a path id that is not an id", "return", "../x", "closed", "/admin/returns?closed=1"},
		{"the nil id", "return", uuid.Nil.String(), "closed", "/admin/returns?closed=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			form := url.Values{"from": {tt.from}}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/returns/x/decide", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("id", tt.pathID)
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := answerAt(req, tt.notice); got != tt.want {
				t.Errorf("answerAt(from=%q, id=%q, %q) = %q, want %q", tt.from, tt.pathID, tt.notice, got, tt.want)
			}
		})
	}
}

// TestNoticesDoneSentencesDiffer: no two actions share a sentence, so a save says
// which change it was.
func TestNoticesDoneSentencesDiffer(t *testing.T) {
	t.Parallel()
	seen := map[i18n.Key]string{}
	for name, e := range notices {
		if e.Outcome != widget.OutcomeDone {
			continue
		}
		if first, dup := seen[e.Key]; dup {
			t.Errorf("?%s=1 and ?%s=1 both say %q", first, name, e.Key)
		}
		seen[e.Key] = name
	}
}
