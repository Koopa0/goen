package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// Message is one email.
type Message struct {
	To      string
	Subject string
	// Body is plain text. goen sends no HTML mail: a transactional message is
	// six lines and a link, HTML doubles the work for every client quirk, and a
	// text-only receipt is the one that renders everywhere.
	Body string
}

// Sender delivers a message. Defined here, by the package that produces
// messages, because it is the boundary the outbox handlers depend on.
type Sender interface {
	Send(ctx context.Context, m *Message) error
}

// LogSender writes mail to the log instead of sending it.
//
// The development default, and deliberately not a silent no-op: a developer
// checking whether the confirmation fired should see that it fired, to whom,
// and which of the eight messages it was.
//
// # What it does NOT write, and why
//
// The BODY. A password reset link, an email verification token and a newsletter
// unsubscribe token all travel in the body — they have to, because sending from
// the handler loses the message when the process dies mid-send — and this sender
// returns nil, so the outbox stamps the message DELIVERED and nothing anywhere
// looks wrong. A deployment that reaches production with no SMTP address
// configured therefore writes every live credential goen issues into a log that
// is shipped to an aggregator, kept for a year, and readable by everybody except
// the person the letter was for.
//
// The recipient and the subject stay. They answer the questions a developer
// actually asks of this line — did it fire, to whom, and which message — and
// neither is a credential: the address is the customer's own and the subject is
// goen's own words. The body's LENGTH stays with them, so "the letter went out
// empty" is still visible without the letter being in the log.
type LogSender struct {
	Log *slog.Logger
	// ShowBody puts the body back, for local development of a flow whose whole
	// point is the link inside it — /forgot and /verify cannot be walked through
	// without one.
	//
	// Off by the zero value, which is the only shape main constructs, so this is
	// something a developer turns on for themselves and never something a
	// deployment acquires by omission. It is the difference between a log and a
	// list of live tokens.
	ShowBody bool
}

// Send records the message.
func (s LogSender) Send(ctx context.Context, m *Message) error {
	attrs := []any{"to", m.To, "subject", m.Subject, "body_bytes", len(m.Body)}
	if s.ShowBody {
		attrs = append(attrs, "body", m.Body)
	}
	s.Log.InfoContext(ctx, "email (not sent: no SMTP configured)", attrs...)
	return nil
}

// SMTPSender delivers over SMTP.
type SMTPSender struct {
	Addr string // host:port
	From string
	Auth smtp.Auth
	// TLSName is the server name to verify the certificate against. It is the
	// host, kept separate so a deployment that connects through a proxy can
	// still verify the name it means.
	TLSName string
}

// SendTimeout bounds one delivery. An SMTP server that accepts a connection and
// then stops talking would otherwise hold a worker slot forever.
const SendTimeout = 30 * time.Second

// Send delivers m.
//
// STARTTLS is required, not attempted: mail carrying an order number and a
// delivery address must not cross a network in the clear, and a server that
// cannot upgrade is a misconfiguration rather than a reason to downgrade.
func (s SMTPSender) Send(ctx context.Context, m *Message) error {
	if s.Addr == "" {
		return errors.New("email: no SMTP address configured")
	}
	if !Valid(m.To) {
		return fmt.Errorf("email: refusing to send to %q", m.To)
	}
	// Resolved BEFORE anything is dialled. A From that is not a sendable address
	// is a misconfiguration every message will meet, and there is nothing to be
	// learned by finding it out one socket and one TLS handshake later.
	envelope, err := envelopeFrom(s.From)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	// The client's Quit below closes it on the happy path; this is the safety
	// net for every early return, and a close error there tells nobody
	// anything.
	defer func() { _ = conn.Close() }() //nolint:errcheck // best-effort cleanup

	// The deadline the context already carries, put on the SOCKET.
	//
	// SendTimeout reached DialContext and nothing after it. net/smtp has no
	// context-aware call, so the greeting, STARTTLS, AUTH, MAIL, RCPT and the
	// whole of DATA ran with no deadline at all: a server that accepts a
	// connection and then says nothing — a black hole, a half-closed NAT, a
	// provider under load — held the sending goroutine forever. The outbox
	// delivers a batch serially, so one such server does not stall one message,
	// it stalls every email goen sends.
	if deadline, ok := ctx.Deadline(); ok {
		if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
			return fmt.Errorf("set smtp deadline: %w", deadlineErr)
		}
	}

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
	// Quit is the polite close; the deferred conn.Close above is what actually
	// releases the socket if it fails.
	defer func() { _ = c.Quit() }() //nolint:errcheck // best-effort cleanup

	return deliver(c, s, m, host, envelope)
}

// envelopeFrom is the address that goes in MAIL FROM.
//
// A bare addr-spec and never the display-name form. `goen <no-reply@goen.example>`
// — the default this repository ships — is a correct `From:` HEADER and an
// invalid reverse-path: RFC 5321 wants `<no-reply@goen.example>` between the
// angle brackets and net/smtp writes whatever it is handed, so a strict server
// answers 501 and every message goen sends fails, while a lenient one accepts a
// bounce address that is not an address.
//
// The predicate is this package's own. [Valid] is what every field collecting an
// address is already held to, and it was applied to m.To one line above the
// place From was passed through untouched — the two halves of one rule, only one
// of which was enforced.
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

// deliver runs the SMTP conversation.
//
// Split from Send so that function stays under the complexity limit; the split
// is at the natural seam anyway — Send establishes a connection, this talks the
// protocol over it.
//
// envelope is the reverse-path, already reduced to a bare address by
// [envelopeFrom]. s.From keeps its display name because the HEADER is where the
// display name belongs and is read.
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

// render builds the wire format.
//
// The subject is encoded as UTF-8 base64 per RFC 2047: a Traditional Chinese
// subject line sent as raw bytes arrives as mojibake in about half of the
// clients that exist.
func render(from string, m *Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + m.To + "\r\n")
	b.WriteString("Subject: " + encodeHeader(m.Subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	// Lone newlines become CRLF: SMTP is a CRLF protocol, and a bare LF in the
	// body is the kind of thing one server accepts and the next rejects.
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(b.String())
}

// encodeHeader makes a header value safe for the wire.
//
// mime.QEncoding rather than hand-rolled base64: it also folds long lines and
// leaves plain ASCII alone, so an English subject stays readable in a raw
// message dump.
func encodeHeader(s string) string {
	return mime.QEncoding.Encode("utf-8", s)
}
