package email

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Message struct {
	To      string
	Subject string
	// Body is plain text, the letter as written.
	Body string
	// HTML is Body laid out as a page, sent beside it as an alternative. Empty
	// sends Body alone.
	HTML string
	// OneClickUnsubscribe emits only the paired RFC 8058 headers.
	OneClickUnsubscribe OneClickUnsubscribeURL
}

type OneClickUnsubscribeURL string

func (u OneClickUnsubscribeURL) validate() error {
	if u == "" {
		return nil
	}
	link, err := url.ParseRequestURI(string(u))
	if err != nil || link.Scheme != "https" || link.Host == "" || link.User != nil || link.Fragment != "" ||
		strings.ContainsAny(string(u), "<>\"#") || strings.IndexFunc(string(u), func(r rune) bool { return r <= ' ' || r >= 127 }) >= 0 {
		return errors.New("email: invalid HTTPS one-click unsubscribe URL")
	}
	return nil
}

type Sender interface {
	Send(ctx context.Context, m *Message) error
}

// LogSender writes mail to the log instead of sending it, for development. It
// never logs the BODY: mailed tokens travel in it, and this sender returns nil,
// so an unconfigured deployment would log every live credential goen issues.
type LogSender struct {
	Log *slog.Logger
	// ShowBody puts the body back, for local development of a flow whose whole
	// point is the link inside it. Off by the zero value.
	ShowBody bool
}

func (s LogSender) Send(ctx context.Context, m *Message) error {
	attrs := []any{"to", m.To, "subject", m.Subject, "body_bytes", len(m.Body)}
	if s.ShowBody {
		attrs = append(attrs, "body", m.Body)
	}
	s.Log.InfoContext(ctx, "email (not sent: no SMTP configured)", attrs...)
	return nil
}

type SMTPSender struct {
	Addr string // host:port
	From string
	Auth smtp.Auth
	// TLSName is the server name to verify the certificate against, kept
	// separate from Addr so a deployment behind a proxy can still verify the
	// name it means.
	TLSName string
}

const SendTimeout = 30 * time.Second

var (
	smtpConnMu sync.Mutex
	smtpConn   = func(ctx context.Context, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
)

func dialSMTPConn(ctx context.Context, addr string) (net.Conn, error) {
	smtpConnMu.Lock()
	dial := smtpConn
	smtpConnMu.Unlock()
	return dial(ctx, addr)
}

// Send delivers m. STARTTLS is required, not attempted.
func (s SMTPSender) Send(ctx context.Context, m *Message) error {
	if s.Addr == "" {
		return errors.New("email: no SMTP address configured")
	}
	if !Valid(m.To) {
		return fmt.Errorf("email: refusing to send to %q", m.To)
	}
	if err := m.OneClickUnsubscribe.validate(); err != nil {
		return err
	}
	// Resolved BEFORE anything is dialled: an unsendable From fails every message.
	envelope, err := envelopeFrom(s.From)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()

	conn, err := dialSMTPConn(ctx, s.Addr)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck // best-effort cleanup

	stop, err := prepareSMTPConn(conn, ctx)
	if err != nil {
		return err
	}
	defer stop()

	host := s.TLSName
	if host == "" {
		h, _, splitErr := net.SplitHostPort(s.Addr)
		if splitErr != nil {
			return fmt.Errorf("smtp address %q: %w", s.Addr, splitErr)
		}
		host = h
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = c.Quit() }() //nolint:errcheck // best-effort cleanup

	return deliver(c, s, m, host, envelope)
}

// prepareSMTPConn binds the delivery context to conn. The delivery deadline is
// installed before the cancellation hook so a cancel that fires while the
// deadline is being set cannot be overwritten by a later SetDeadline.
func prepareSMTPConn(conn net.Conn, ctx context.Context) (stop func() bool, err error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set smtp deadline: %w", err)
		}
	}
	stop = context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now()) //nolint:errcheck // best-effort interrupt
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		_ = conn.SetDeadline(time.Now()) //nolint:errcheck // cancelled before hook ran
	}
	return stop, nil
}

// envelopeFrom is the address that goes in MAIL FROM: a bare addr-spec and never
// the display-name form, because RFC 5321 wants only the address between the
// angle brackets and net/smtp writes whatever it is handed.
func envelopeFrom(from string) (string, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(from))
	if err != nil {
		return "", fmt.Errorf("email: From %q is not an address: %w", from, err)
	}
	if !Valid(addr.Address) {
		return "", fmt.Errorf("email: From %q carries no sendable address", from)
	}
	return addr.Address, nil
}

