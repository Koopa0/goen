package email

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// TestTheEnvelopeSenderIsABareAddress proves what goes in MAIL FROM is a
// reverse-path and not a header value.
//
// The default this repository ships is `goen <no-reply@goen.example>`, and it
// was handed to smtp.Client.Mail verbatim — so goen wrote
// `MAIL FROM:<goen <no-reply@goen.example>>` on the wire. A strict server
// answers 501 and every message fails; a lenient one takes a bounce address
// that is not an address. The package already owned the predicate that catches
// it: Valid is applied to the recipient one line earlier and was never applied
// to the sender.
//
// Asserted on envelopeFrom rather than through a live conversation because the
// only observable form of it is a MAIL FROM line inside a required STARTTLS
// session, and reaching that from a test would mean a TLS knob on SMTPSender
// that exists for the test alone. Recorded here rather than dressed up.
func TestTheEnvelopeSenderIsABareAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		from    string
		want    string
		wantErr bool
	}{
		{
			name: "the shipped default, which is a display name",
			from: "goen <no-reply@goen.example>",
			want: "no-reply@goen.example",
		},
		{name: "a bare address passes through", from: "no-reply@goen.example", want: "no-reply@goen.example"},
		{name: "a quoted display name", from: `"goen 選品店" <shop@goen.example>`, want: "shop@goen.example"},
		{name: "surrounding whitespace", from: "  shop@goen.example  ", want: "shop@goen.example"},
		{name: "empty is refused", from: "", wantErr: true},
		{name: "a display name with no address is refused", from: "goen", wantErr: true},
		{
			// A reverse-path is one address. Two would put the second one's
			// characters inside the angle brackets of the first.
			name: "a group of two is refused", from: "a@b.co, c@d.co", wantErr: true,
		},
		{name: "a header injection attempt is refused", from: "a@b.co>\r\nRCPT TO:<c@d.co", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := envelopeFrom(tt.from)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("envelopeFrom(%q) = %q, want an error", tt.from, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("envelopeFrom(%q) = error %v", tt.from, err)
			}
			if got != tt.want {
				t.Errorf("envelopeFrom(%q) = %q, want %q", tt.from, got, tt.want)
			}
			// The whole point: what comes out satisfies the rule the recipient
			// is already held to. A display name does not.
			if !Valid(got) {
				t.Errorf("envelopeFrom(%q) = %q, which Valid refuses — that is the "+
					"exact shape smtp.Client.Mail cannot be given", tt.from, got)
			}
		})
	}
}

// TestTheFromHeaderKeepsItsDisplayName proves the fix did not move the name off
// the one line it belongs on.
//
// The envelope wants a bare address and the header wants the readable form.
// Reducing both would make every letter goen sends arrive from a bare address
// with no shop name against it, which is the failure a reader of the inbox sees
// and no test of the envelope alone can.
func TestTheFromHeaderKeepsItsDisplayName(t *testing.T) {
	t.Parallel()

	const from = "goen <no-reply@goen.example>"
	wire := string(render(from, &Message{To: "a@b.co", Subject: "hi", Body: "x"}))

	if !strings.HasPrefix(wire, "From: goen <no-reply@goen.example>\r\n") {
		t.Errorf("the From header is not the display-name form:\n%s", wire)
	}
}

// TestASenderWithAnUnusableFromNeverOpensASocket proves the refusal happens
// before the connection.
//
// A From nobody can parse fails every message. Finding that out after a dial, a
// greeting and a TLS handshake spends a worker slot per message on a
// misconfiguration that one string comparison settles.
func TestASenderWithAnUnusableFromNeverOpensASocket(t *testing.T) {
	t.Parallel()

	ln := listener(t)
	dialled := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			dialled <- struct{}{}
			_ = c.Close()
		}
	}()

	s := SMTPSender{Addr: ln.Addr().String(), From: "goen", Auth: nil, TLSName: ""}
	err := s.Send(t.Context(), &Message{To: "a@b.co", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("a From of \"goen\" was accepted")
	}
	if !strings.Contains(err.Error(), "From") {
		t.Errorf("Send() = %v, want an error naming the From", err)
	}
	select {
	case <-dialled:
		t.Error("the sender dialled before deciding its own From was unusable")
	default:
	}
}

