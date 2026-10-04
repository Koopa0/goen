package products

import (
	"bytes"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/web"
)

// losslessWebP is a 64x48 lossless WebP: one VP8L frame whose five Huffman
// codes have one symbol each.
var losslessWebP = []byte{
	'R', 'I', 'F', 'F', 0x14, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P',
	'V', 'P', '8', 'L', 0x08, 0x00, 0x00, 0x00,
	0x2f, 0x3f, 0xc0, 0x0b, 0x00, 0x88, 0x88, 0x08,
}

// TestALosslessWebPUploadIsRefusedWithItsOwnNotice posts a lossless WebP to
// both back-office forms that take an image. The media store sits over a pool
// that can never connect, so an upload that reached storage would come back as
// uploadfailed: the losslesswebp redirect is Normalise refusing the file
// before anything is written. The page it lands on tells staff, in either
// language, what to upload instead.
func TestALosslessWebPUploadIsRefusedWithItsOwnNotice(t *testing.T) {
	t.Parallel()

	idle, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("open an unused pool: %v", err)
	}
	t.Cleanup(idle.Close)
	log := slog.New(slog.DiscardHandler)
	images := media.NewHandler(media.NewStore(idle), log)
	h := &Handler{images: images, log: log}
	home := content.NewHandler(&content.Store{}, images, &newsletter.Store{}, log)

	for _, tt := range []struct {
		name  string
		path  string
		serve func(w http.ResponseWriter, r *http.Request)
		// fields completes the form, so nothing but the image can refuse it.
		fields map[string]string
		want   string
	}{
		{
			name: "a product image", path: "/admin/products/aurora-slate/images",
			serve: h.UploadImage, fields: map[string]string{"alt": "正面"},
			want: "/admin/products/aurora-slate?losslesswebp=1",
		},
		{
			name: "a hero slide", path: "/admin/home",
			serve: home.CreateHero, fields: map[string]string{
				"headline": "秋季新品", "primary_label": "去看看", "primary_href": "/deals",
				"alt": "秋季新品主視覺",
			},
			want: "/admin/home?losslesswebp=1",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			part, err := form.CreateFormFile("image", "photo.webp")
			if err != nil {
				t.Fatalf("create the file part: %v", err)
			}
			if _, err := part.Write(losslessWebP); err != nil {
				t.Fatalf("write the file part: %v", err)
			}
			for name, value := range tt.fields {
				if err := form.WriteField(name, value); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			if err := form.Close(); err != nil {
				t.Fatalf("close the form: %v", err)
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tt.path, &body)
			req.Header.Set("Content-Type", form.FormDataContentType())
			req.SetPathValue("slug", "aurora-slate")
			res := httptest.NewRecorder()
			tt.serve(res, req)

			if got := res.Header().Get("Location"); res.Code != http.StatusSeeOther || got != tt.want {
				t.Fatalf("answered %d to %q, want 303 to %q", res.Code, got, tt.want)
			}
			for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
				ctx := i18n.WithLocale(t.Context(), locale)
				landing := httptest.NewRequestWithContext(ctx, http.MethodGet, tt.want, http.NoBody)
				if got, want := web.Notice(landing, notices), i18n.T(ctx, i18n.KeyAdminNoticeLosslessWebP); got != want {
					t.Errorf("%s: the page shows %q, want %q", locale, got, want)
				}
			}
		})
	}
}
