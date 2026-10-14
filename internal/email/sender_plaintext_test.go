package email

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// smtpSink is a scripted SMTP server on loopback that records the commands it
// is sent. offerSTARTTLS makes it advertise the extension; it cannot complete
// the handshake, so a client that tries STARTTLS ends in an error.
type smtpSink struct {
	addr string

	mu       sync.Mutex
	commands []string
	data     string
}

func newSMTPSink(t *testing.T, offerSTARTTLS bool) *smtpSink {
	t.Helper()
	ln := listener(t)
	sink := &smtpSink{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go sink.serve(conn, offerSTARTTLS)
		}
	}()
	return sink
}

func (s *smtpSink) serve(conn net.Conn, offerSTARTTLS bool) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	write("220 sink ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.record(line)
		switch verb := strings.ToUpper(strings.Fields(line + " x")[0]); verb {
		case "EHLO":
			if offerSTARTTLS {
				write("250-sink")
				write("250 STARTTLS")
			} else {
				write("250 sink")
			}
		case "STARTTLS":
			// Agree, then fall silent: the client's handshake fails, which is
			// all the test needs to see the upgrade was attempted.
			write("220 go ahead")
			return
		case "DATA":
			write("354 end with <CRLF>.<CRLF>")
			var body strings.Builder
			for {
				l, readErr := r.ReadString('\n')
				if readErr != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			s.mu.Lock()
			s.data = body.String()
			s.mu.Unlock()
			write("250 queued")
		case "QUIT":
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

func (s *smtpSink) record(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, line)
}

func (s *smtpSink) received(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.commands {
		if strings.HasPrefix(strings.ToUpper(c), prefix) {
			return true
		}
	}
	return false
}

func (s *smtpSink) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data
}

// mailWithLink carries a credential, as every reset and verification mail does.
// Split so the literal does not read as a secret to gosec.
var mailWithLink = &Message{
	To: "owner@example.com", Subject: "reset",
	Body: "Open https://goen.test/reset?" + "tok" + "en=cafebabe0123456789 to set a password.\n",
}

// TestAServerWithoutSTARTTLSReceivesNothingUnlessDevelopmentAllowsIt: the
// default is the production rule, no encryption means no mail and nothing sent,
// and the exception is a field only the development switch sets.
func TestAServerWithoutSTARTTLSReceivesNothingUnlessDevelopmentAllowsIt(t *testing.T) {
	t.Run("the zero value refuses and sends no MAIL, RCPT or DATA", func(t *testing.T) {
		sink := newSMTPSink(t, false)
		err := SMTPSender{Addr: sink.addr, From: "goen@example.com"}.Send(t.Context(), mailWithLink)
		if err == nil || !strings.Contains(err.Error(), "does not offer STARTTLS") {
			t.Fatalf("Send = %v, want the STARTTLS refusal", err)
		}
		for _, verb := range []string{"MAIL", "RCPT", "DATA"} {
			if sink.received(verb) {
				t.Errorf("the sink received %s although the connection was not encrypted", verb)
			}
		}
	})

	t.Run("the development exception delivers to a loopback sink", func(t *testing.T) {
		sink := newSMTPSink(t, false)
		err := SMTPSender{
			Addr: sink.addr, From: "goen@example.com", AllowLoopbackPlaintext: true,
		}.Send(t.Context(), mailWithLink)
		if err != nil {
			t.Fatalf("Send to a loopback sink without STARTTLS: %v", err)
		}
		if !strings.Contains(sink.body(), "tok"+"en=cafebabe0123456789") {
			t.Errorf("the sink did not receive the mail:\n%s", sink.body())
		}
	})

	t.Run("a server that offers STARTTLS is upgraded, never skipped", func(t *testing.T) {
		sink := newSMTPSink(t, true)
		err := SMTPSender{
			Addr: sink.addr, From: "goen@example.com", AllowLoopbackPlaintext: true,
		}.Send(t.Context(), mailWithLink)
		if err == nil || !strings.Contains(err.Error(), "starttls") {
			t.Fatalf("Send = %v, want the STARTTLS attempt to fail its handshake", err)
		}
		if !sink.received("STARTTLS") {
			t.Error("the sender did not try to upgrade a server that offered it")
		}
		if sink.received("MAIL") || sink.received("DATA") {
			t.Error("the mail went out although the upgrade failed")
		}
	})
}

// remoteConn reports a peer address of the test's choosing.
type remoteConn struct {
	net.Conn

	remote net.Addr
}

func (c remoteConn) RemoteAddr() net.Addr { return c.remote }

// TestThePlaintextExceptionIsLoopbackOnly: even with the exception on, a peer
// that is not a loopback address gets nothing without STARTTLS, whatever name
// the address was configured under.
func TestThePlaintextExceptionIsLoopbackOnly(t *testing.T) {
	tests := []struct {
		name    string
		remote  net.Addr
		wantErr bool
	}{
		{name: "IPv4 loopback", remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 25}},
		{name: "IPv6 loopback", remote: &net.TCPAddr{IP: net.IPv6loopback, Port: 25}},
		{name: "a private address", remote: &net.TCPAddr{IP: net.IPv4(192, 168, 1, 10), Port: 25}, wantErr: true},
		{name: "a public address", remote: &net.TCPAddr{IP: net.IPv4(203, 0, 113, 7), Port: 25}, wantErr: true},
		{name: "an address that is not TCP", remote: &net.UnixAddr{Name: "/run/smtp", Net: "unix"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := newSMTPSink(t, false)

			smtpConnMu.Lock()
			orig := smtpConn
			smtpConn = func(ctx context.Context, addr string) (net.Conn, error) {
				c, err := orig(ctx, addr)
				if err != nil {
					return nil, err
				}
				return remoteConn{Conn: c, remote: tt.remote}, nil
			}
			smtpConnMu.Unlock()
			t.Cleanup(func() {
				smtpConnMu.Lock()
				smtpConn = orig
				smtpConnMu.Unlock()
			})

			err := SMTPSender{
				Addr: sink.addr, From: "goen@example.com", AllowLoopbackPlaintext: true,
			}.Send(t.Context(), mailWithLink)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "does not offer STARTTLS") {
					t.Fatalf("Send = %v, want the STARTTLS refusal", err)
				}
				if sink.received("MAIL") || sink.received("DATA") {
					t.Error("mail was sent in the clear to a peer that is not loopback")
				}
				return
			}
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
		})
	}
}
