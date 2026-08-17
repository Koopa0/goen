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
	// Body is plain text. goen sends no HTML mail.
	Body string
}

// Sender delivers a message.
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
	// TLSName is the server name to verify the certificate against, kept
	// separate from Addr so a deployment behind a proxy can still verify the
	// name it means.
	TLSName string
}

// SendTimeout bounds one delivery.
const SendTimeout = 30 * time.Second

// Send delivers m. STARTTLS is required, not attempted.
func (s SMTPSender) Send(ctx context.Context, m *Message) error {
	if s.Addr == "" {
		return errors.New("email: no SMTP address configured")
	}
	if !Valid(m.To) {
		return fmt.Errorf("email: refusing to send to %q", m.To)
	}
	// Resolved BEFORE anything is dialled: an unsendable From fails every message.
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
	defer func() { _ = conn.Close() }() //nolint:errcheck // best-effort cleanup

	// The context's deadline, put on the SOCKET. net/smtp has no context-aware
	// call, so without this the greeting, STARTTLS, AUTH, MAIL, RCPT and DATA all
	// run unbounded and a server that accepts and goes quiet stalls the worker.
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
	defer func() { _ = c.Quit() }() //nolint:errcheck // best-effort cleanup

	return deliver(c, s, m, host, envelope)
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
func render(from string, m *Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + m.To + "\r\n")
	b.WriteString("Subject: " + encodeHeader(m.Subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	// Lone newlines become CRLF: SMTP is a CRLF protocol, and a bare LF is the
	// kind of thing one server accepts and the next rejects.
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(b.String())
}

// encodeHeader makes a header value safe for the wire.
func encodeHeader(s string) string {
	return mime.QEncoding.Encode("utf-8", s)
}
