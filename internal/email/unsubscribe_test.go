package email

import (
	"bytes"
	"net/mail"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestNewsletterIssuesCarryOneClickHeaders(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			const token = "token+/\r\nBcc: unwanted@example.net" //nolint:gosec // Synthetic header-injection fixture.
			sink := &recorded{}
			n := New(sink, "https://goen.test/", "", "")
			if err := n.SendNewsletterIssue(t.Context(), &NewsletterIssue{
				Locale: string(locale), Email: "reader@example.com", Subject: "issue", Body: "issue body", UnsubscribeToken: token,
			}); err != nil {
				t.Fatal(err)
			}
			if len(sink.msgs) != 1 {
				t.Fatalf("sent %d copies, want 1", len(sink.msgs))
			}
			letter := sink.msgs[0]
			wire := render("goen <no-reply@goen.test>", letter)
			parsed, err := mail.ReadMessage(bytes.NewReader(wire))
			if err != nil {
				t.Fatal(err)
			}
			link := "https://goen.test/newsletter/unsubscribe?token=" + url.QueryEscape(token)
			if got := parsed.Header.Get("List-Unsubscribe"); got != "<"+link+">" {
				t.Errorf("List-Unsubscribe = %q, want <%s>", got, link)
			}
			if got := parsed.Header.Get("List-Unsubscribe-Post"); got != "List-Unsubscribe=One-Click" {
				t.Errorf("List-Unsubscribe-Post = %q", got)
			}
			if !strings.Contains(letter.Body, link) || !strings.Contains(letter.HTML, link) {
				t.Error("the body and header must carry the same escaped unsubscribe link")
			}
			if parsed.Header.Get("Bcc") != "" || len(wireHeaders(letter)) != 10 {
				t.Error("the unsubscribe token added an unexpected header")
			}
		})
	}
}

func TestNewsletterOneClickRequiresAnHTTPSOrigin(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"http://goen.test", "https://goen.test\r\nBcc: unwanted@example.net", "https://goen.test>"} {
		sink := &recorded{}
		err := New(sink, base, "", "").SendNewsletterIssue(t.Context(), &NewsletterIssue{
			Email: "reader@example.com", Subject: "issue", Body: "body", UnsubscribeToken: "token",
		})
		if err == nil || len(sink.msgs) != 0 {
			t.Errorf("base URL %q: error=%v sent=%d, want refusal before sending", base, err, len(sink.msgs))
		}
	}
}

func TestSMTPRefusesUnsafeOneClickHeadersBeforeDialling(t *testing.T) {
	t.Parallel()
	for _, link := range []string{
		"http://goen.test/newsletter/unsubscribe?token=tok",
		"https://goen.test/\r\nBcc: unwanted@example.net",
		"https://goen.test/><mailto:unwanted@example.net>",
		"https://user:pass@goen.test/newsletter/unsubscribe",
		"https://goen.test/newsletter/unsubscribe#fragment",
	} {
		err := (SMTPSender{Addr: "smtp.goen.invalid:587", From: "sender@goen.test"}).Send(t.Context(), &Message{
			To: "reader@example.com", OneClickUnsubscribe: OneClickUnsubscribeURL(link),
		})
		if err == nil || err.Error() != "email: invalid HTTPS one-click unsubscribe URL" {
			t.Errorf("URL %q: error=%v, want validation before SMTP", link, err)
		}
	}
}
