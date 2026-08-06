package admin

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// MaxBanners bounds the back office's list, for the reason MaxSlides does: one
// promotion shows at a time and a shop with more than this queued has lost track.
const MaxBanners = 20

// MaxBannerRunes bounds the strip's copy.
//
// It is ONE row above the header at every width — check-layout measures exactly
// that — so a message this long is already pushing it, and anything longer wraps and
// levies a second line's height on every page.
const MaxBannerRunes = 60

// BannerForm is what the back office submits.
type BannerForm struct {
	Message string
	// Short is the narrow-screen wording. Different copy, not a truncation: 60
	// characters that fit on a laptop do not fit on a phone, and cutting a
	// sentence in the middle is worse than writing a shorter one.
	Short    string
	Code     string
	CTALabel string
	CTAHref  string
	// The English strip, all optional and all falling back.
	MessageEn  string
	ShortEn    string
	CTALabelEn string
	Days       int32
}

// Validate refuses what the schema would, and the two things it cannot see.
func (f *BannerForm) Validate() map[string]string {
	f.Message = strings.TrimSpace(f.Message)
	f.Short = strings.TrimSpace(f.Short)
	f.Code = strings.TrimSpace(f.Code)
	f.CTALabel = strings.TrimSpace(f.CTALabel)
	f.CTAHref = strings.TrimSpace(f.CTAHref)
	f.MessageEn = strings.TrimSpace(f.MessageEn)
	f.ShortEn = strings.TrimSpace(f.ShortEn)
	f.CTALabelEn = strings.TrimSpace(f.CTALabelEn)

	errs := map[string]string{}
	if f.Message == "" || utf8.RuneCountInString(f.Message) > MaxBannerRunes {
		errs["message"] = "請填寫訊息,不超過 60 個字。"
	}
	for field, value := range map[string]string{
		"short":      f.Short,
		"message_en": f.MessageEn,
		"short_en":   f.ShortEn,
	} {
		if utf8.RuneCountInString(value) > MaxBannerRunes {
			errs[field] = "不超過 60 個字。"
		}
	}

	// Both or neither. promo_banners_cta_complete says the same in the schema, and
	// this is what turns it into a sentence rather than a constraint name.
	if (f.CTALabel == "") != (f.CTAHref == "") {
		errs["cta"] = "按鈕文字和連結要一起填,或都留空。"
	}
	// The href is typed by a person and rendered into the largest link at the top of
	// every page. web.SitePath is the one owner of that rule: an absolute URL would
	// send every visitor off-site from the top of the site, and `javascript:` would
	// put script in it.
	if f.CTAHref != "" {
		if _, ok := web.SitePath(f.CTAHref); !ok {
			errs["cta"] = "連結必須是本站路徑,例如 /deals。"
		}
	}
	if f.Days < 0 || f.Days > MaxHeroDays {
		errs["days"] = "檔期天數必須介於 0(不限)到 365 天。"
	}
	return errs
}

// Banners reads the list.
func (s *Store) Banners(ctx context.Context) ([]pages.AdminBanner, error) {
	rows, err := s.q.ManagedBanners(ctx, MaxBanners)
	if err != nil {
		return nil, fmt.Errorf("read promo banners: %w", err)
	}
	out := make([]pages.AdminBanner, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.AdminBanner{
			ID: r.ID.String(), Message: r.Message, Short: r.MessageShort,
			Code: r.Code, CTALabel: r.CtaLabel, CTAHref: r.CtaHref,
			MessageEn: r.MessageEn, ShortEn: r.MessageShortEn,
			CTALabelEn: r.CtaLabelEn, Active: r.IsActive,
			EndsAt: nullableDate(r.EndsAt),
		})
	}
	return out, nil
}

// CreateBanner adds a promotion.
//
// Active immediately, unlike a hero slide. The two differ on purpose: a hero
// REPLACES what is on the home page and that is an editorial decision, while a strip
// is an announcement — a shop writing one has already decided to make it.
func (s *Store) CreateBanner(ctx context.Context, f *BannerForm) (map[string]string, error) {
	if errs := f.Validate(); len(errs) > 0 {
		return errs, nil
	}
	if err := s.audited(ctx, Event{
		Action: ActionCreateBanner, Table: "promo_banners",
		After: map[string]any{"message": f.Message, "cta": f.CTAHref},
	}, func(ctx context.Context, q *db.Queries) error {
		return q.CreateBanner(ctx, db.CreateBannerParams{
			Message: f.Message, MessageShort: f.Short, Code: f.Code,
			CtaLabel: f.CTALabel, CtaHref: f.CTAHref,
			MessageEn: f.MessageEn, MessageShortEn: f.ShortEn,
			CtaLabelEn: f.CTALabelEn, Days: f.Days,
		})
	}); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// SetBannerActive switches a promotion on or off.
//
// Off rather than deleted: a promotion that ran is part of what the storefront said,
// and the dismissal cookie is keyed on the id — deleting the row and creating another
// with the same copy would reappear for everybody who had closed it, which is right
// only if it is genuinely a new promotion.
func (s *Store) SetBannerActive(ctx context.Context, id string, active bool) error {
	bannerID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: ActionToggleBanner, Table: "promo_banners", ID: nullableID(bannerID),
		After: map[string]any{"active": active},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.SetBannerActive(ctx, db.SetBannerActiveParams{
			BannerID: bannerID, IsActive: active,
		})
		if execErr != nil {
			return fmt.Errorf("%w: %s", ErrRefused, execErr.Error())
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
