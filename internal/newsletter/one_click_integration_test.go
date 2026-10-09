//go:build integration

package newsletter_test

import (
	"bytes"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/ratelimit"
)

func TestOneClickUnsubscribeUsesTheURLTokenAndIsIdempotent(t *testing.T) {
	t.Parallel()
	for _, multipartBody := range []bool{false, true} {
		t.Run(map[bool]string{false: "form encoded", true: "multipart"}[multipartBody], func(t *testing.T) {
			t.Parallel()
			s, email := store(t), addr(t)
			if _, err := s.Request(t.Context(), email); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Confirm(t.Context(), tokenFor(t, "newsletter.confirm", email, "token")); err != nil {
				t.Fatal(err)
			}
			token := tokenFor(t, "newsletter.welcome", email, "unsubscribe_token")
			h := newsletter.NewHandler(s, ratelimit.New(ratelimit.Config{
				Every: time.Second, Burst: 10, TTL: time.Hour, MaxKeys: 10,
			}), slog.New(slog.DiscardHandler))
			link := "/newsletter/unsubscribe?token=" + url.QueryEscape(token)
			get := httptest.NewRecorder()
			h.UnsubscribePage(get, httptest.NewRequestWithContext(t.Context(), http.MethodGet, link, http.NoBody))
			if active, _ := subscribed(t, email); !active {
				t.Fatal("a GET unsubscribed the reader")
			}
			var first time.Time
			for range 2 {
				var body bytes.Buffer
				contentType := "application/x-www-form-urlencoded"
				if multipartBody {
					writer := multipart.NewWriter(&body)
					if err := writer.WriteField("List-Unsubscribe", "One-Click"); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					contentType = writer.FormDataContentType()
				} else {
					body.WriteString("List-Unsubscribe=One-Click")
				}
				r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, link, &body)
				r.Header.Set("Content-Type", contentType)
				w := httptest.NewRecorder()
				h.Unsubscribe(w, r)
				if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Location") != "" {
					t.Fatalf("one-click response = %d body=%s location=%q, want empty 200", w.Code, w.Body.String(), w.Header().Get("Location"))
				}
				var at time.Time
				if err := pool.QueryRow(t.Context(), `SELECT unsubscribed_at FROM newsletter_subscribers WHERE email = $1`, email).Scan(&at); err != nil {
					t.Fatal(err)
				}
				if !first.IsZero() && !at.Equal(first) {
					t.Error("a second one-click changed the first suppression time")
				}
				first = at
			}
		})
	}
}

func TestAnURLTokenNeedsTheOneClickMarker(t *testing.T) {
	t.Parallel()
	s, email := store(t), addr(t)
	if _, err := s.Request(t.Context(), email); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Confirm(t.Context(), tokenFor(t, "newsletter.confirm", email, "token")); err != nil {
		t.Fatal(err)
	}
	token := tokenFor(t, "newsletter.welcome", email, "unsubscribe_token")
	h := newsletter.NewHandler(s, ratelimit.New(ratelimit.Config{
		Every: time.Second, Burst: 10, TTL: time.Hour, MaxKeys: 10,
	}), slog.New(slog.DiscardHandler))
	for _, body := range []string{"", "List-Unsubscribe=Other", "List-Unsubscribe=One-Click&List-Unsubscribe=Other", "List-Unsubscribe=One-Click&token=wrong"} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/newsletter/unsubscribe?token="+url.QueryEscape(token), strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.Unsubscribe(w, r)
		if w.Code < 400 {
			t.Errorf("body %q: status=%d, want refusal", body, w.Code)
		}
		if active, _ := subscribed(t, email); !active {
			t.Fatalf("body %q unsubscribed without a unique one-click marker", body)
		}
	}
	for _, query := range []string{"", "token=unknown", "token=" + url.QueryEscape(token) + "&token=unknown"} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/newsletter/unsubscribe?"+query, strings.NewReader("List-Unsubscribe=One-Click"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.Unsubscribe(w, r)
		if w.Code < 400 {
			t.Errorf("query %q: status=%d, want refusal", query, w.Code)
		}
		if active, _ := subscribed(t, email); !active {
			t.Fatalf("query %q unsubscribed without a unique known URL token", query)
		}
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/newsletter/unsubscribe?token=wrong", strings.NewReader(url.Values{"token": {token}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.Unsubscribe(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/newsletter/unsubscribe?done=1" {
		t.Errorf("ordinary body-token form response = %d %q", w.Code, w.Header().Get("Location"))
	}
}