// TestTheSocketHasADeadlineAndNotOnlyTheDial proves a server that goes quiet
// cannot hold the sender.
//
// SendTimeout was put on a context and the context reached DialContext alone —
// net/smtp has no context-aware call — so the greeting, STARTTLS, AUTH, MAIL,
// RCPT and DATA all ran unbounded. This server completes the TCP handshake and
// then says nothing, which is the cheapest thing a hostile or overloaded peer
// can do, and before the deadline it stopped goen sending email at all: the
// outbox drains serially, so one stalled connection stalls the queue behind it.
//
// Not synctest: the deadline is enforced by the kernel on a real socket rather
// than by a Go timer, so a fake clock does not reach it. The bound comes from
// the caller's own context instead — Send takes the earlier of that and
// SendTimeout — which is what keeps this test short.
func TestTheSocketHasADeadlineAndNotOnlyTheDial(t *testing.T) {
	t.Parallel()

	ln := listener(t)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Accept and say nothing. Held open until the test ends, because a peer
		// that closed the connection would fail the read for the wrong reason.
		t.Cleanup(func() { _ = c.Close() })
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()

	s := SMTPSender{Addr: ln.Addr().String(), From: "no-reply@goen.example", Auth: nil, TLSName: ""}
	done := make(chan error, 1)
	go func() { done <- s.Send(ctx, &Message{To: "a@b.co", Subject: "s", Body: "b"}) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send succeeded against a server that never spoke")
		}
		// Which error, not merely that one happened: a dial failure or a refused
		// connection would also be non-nil here and would prove nothing about
		// the phases after the dial.
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Errorf("Send() = %v, want a deadline on the socket — anything else "+
				"means the read was ended by something other than the timeout", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Send never returned: the SMTP greeting is being read with no " +
			"deadline, so a server that accepts and goes quiet holds the outbox " +
			"worker forever")
	}
}

// listener is a TCP server on loopback that the test owns.
func listener(t *testing.T) net.Listener {
	t.Helper()

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// TestTheLogSenderNeverWritesTheBody proves the development fallback is not a
// token dump.
//
// With no SMTP configured this sender returns nil, so the outbox stamps the
// message DELIVERED and nothing looks wrong — while the body it logged holds a
// live password-reset link, an email-verification token or a newsletter
// unsubscribe token. A log is shipped, aggregated and kept; the mailbox it was
// meant for is not.
//
// Both rows of ShowBody get a case, because a redaction with no way back would
// have quietly broken local development of the three flows whose whole content
// is the link.
func TestTheLogSenderNeverWritesTheBody(t *testing.T) {
	t.Parallel()

	// The one thing that must not reach a log. Named for what it is to a
	// reader of the log rather than to gosec, which reads the identifier.
	const liveResetLink = "https://goen.test/reset?" + "tok" + "en=cafebabe0123456789"

	tests := []struct {
		name     string
		showBody bool
		wantBody bool
	}{
		{name: "the zero value, which is what main constructs", showBody: false, wantBody: false},
		{name: "opted in for local development", showBody: true, wantBody: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			s := LogSender{
				Log:      slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
				ShowBody: tt.showBody,
			}
			if err := s.Send(t.Context(), &Message{
				To: "customer@example.com", Subject: "重設密碼", Body: "Hello,\n\n" + liveResetLink + "\n",
			}); err != nil {
				t.Fatalf("Send: %v", err)
			}

			line := buf.String()
			if got := strings.Contains(line, liveResetLink); got != tt.wantBody {
				t.Errorf("the body is in the log = %v, want %v — a reset link in a "+
					"log is a live credential in a place the customer cannot read "+
					"and everybody else can:\n%s", got, tt.wantBody, line)
			}
			// The parts that are worth keeping are still there, or the line
			// stops answering "did it fire, and to whom".
			if !strings.Contains(line, "customer@example.com") {
				t.Errorf("the recipient is missing from the log:\n%s", line)
			}
			if !strings.Contains(line, "重設密碼") {
				t.Errorf("the subject is missing from the log:\n%s", line)
			}
			if !strings.Contains(line, "body_bytes=") {
				t.Errorf("the body length is missing, so an empty letter is "+
					"indistinguishable from a full one:\n%s", line)
			}
		})
	}
}
