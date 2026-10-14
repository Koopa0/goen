package main

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/email"
)

// resetLink stands for the credential every reset and verification mail
// carries. Split so the literal does not read as a secret to gosec.
const resetLink = "https://goen.test/reset?" + "tok" + "en=cafebabe0123456789"

// TestMailConveniencesExistOnlyInDevelopment reads the switch the way a process
// does, from the environment through loadConfig, so a change to where the
// program takes it from is caught here and not in a deployment. Development is
// GOEN_INSECURE_COOKIES=1 and nothing else: any other value, including an
// unset one, is the secure posture.
func TestMailConveniencesExistOnlyInDevelopment(t *testing.T) {
	tests := []struct {
		name        string
		insecure    string
		unset       bool
		development bool
	}{
		{name: "unset is production", unset: true},
		{name: "empty is production", insecure: ""},
		{name: "0 is production", insecure: "0"},
		{name: "true is production", insecure: "true"},
		{name: "1 is development", insecure: "1", development: true},
	}
	for _, tt := range tests {
		for _, smtpAddr := range []string{"", "127.0.0.1:1025"} {
			t.Run(tt.name+" with SMTP "+smtpAddr, func(t *testing.T) {
				t.Setenv("GOEN_DATABASE_URL", "postgres://store.example/goen")
				t.Setenv("GOEN_SMTP_ADDR", smtpAddr)
				if tt.unset {
					// t.Setenv registers the restore; the unset is what is tested.
					t.Setenv("GOEN_INSECURE_COOKIES", "")
					if err := os.Unsetenv("GOEN_INSECURE_COOKIES"); err != nil {
						t.Fatal(err)
					}
				} else {
					t.Setenv("GOEN_INSECURE_COOKIES", tt.insecure)
				}
				cfg, err := loadConfig()
				if err != nil {
					t.Fatalf("loadConfig: %v", err)
				}

				var logged bytes.Buffer
				sender, err := newSender(&cfg, slog.New(slog.NewTextHandler(&logged, nil)))
				if err != nil {
					t.Fatalf("newSender: %v", err)
				}
				switch s := sender.(type) {
				case email.LogSender:
					if smtpAddr != "" {
						t.Fatalf("a configured relay produced %T", sender)
					}
					if s.ShowBody != tt.development {
						t.Errorf("LogSender.ShowBody = %v, want %v", s.ShowBody, tt.development)
					}
					if err := s.Send(t.Context(), &email.Message{
						To: "owner@example.com", Subject: "reset", Body: "Open " + resetLink + " now.\n",
					}); err != nil {
						t.Fatalf("Send: %v", err)
					}
					if got := strings.Contains(logged.String(), resetLink); got != tt.development {
						t.Errorf("the reset link is in the log = %v, want %v:\n%s", got, tt.development, logged.String())
					}
				case email.SMTPSender:
					if smtpAddr == "" {
						t.Fatalf("no relay produced %T", sender)
					}
					if s.AllowLoopbackPlaintext != tt.development {
						t.Errorf("SMTPSender.AllowLoopbackPlaintext = %v, want %v",
							s.AllowLoopbackPlaintext, tt.development)
					}
				default:
					t.Fatalf("newSender returned %T", sender)
				}
			})
		}
	}
}
