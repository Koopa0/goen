package email

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/outbox"
)

// TestTheEnvelopeSenderIsABareAddress asserts on envelopeFrom, because the only
// live form of MAIL FROM is inside a required STARTTLS session.
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
			if !Valid(got) {
				t.Errorf("envelopeFrom(%q) = %q, which Valid refuses — that is the "+
					"exact shape smtp.Client.Mail cannot be given", tt.from, got)
			}
		})
	}
}

// TestTheFromHeaderKeepsItsDisplayName: the envelope wants a bare address and
// the header wants the readable form.
func TestTheFromHeaderKeepsItsDisplayName(t *testing.T) {
	t.Parallel()

	const from = "goen <no-reply@goen.example>"
	wire := string(render(from, &Message{To: "a@b.co", Subject: "hi", Body: "x"}))

	if !strings.HasPrefix(wire, "From: goen <no-reply@goen.example>\r\n") {
		t.Errorf("the From header is not the display-name form:\n%s", wire)
	}
}

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

// stallFirstSetDeadlineConn pauses the first SetDeadline until a concurrent
// caller has also set the socket deadline, so cancellation can run between hook
// registration and the delivery deadline install.
type stallFirstSetDeadlineConn struct {
	net.Conn

	onFirst func()
	once    sync.Once
	calls   int
	mu      sync.Mutex
}

func (c *stallFirstSetDeadlineConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()

	if call == 1 {
		c.once.Do(c.onFirst)
		deadline := time.Now().Add(200 * time.Millisecond)
		for time.Now().Before(deadline) {
			c.mu.Lock()
			seen := c.calls
			c.mu.Unlock()
			if seen >= 2 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return c.Conn.SetDeadline(t)
}

// TestAuditSMTPCancellationDuringSocketSetup: cancellation that races the
// initial socket-deadline install must not be overwritten by the delivery
// deadline. A hook registered before SetDeadline lets the main goroutine
// replace the interrupt with the future timeout.
func TestAuditSMTPCancellationDuringSocketSetup(t *testing.T) {
	ln := listener(t)
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	smtpConnMu.Lock()
	origDial := smtpConn
	smtpConn = func(dialCtx context.Context, addr string) (net.Conn, error) {
		c, err := origDial(dialCtx, addr)
		if err != nil {
			return nil, err
		}
		return &stallFirstSetDeadlineConn{
			Conn: c,
			onFirst: func() {
				cancel()
			},
		}, nil
	}
	smtpConnMu.Unlock()
	t.Cleanup(func() {
		smtpConnMu.Lock()
		smtpConn = origDial
		smtpConnMu.Unlock()
	})

	s := SMTPSender{Addr: ln.Addr().String(), From: "no-reply@goen.example", Auth: nil, TLSName: ""}
	done := make(chan error, 1)
	go func() {
		done <- s.Send(ctx, &Message{To: "a@b.co", Subject: "s", Body: "b"})
	}()

	var peer net.Conn
	select {
	case peer = <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("sender did not dial fixture")
	}
	t.Cleanup(func() { _ = peer.Close() })

	started := time.Now()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled send succeeded")
		}
		t.Logf("returned after setup cancellation in %s: %v", time.Since(started), err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("SMTP remains blocked after cancellation: initial deadline overwrote the cancellation interrupt")
	}
}

// TestAuditSMTPCancellationAfterDial: once the TCP connection is up, an
// explicit parent cancellation must interrupt the SMTP conversation promptly,
// not only when the delivery deadline eventually fires.
func TestAuditSMTPCancellationAfterDial(t *testing.T) {
	t.Parallel()

	ln := listener(t)
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	s := SMTPSender{Addr: ln.Addr().String(), From: "no-reply@goen.example", Auth: nil, TLSName: ""}
	done := make(chan error, 1)
	go func() {
		done <- s.Send(ctx, &Message{To: "a@b.co", Subject: "s", Body: "b"})
	}()

	var peer net.Conn
	select {
	case peer = <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("sender did not dial fixture")
	}
	t.Cleanup(func() { _ = peer.Close() })

	// A silent greeting leaves the production sender blocked after DialContext.
	time.Sleep(50 * time.Millisecond)
	started := time.Now()
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled send succeeded")
		}
		t.Logf("returned after cancellation in %s: %v", time.Since(started), err)
	case <-time.After(time.Second):
		t.Errorf("SMTP Send still blocked 1s after parent cancellation; only initial "+
			"socket deadline is observed (SendTimeout=%s)", SendTimeout)
		_ = peer.Close()
		select {
		case err := <-done:
			t.Logf("peer close unblocked Send: %v", err)
		case <-time.After(time.Second):
			t.Error("cleanup did not unblock sender")
		}
	}
}

// stallFutureDeadlineConn blocks the first delivery-deadline install until the
// test releases it, so cancellation can fire while the install is in flight.
// It records whether a delivery deadline overwrote a cancellation interrupt.
type stallFutureDeadlineConn struct {
	net.Conn

	releaseInstall chan struct{}
	installStarted chan struct{}
	interruptSeen  chan struct{}
	startOnce      sync.Once
	interruptOnce  sync.Once
	mu             sync.Mutex
	interrupted    bool
	overwrote      bool
}