// deliver runs the SMTP conversation. envelope is the reverse-path, already
// reduced to a bare address; s.From keeps its display name for the HEADER.
func deliver(c *smtp.Client, s SMTPSender, m *Message, host, envelope string) error {
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return errors.New("email: server does not offer STARTTLS")
	}
	if tlsErr := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); tlsErr != nil {
		return fmt.Errorf("starttls: %w", tlsErr)
	}
	if s.Auth != nil {
		if authErr := c.Auth(s.Auth); authErr != nil {
			return fmt.Errorf("smtp auth: %w", authErr)
		}
	}
	if mailErr := c.Mail(envelope); mailErr != nil {
		return fmt.Errorf("smtp from: %w", mailErr)
	}
	if rcptErr := c.Rcpt(m.To); rcptErr != nil {
		return fmt.Errorf("smtp rcpt: %w", rcptErr)
	}
	wc, dataErr := c.Data()
	if dataErr != nil {
		return fmt.Errorf("smtp data: %w", dataErr)
	}
	if _, writeErr := wc.Write(render(s.From, m)); writeErr != nil {
		return fmt.Errorf("smtp write: %w", writeErr)
	}
	if closeErr := wc.Close(); closeErr != nil {
		return fmt.Errorf("smtp close: %w", closeErr)
	}
	return nil
}

// render builds the wire format. The subject is encoded per RFC 2047, or a
// Traditional Chinese subject line arrives as mojibake.
//
// A message with HTML is multipart/alternative, text first: a client shows the
// last part it can display, so one that cannot show HTML still has the letter.
// Its Content-Transfer-Encoding is 7bit, which both base64 parts make true, so
// the MIME headers are the same ones a text-only letter has. Newsletter issues
// also carry the paired unsubscribe headers.
//
// Date and Message-ID are generated here and never from the message: RFC 5322
// requires the first, and the order confirmation is a record the consumer keeps,
// so it carries the time it was sent. Neither takes text from a caller.
func render(from string, m *Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + m.To + "\r\n")
	b.WriteString("Subject: " + encodeHeader(m.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + messageID(from) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	if m.OneClickUnsubscribe != "" {
		b.WriteString("List-Unsubscribe: <" + string(m.OneClickUnsubscribe) + ">\r\n")
		b.WriteString("List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n")
	}
	if m.HTML == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
		b.WriteString("\r\n")
		b.WriteString(crlf(m.Body))
		return []byte(b.String())
	}
	boundary := rand.Text()
	b.WriteString(`Content-Type: multipart/alternative; boundary="` + boundary + "\"\r\n")
	b.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	b.WriteString("\r\n")
	writePart(&b, boundary, "text/plain", m.Body)
	writePart(&b, boundary, "text/html", m.HTML)
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

// messageID is a unique id in the sender's own domain, so a receiver can thread
// and de-duplicate by it. An unparsable From has already failed the send in
// envelopeFrom; the fallback only keeps render total.
func messageID(from string) string {
	domain := "goen.invalid"
	if addr, err := mail.ParseAddress(from); err == nil {
		if _, host, ok := strings.Cut(addr.Address, "@"); ok && host != "" {
			domain = host
		}
	}
	return "<" + uuid.NewString() + "@" + domain + ">"
}

// writePart writes one base64 part. Base64 rather than quoted-printable: it
// carries a Chinese letter in under half the bytes, and its alphabet has no
// "-", so no line of a part can be read as the boundary.
func writePart(b *strings.Builder, boundary, mediaType, text string) {
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: " + mediaType + "; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")
	// 76 is RFC 2045's limit for a base64 line.
	encoded := base64.StdEncoding.EncodeToString([]byte(crlf(text)))
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")
}

// crlf makes every line break CRLF, whichever form it arrived in: SMTP is a
// CRLF protocol, and a bare CR or a bare LF is what one server reads as the end
// of a line and the next does not, so the two can disagree about where the
// message ends. A text part is decoded to the same form, which RFC 2046 makes
// the canonical one.
func crlf(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func encodeHeader(s string) string {
	return mime.QEncoding.Encode("utf-8", s)
}
