//go:build integration

package admin_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/media"
)

// TestARefusedHeroSlideStoresNoImage holds the order of the hero form: its copy
// is checked before its image is decoded, so a slide refused for a missing
// headline or missing alt text writes nothing to media_objects. The last post
// is the control: the same image under complete copy is stored, so the
// absences before it are about the refusal and not about the probe.
func TestARefusedHeroSlideStoresNoImage(t *testing.T) {
	ctx, _ := staffContext(t)
	h := adminHandlerOver(pool, admin.NewStore(pool, fakeRefunder{}, nil, nil))
	headline := "主視覺" + uuid.NewString()[:8]
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(), `DELETE FROM hero_slides WHERE headline = $1`, headline)
	})

	// Pixels no other test uploads, so the digest names this upload alone.
	picture := image.NewRGBA(image.Rect(0, 0, 5, 4))
	marker := uuid.New()
	for i := range picture.Pix {
		picture.Pix[i] = marker[i%len(marker)] | 0x01
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatalf("encode the picture: %v", err)
	}
	obj, _, err := media.Normalise(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("normalise the picture: %v", err)
	}
	stored := func() bool {
		t.Helper()
		var found bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM media_objects WHERE digest = $1)`, obj.Digest,
		).Scan(&found); err != nil {
			t.Fatalf("look for the image: %v", err)
		}
		return found
	}

	post := func(fields map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, err := form.CreateFormFile("image", "hero.png")
		if err != nil {
			t.Fatalf("create the file part: %v", err)
		}
		if _, err := part.Write(encoded.Bytes()); err != nil {
			t.Fatalf("write the file part: %v", err)
		}
		for name, value := range fields {
			if err := form.WriteField(name, value); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		if err := form.Close(); err != nil {
			t.Fatalf("close the form: %v", err)
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/home", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		res := httptest.NewRecorder()
		h.CreateHeroSlide(res, req)
		return res
	}

	for _, tt := range []struct {
		name   string
		fields map[string]string
	}{
		{name: "no headline", fields: map[string]string{
			"primary_label": "去看看", "primary_href": "/deals", "alt": "秋季新品主視覺",
		}},
		{name: "no alt text for the image", fields: map[string]string{
			"headline": headline, "primary_label": "去看看", "primary_href": "/deals",
		}},
	} {
		res := post(tt.fields)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: answered %d, want 422", tt.name, res.Code)
		}
		if stored() {
			t.Fatalf("%s: the refused slide's image was stored; it is an orphan only the "+
				"sweeper reclaims, and a form refused again and again writes one each time", tt.name)
		}
	}

	res := post(map[string]string{
		"headline": headline, "primary_label": "去看看", "primary_href": "/deals",
		"alt": "秋季新品主視覺",
	})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/home?ok=1" {
		t.Fatalf("a complete slide answered %d to %q, want 303 to ?ok=1",
			res.Code, res.Header().Get("Location"))
	}
	if !stored() {
		t.Fatal("a complete slide's image was not stored; the refusals above prove nothing")
	}
}
