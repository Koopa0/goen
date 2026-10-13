//go:build integration

package content_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/outbox"
)

func TestNewsletterAudienceUsesOnlyActiveSubscribers(t *testing.T) {
	owner := dbtest.Pool(t)
	ctx, staff := admintest.StaffContext(t, owner)
	admin := admintest.AdminRolePool(t, owner)
	if _, err := owner.Exec(ctx, `INSERT INTO newsletter_subscribers (email, locale, unsubscribe_token, unsubscribed_at)
		VALUES ('zh-one@example.com', 'zh-Hant', repeat('a', 32), NULL),
		       ('zh-two@example.com', 'zh-Hant', repeat('b', 32), NULL),
		       ('en-on@example.com', 'en', repeat('c', 32), NULL),
		       ('en-off@example.com', 'en', repeat('d', 32), now()),
		       ('zh-off@example.com', 'zh-Hant', repeat('e', 32), now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO newsletter_confirmations (email, digest, expires_at)
		VALUES ('waiting@example.com', decode(repeat('ab', 32), 'hex'), now() + interval '1 day')`); err != nil {
		t.Fatal(err)
	}
	letters := newsletter.NewStore(admin)
	counts, countErr := letters.Counts(ctx)
	if countErr != nil {
		t.Fatal(countErr)
	}
	if diff := cmp.Diff(newsletter.Counts{Active: 3, EnglishActive: 1, Unsubscribed: 2, Awaiting: 1}, counts); diff != "" {
		t.Fatalf("newsletter counts (-want +got):\n%s", diff)
	}
	log := slog.New(slog.DiscardHandler)
	handler := content.NewHandler(content.NewStore(admin), media.NewHandler(media.NewStore(admin), log), letters, log)
	mux := http.NewServeMux()
	handler.Routes(mux, admintest.BackOffice)
	for _, locale := range []struct {
		locale i18n.Locale
		want   string
	}{
		{locale: i18n.ZhHant, want: "會以這份內容寄給全部 3 位訂閱者（其中 1 位選擇英文），不分語言。"},
		{locale: i18n.En, want: "This goes to all 3 subscribers (1 chose English) as written."},
	} {
		t.Run(locale.locale.Tag(), func(t *testing.T) {
			localized := i18n.WithLocale(ctx, locale.locale)
			get := httptest.NewRecorder()
			mux.ServeHTTP(get, httptest.NewRequestWithContext(localized, http.MethodGet, "/admin/newsletter", nil))
			if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), locale.want) {
				t.Errorf("newsletter GET status/body=%d/%s, want audience %q", get.Code, get.Body.String(), locale.want)
			}
			form := url.Values{"subject": {""}, "body": {" Draft body kept "}}
			request := httptest.NewRequestWithContext(localized, http.MethodPost, "/admin/newsletter", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			post := httptest.NewRecorder()
			mux.ServeHTTP(post, request)
			if post.Code != http.StatusUnprocessableEntity || !strings.Contains(post.Body.String(), locale.want) || !strings.Contains(post.Body.String(), " Draft body kept ") {
				t.Errorf("newsletter refusal status/body=%d/%s, want audience and preserved draft", post.Code, post.Body.String())
			}
		})
	}
	var issues, deliveries int
	if err := owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM newsletter_issues), (SELECT count(*) FROM outbox_messages WHERE topic = 'newsletter.issue')`).Scan(&issues, &deliveries); err != nil {
		t.Fatal(err)
	}
	if issues != 0 || deliveries != 0 {
		t.Errorf("refused composition created %d issues and %d deliveries, want zero", issues, deliveries)
	}
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	issueID, err := letters.Compose(ctx, "Audience contract", "One body for all active subscribers", actor)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := letters.Send(ctx, issueID, actor)
	if err != nil {
		t.Fatal(err)
	}
	var recorded int64
	if readErr := owner.QueryRow(ctx, "SELECT recipients FROM newsletter_issues WHERE id=$1", issueID).Scan(&recorded); readErr != nil {
		t.Fatal(readErr)
	}
	rows, err := owner.Query(ctx, "SELECT payload->>'email', payload->>'locale' FROM outbox_messages WHERE topic=$1 ORDER BY payload->>'email'", outbox.TopicNewsletterIssue.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var recipients []string
	var english int64
	for rows.Next() {
		var address, locale string
		if err := rows.Scan(&address, &locale); err != nil {
			t.Fatal(err)
		}
		recipients = append(recipients, address)
		if locale == "en" {
			english++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(recipients)
	if diff := cmp.Diff([]string{"en-on@example.com", "zh-one@example.com", "zh-two@example.com"}, recipients); diff != "" {
		t.Errorf("sent recipients (-want +got):\n%s", diff)
	}
	if sent != 3 || int64(sent) != counts.Active || recorded != counts.Active || int64(len(recipients)) != counts.Active || english != counts.EnglishActive {
		t.Errorf("send/counts = %d/%d/%v/%d, want audience %d English %d", sent, recorded, recipients, english, counts.Active, counts.EnglishActive)
	}
}