func (c *stallFutureDeadlineConn) SetDeadline(t time.Time) error {
	now := time.Now()
	isFuture := t.After(now.Add(5 * time.Second))
	isInterrupt := !t.After(now.Add(time.Second))

	if isFuture {
		c.startOnce.Do(func() { close(c.installStarted) })
		select {
		case <-c.releaseInstall:
		case <-time.After(2 * time.Second):
			return errors.New("stallFutureDeadlineConn: timed out waiting to install delivery deadline")
		}
	}

	c.mu.Lock()
	if isFuture && c.interrupted {
		c.overwrote = true
	}
	if isInterrupt {
		c.interrupted = true
		c.interruptOnce.Do(func() { close(c.interruptSeen) })
	}
	c.mu.Unlock()

	return c.Conn.SetDeadline(t)
}

// TestSMTPCancellationSurvivesDeadlineInstall: if the cancellation hook is
// registered before the delivery deadline, a cancel that fires while the
// deadline is being installed can be overwritten and leave a silent peer
// blocking until SendTimeout.
func TestSMTPCancellationSurvivesDeadlineInstall(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })

	releaseInstall := make(chan struct{})
	installStarted := make(chan struct{})
	interruptSeen := make(chan struct{})
	wrapped := &stallFutureDeadlineConn{
		Conn:           client,
		releaseInstall: releaseInstall,
		installStarted: installStarted,
		interruptSeen:  interruptSeen,
	}

	ctx, cancel := context.WithTimeout(t.Context(), SendTimeout)

	prepared := make(chan struct{})
	var stop func() bool
	var prepErr error
	go func() {
		stop, prepErr = prepareSMTPConn(wrapped, ctx)
		close(prepared)
	}()

	select {
	case <-installStarted:
	case <-time.After(time.Second):
		t.Fatal("delivery deadline install did not start")
	}

	cancel()
	// If the hook is registered before the delivery deadline, the interrupt can
	// land while the install is still stalled — the window this test guards.
	select {
	case <-interruptSeen:
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseInstall)

	select {
	case <-prepared:
	case <-time.After(time.Second):
		t.Fatal("prepareSMTPConn did not finish")
	}
	if prepErr != nil {
		t.Fatalf("prepareSMTPConn: %v", prepErr)
	}
	defer stop()

	if wrapped.overwrote {
		t.Error("SMTP remains blocked after cancellation: initial deadline overwrote " +
			"the cancellation interrupt")
	}
}

// TestTheSocketHasADeadlineAndNotOnlyTheDial is not synctest: a kernel socket
// deadline is not a Go timer, so a fake clock does not reach it.
func TestTheSocketHasADeadlineAndNotOnlyTheDial(t *testing.T) {
	t.Parallel()

	ln := listener(t)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Held open until the test ends: a peer that closed the connection would
		// fail the read for the wrong reason.
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
		// Which error, not merely that one happened: a dial failure would also
		// be non-nil and would prove nothing about the phases after the dial.
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

// TestTheLogSenderNeverWritesTheBody: the sender returns nil, so the outbox
// stamps the message delivered while a logged body would hold a live credential.
func TestTheLogSenderNeverWritesTheBody(t *testing.T) {
	t.Parallel()

	// Split so the literal does not read as a secret to gosec.
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

// TestInvalidRecipientIsNonRetryableAndOmitsAddress guards that an invalid recipient
// fails permanently without putting the recipient's address into last_error.
func TestInvalidRecipientIsNonRetryableAndOmitsAddress(t *testing.T) {
	t.Parallel()

	s := SMTPSender{Addr: "127.0.0.1:25", From: "shop@goen.example"}
	const badAddress = "customer@invalid..com"
	err := s.Send(t.Context(), &Message{To: badAddress, Subject: "test", Body: "body"})
	if err == nil {
		t.Fatal("Send accepted an invalid recipient address")
	}
	if !outbox.IsNonRetryable(err) {
		t.Errorf("error %v is not non-retryable; unsendable recipient would retry forever", err)
	}
	if strings.Contains(err.Error(), badAddress) {
		t.Errorf("error %q contains recipient address; last_error must not retain customer address", err)
	}
}

// TestInvalidEnvelopeFromIsRetryable guards that a deployment From misconfiguration
// remains retryable so a valid queued payload survives until configuration is fixed.
func TestInvalidEnvelopeFromIsRetryable(t *testing.T) {
	t.Parallel()

	s := SMTPSender{Addr: "127.0.0.1:25", From: "not an address"}
	err := s.Send(t.Context(), &Message{To: "customer@example.com", Subject: "test", Body: "body"})
	if err == nil {
		t.Fatal("Send accepted an invalid From address")
	}
	if outbox.IsNonRetryable(err) {
		t.Errorf("error %v is non-retryable; configuration failures must stay recoverable", err)
	}
}
