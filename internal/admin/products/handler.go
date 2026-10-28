package products

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/picture"
	"github.com/koopa0/goen/internal/shipping"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store  *Store
	images *media.Handler
	log    *slog.Logger
}

func NewHandler(store *Store, images *media.Handler, log *slog.Logger) *Handler {
	if store == nil || images == nil || log == nil {
		panic("products: NewHandler requires a store, a media handler and a logger")
	}
	return &Handler{store: store, images: images, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *admin.Control) {
	mux.HandleFunc("GET /admin/products", ac.RequireStaff(h.List))
	mux.HandleFunc("POST /admin/products", ac.RequireStaff(h.Create))
	mux.HandleFunc("GET /admin/products/new", ac.RequireStaff(h.New))
	mux.HandleFunc("GET /admin/products/{slug}", ac.RequireStaff(h.Edit))
	mux.HandleFunc("POST /admin/products/{slug}", ac.RequireStaff(h.Update))
	mux.HandleFunc("POST /admin/products/{slug}/label", ac.RequireStaff(h.ProductLabel))
	mux.HandleFunc("POST /admin/products/{slug}/invoice-line", ac.RequireStaff(h.ProductInvoiceLine))
	mux.HandleFunc("POST /admin/products/{slug}/status", ac.RequireStaff(h.Publish))
	mux.HandleFunc("POST /admin/products/{slug}/variants", ac.RequireStaff(h.AddVariant))
	mux.HandleFunc("POST /admin/products/{slug}/options", ac.RequireStaff(h.AddOption))
	mux.HandleFunc("POST /admin/products/{slug}/options/values", ac.RequireStaff(h.AddOptionValue))
	mux.HandleFunc("POST /admin/products/{slug}/specs", ac.RequireStaff(h.AddSpec))
	mux.HandleFunc("POST /admin/products/{slug}/specs/remove", ac.RequireStaff(h.RemoveSpec))
	mux.HandleFunc("POST /admin/products/{slug}/images", ac.RequireStaff(h.UploadImage))
	mux.HandleFunc("POST /admin/products/{slug}/images/reuse", ac.RequireStaff(h.ReuseImage))
	mux.HandleFunc("POST /admin/products/{slug}/images/remove", ac.RequireStaff(h.RemoveImage))
	mux.HandleFunc("POST /admin/products/{slug}/images/option", ac.RequireStaff(h.SetImageOption))
	mux.HandleFunc("POST /admin/products/{slug}/images/move", ac.RequireStaff(h.MoveImage))
}

var notices = map[string]widget.NoticeEntry{
	"created":      widget.NoticeDone(i18n.KeyAdminNoticeProductCreated),
	"saved":        widget.NoticeDone(i18n.KeyAdminNoticeProductSaved),
	"published":    widget.NoticeDone(i18n.KeyAdminNoticeProductPublished),
	"drafted":      widget.NoticeDone(i18n.KeyAdminNoticeProductDrafted),
	"archived":     widget.NoticeDone(i18n.KeyAdminNoticeProductArchived),
	"variant":      widget.NoticeDone(i18n.KeyAdminNoticeProductVariant),
	"image":        widget.NoticeDone(i18n.KeyAdminNoticeProductImage),
	"imageopt":     widget.NoticeDone(i18n.KeyAdminNoticeProductImageOption),
	"imagemoved":   widget.NoticeDone(i18n.KeyAdminNoticeProductImageMoved),
	"imageremoved": widget.NoticeDone(i18n.KeyAdminNoticeProductImageRemoved),
	"option":       widget.NoticeDone(i18n.KeyAdminNoticeProductOption),
	"optionvalue":  widget.NoticeDone(i18n.KeyAdminNoticeProductOptionValue),
	"spec":         widget.NoticeDone(i18n.KeyAdminNoticeProductSpec),
	"specremoved":  widget.NoticeDone(i18n.KeyAdminNoticeProductSpecRemoved),
	"invoice":      widget.NoticeDone(i18n.KeyAdminNoticeProductInvoice),
	"label":        widget.NoticeDone(i18n.KeyAdminNoticeProductLabel),
	"refused":      widget.NoticeRefused(i18n.KeyAdminNoticeRefused),
	"imageneeds":   widget.NoticeRefused(i18n.KeyAdminNoticeImageNeeds),
	"badoption":    widget.NoticeRefused(i18n.KeyAdminNoticeBadOption),
	"specfailed":   widget.NoticeFailed(i18n.KeyAdminNoticeSpecFailed),
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.List(r.Context(), r.URL.Query().Get(keyset.Param))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read products", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.Notice = widget.NoticeFrom(r, notices)
	respond.Component(w, r, h.log, http.StatusOK, Products(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageProducts)}, view))
}

func (h *Handler) New(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.NewForm(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "new product form", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	respond.Component(w, r, h.log, http.StatusOK, Product(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewProduct)}, view))
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseLongTextForm(w, r, 2*maxDescriptionRunes); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f, errs := productFormOf(r)
	maps.Copy(errs, f.Validate(r.Context()))
	if len(errs) > 0 {
		h.rejectProduct(w, r, f, errs, true)
		return
	}
	slug, errs, err := h.store.Create(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create product", "error", err)
		admin.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectProduct(w, r, f, errs, true)
	default:
		//nolint:gosec // G710: slug came back from the database
		http.Redirect(w, r, "/admin/products/"+slug+"?created=1"+SectionDetails.Query(), http.StatusSeeOther)
	}
}

func (h *Handler) Edit(w http.ResponseWriter, r *http.Request) {
	h.renderProduct(w, r, http.StatusOK, widget.NoticeFrom(r, notices), sectionOf(r.URL.Query().Get("in")))
}

// product reads the editor's product; a failure to read its sales or reviews is
// logged, and the view says what is missing.
func (h *Handler) product(ctx context.Context, slug string) (ProductView, error) {
	view, err := h.store.Product(ctx, slug)
	if errors.Is(err, ErrStanding) {
		h.log.ErrorContext(ctx, "read product sales and reviews", "error", err)
		err = nil
	}
	return view, err
}

func (h *Handler) productView(ctx context.Context, slug string) (ProductView, error) {
	view, err := h.product(ctx, slug)
	if err != nil {
		return view, err
	}
	if images, imgErr := h.store.Images(ctx, slug); imgErr != nil {
		// Not fatal: losing the image strip is smaller than losing the page.
		h.log.ErrorContext(ctx, "read product images", "error", imgErr)
	} else {
		view.Images = images
	}
	if recent, recentErr := h.images.Recent(ctx); recentErr != nil {
		h.log.ErrorContext(ctx, "read recent uploads", "error", recentErr)
	} else {
		for _, obj := range recent {
			view.Library = append(view.Library, Image{
				Photo: picture.Image{Digest: obj.Digest, Width: int(obj.Width)},
			})
		}
	}
	return view, nil
}

func (h *Handler) renderProduct(w http.ResponseWriter, r *http.Request, status int, notice widget.Result, noticeIn Section) {
	view, err := h.productView(r.Context(), r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			respond.Component(w, r, h.log, http.StatusNotFound, frame.MissingRecord(
				frame.Page{Title: i18n.T(r.Context(), i18n.KeyAdminMissingProduct)},
				frame.MissingRecordView{Section: "products", Heading: i18n.T(r.Context(), i18n.KeyAdminMissingProduct), Body: i18n.T(r.Context(), i18n.KeyAdminNotFoundBody), BackLabel: i18n.KeyAdminBackProducts}))
			return
		}
		h.log.ErrorContext(r.Context(), "read product", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.Notice, view.NoticeIn = notice, noticeIn

	respond.Component(w, r, h.log, status, Product(
		frame.Page{Refused: status == http.StatusUnprocessableEntity, Title: view.Name}, view))
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseLongTextForm(w, r, 2*maxDescriptionRunes); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f, errs := productFormOf(r)
	f.Slug = r.PathValue("slug") // the slug is the identity; the form cannot move it
	maps.Copy(errs, f.Validate(r.Context()))
	if len(errs) > 0 {
		h.rejectProduct(w, r, f, errs, false)
		return
	}

	errs, err := h.store.Update(r.Context(), f)
	switch {
	case errors.Is(err, ErrNotFound):
		admin.NotFound(w, r, h.log)
	case err != nil:
		h.log.ErrorContext(r.Context(), "update product", "error", err)
		admin.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectProduct(w, r, f, errs, false)
	default:
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+f.Slug+"?saved=1"+SectionDetails.Query(), http.StatusSeeOther)
	}
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	status := product.Status(r.PostFormValue("status"))
	switch err := h.store.SetStatus(r.Context(), slug, status); {
	case err == nil:
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+statusNotice(status), http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "set product status", "slug", slug, "error", err)
		admin.NotFound(w, r, h.log)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "set product status", "slug", slug, "error", err)
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set product status", "slug", slug, "error", err)
		admin.ServerError(w, r, h.log)
	}
}

// statusNotice is the query that makes the page say which status the product
// moved to.
func statusNotice(status product.Status) string {
	switch status {
	case product.StatusActive:
		return "?published=1"
	case product.StatusDraft:
		return "?drafted=1"
	case product.StatusArchived:
		return "?archived=1"
	default:
		return ""
	}
}

func (h *Handler) AddVariant(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	f, draft, errs := variantFormOf(r)
	maps.Copy(errs, f.Validate(r.Context()))
	if len(errs) > 0 {
		h.editProductWithErrors(w, r, slug, errs, &productDrafts{variant: draft})
		return
	}
	errs, err := h.store.AddVariant(r.Context(), slug, f)
	switch {
	case errors.Is(err, ErrNotFound):
		admin.NotFound(w, r, h.log)
	case err != nil:
		h.log.ErrorContext(r.Context(), "add variant", "error", err)
		admin.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, &productDrafts{variant: draft})
	default:
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?variant=1"+SectionVariants.Query(), http.StatusSeeOther)
	}
}

func variantFormOf(r *http.Request) (*VariantForm, VariantDraft, map[string]string) {
	draft := VariantDraft{
		SKU: r.PostFormValue("sku"), Price: r.PostFormValue("price"),
		Compare: r.PostFormValue("compare"), Safety: r.PostFormValue("safety"),
		ParcelLongest:  r.PostFormValue("parcel_longest"),
		ParcelSum:      r.PostFormValue("parcel_sum"),
		ParcelWeight:   r.PostFormValue("parcel_weight"),
		OptionValueIDs: r.PostForm["option_value"],
	}
	errs := map[string]string{}
	safety := parseVariantCount(draft.Safety, safetyStockCeiling,
		"safety", i18n.T(r.Context(), i18n.KeyFormSafetyStock), errs)
	longest := parseVariantCount(draft.ParcelLongest, shipping.MaxParcelLongestMM,
		"parcel_longest", fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormParcelMeasurement), shipping.MaxParcelLongestMM), errs)
	sum := parseVariantCount(draft.ParcelSum, shipping.MaxParcelSumMM,
		"parcel_sum", fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormParcelMeasurement), shipping.MaxParcelSumMM), errs)
	weight := parseVariantCount(draft.ParcelWeight, shipping.MaxParcelWeightG,
		"parcel_weight", fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormParcelMeasurement), shipping.MaxParcelWeightG), errs)
	// ParsePrice, not a parser returning the figure alone: blank is a legitimate
	// compare-at price and it stores zero, so a collapsed unreadable figure is
	// indistinguishable from "no discount". /admin/stock prices through this too.
	price, priceOK := money.ParseDollars(r.PostFormValue("price"))
	compare, compareOK := money.ParseDollars(r.PostFormValue("compare"))
	if !priceOK {
		errs["price"] = i18n.T(r.Context(), i18n.KeyFormPricePositive)
	}
	if !compareOK {
		errs["compare"] = fmt.Sprintf(
			i18n.T(r.Context(), i18n.KeyFormCompareAmount), money.MaxCents/100)
	}
	return &VariantForm{
		SKU:          r.PostFormValue("sku"),
		PriceCents:   price,
		CompareCents: compare,
		SafetyStock:  safety,
		// Zero is UNMEASURED and stores NULL, so a blank field leaves the variant
		// refused by no shipping method rather than blocked from all of them.
		ParcelLongestMM: longest,
		ParcelSumMM:     sum,
		ParcelWeightG:   weight,
		// One select per option, all named option_value; PostForm holds them in
		// the order the page rendered the axes.
		OptionValues: r.PostForm["option_value"],
	}, draft, errs
}

func parseVariantCount(raw string, ceiling int32, field, message string, errs map[string]string) int32 {
	value, ok := formtext.ParseBounded(raw, ceiling)
	if !ok {
		errs[field] = message
	}
	return value
}

func productFormOf(r *http.Request) (form *Form, errs map[string]string) {
	raw := r.PostFormValue("warranty_months")
	warranty, ok := formtext.ParseBounded(raw, MaxWarrantyMonths)
	errs = map[string]string{}
	if !ok {
		errs["warranty_months"] = i18n.T(r.Context(), i18n.KeyFormWarrantyMonths)
	}
	return &Form{
		Slug:              r.PostFormValue("slug"),
		Name:              r.PostFormValue("name"),
		Summary:           r.PostFormValue("summary"),
		Description:       r.PostFormValue("description"),
		NameEn:            r.PostFormValue("name_en"),
		SummaryEn:         r.PostFormValue("summary_en"),
		DescriptionEn:     r.PostFormValue("description_en"),
		WarrantyNote:      r.PostFormValue("warranty"),
		WarrantyMonthsRaw: raw,
		WarrantyMonths:    warranty,
		BrandID:           r.PostFormValue("brand"),
		CategoryID:        r.PostFormValue("category"),
	}, errs
}

func (h *Handler) rejectProduct(w http.ResponseWriter, r *http.Request, f *Form, errs map[string]string, isNew bool) {
	var view ProductView
	var err error
	if isNew {
		view, err = h.store.NewForm(r.Context())
	} else {
		view, err = h.productView(r.Context(), f.Slug)
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			admin.NotFound(w, r, h.log)
			return
		}
		h.log.ErrorContext(r.Context(), "rebuild product form", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.Slug, view.Name, view.Summary = f.Slug, r.PostFormValue("name"), r.PostFormValue("summary")
	if isNew {
		view.Slug = r.PostFormValue("slug")
	}
	view.Description, view.WarrantyNote = r.PostFormValue("description"), r.PostFormValue("warranty")
	view.WarrantyMonthsRaw = f.WarrantyMonthsRaw
	view.WarrantyMonths = f.WarrantyMonths
	view.NameEn, view.SummaryEn = r.PostFormValue("name_en"), r.PostFormValue("summary_en")
	view.DescriptionEn = r.PostFormValue("description_en")
	view.BrandID, view.CategoryID = r.PostFormValue("brand"), r.PostFormValue("category")
	view.Errors = errs
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Product(
		frame.Page{Refused: true, Title: view.Title(r.Context())}, view))
}

// UploadImage stores then attaches, deliberately NOT in one transaction: storing is idempotent by content, so a
// failure between them leaves only an orphan UnreferencedMedia reclaims.
func (h *Handler) UploadImage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	obj, err := h.images.StoreUpload(w, r, "image")
	if err != nil {
		h.respondToUploadError(w, r, err)
		return
	}

	alt := r.PostFormValue("alt")
	if err := h.store.AttachImage(r.Context(), slug, obj.Digest, alt,
		r.PostFormValue("alt_en"), r.PostFormValue("option_value"),
		obj.Width, obj.Height); err != nil {
		field, key := attachImageRefusal(err, alt)
		if field == "" {
			h.log.ErrorContext(r.Context(), "attach image", "error", err, "slug", slug)
			admin.ServerError(w, r, h.log)
			return
		}
		h.log.WarnContext(r.Context(), "attach image", "error", err, "slug", slug)
		h.rejectImageUpload(w, r, field, key)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?image=1"+SectionImages.Query(), http.StatusSeeOther)
}

func (h *Handler) ReuseImage(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")

	// The dimensions come from the STORED object and not the form: the srcset is
	// built from them, so a hand-edited pair lays out against the wrong size.
	obj, err := h.images.Object(r.Context(), r.PostFormValue("digest"))
	if err != nil {
		h.log.WarnContext(r.Context(), "reuse image", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?imageneeds=1"+SectionImages.Query(), http.StatusSeeOther)
		return
	}
	if err := h.store.AttachImage(r.Context(), slug, obj.Digest,
		r.PostFormValue("alt"), r.PostFormValue("alt_en"), "",
		obj.Width, obj.Height); err != nil {
		field, key := attachImageRefusal(err, r.PostFormValue("alt"))
		if field == "" {
			h.log.ErrorContext(r.Context(), "attach reused image", "error", err, "slug", slug)
			admin.ServerError(w, r, h.log)
			return
		}
		h.log.WarnContext(r.Context(), "attach reused image", "error", err, "slug", slug)
		h.rejectImageReuse(w, r, field, key)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?image=1"+SectionImages.Query(), http.StatusSeeOther)
}

func (h *Handler) rejectImageUpload(w http.ResponseWriter, r *http.Request, field string, key i18n.Key) {
	h.editProductWithErrors(w, r, r.PathValue("slug"), map[string]string{field: i18n.T(r.Context(), key)}, &productDrafts{
		upload: ImageUploadDraft{Alt: r.PostFormValue("alt"), AltEn: r.PostFormValue("alt_en"), OptionValue: r.PostFormValue("option_value")},
	})
}

func (h *Handler) rejectImageReuse(w http.ResponseWriter, r *http.Request, field string, key i18n.Key) {
	h.editProductWithErrors(w, r, r.PathValue("slug"), map[string]string{"reuse_" + field: i18n.T(r.Context(), key)}, &productDrafts{
		reuse: ImageReuseDraft{Digest: r.PostFormValue("digest"), Alt: r.PostFormValue("alt"), AltEn: r.PostFormValue("alt_en")},
	})
}

func (h *Handler) SetImageOption(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	if err := h.store.SetImageOption(r.Context(), slug,
		r.PostFormValue("digest"), r.PostFormValue("option_value")); err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			admin.NotFound(w, r, h.log)
		case errors.Is(err, ErrNotThisProductsOption):
			h.log.WarnContext(r.Context(), "set image option refused", "error", err, "slug", slug)
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, "/admin/products/"+slug+"?badoption=1"+SectionImages.Query(), http.StatusSeeOther)
		case errors.Is(err, ErrRefused):
			h.log.WarnContext(r.Context(), "set image option refused", "error", err, "slug", slug)
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, "/admin/products/"+slug+"?refused=1"+SectionImages.Query(), http.StatusSeeOther)
		default:
			h.log.ErrorContext(r.Context(), "set image option", "error", err, "slug", slug)
			admin.ServerError(w, r, h.log)
		}
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?imageopt=1"+SectionImages.Query(), http.StatusSeeOther)
}

func (h *Handler) RemoveImage(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	if err := h.store.DetachImage(r.Context(), slug, r.PostFormValue("digest")); err != nil {
		if errors.Is(err, ErrNotFound) {
			admin.NotFound(w, r, h.log)
			return
		}
		if errors.Is(err, ErrRefused) {
			h.log.WarnContext(r.Context(), "detach image refused", "error", err, "slug", slug)
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, "/admin/products/"+slug+"?refused=1"+SectionImages.Query(), http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "detach image", "error", err, "slug", slug)
		admin.ServerError(w, r, h.log)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?imageremoved=1"+SectionImages.Query(), http.StatusSeeOther)
}

func (h *Handler) MoveImage(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	err := h.store.MoveImage(r.Context(), slug, r.PostFormValue("digest"), ImageMove(r.PostFormValue("move")))
	switch {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?imagemoved=1"+SectionImages.Query(), http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		admin.NotFound(w, r, h.log)
	case errors.Is(err, ErrInvalid):
		h.log.WarnContext(r.Context(), "move image refused", "error", err, "slug", slug)
		h.renderProduct(w, r, http.StatusUnprocessableEntity,
			widget.Result{Outcome: widget.OutcomeRefused, Text: i18n.T(r.Context(), i18n.KeyAdminNoticeImageStale)}, SectionImages)
	default:
		h.log.ErrorContext(r.Context(), "move image", "error", err, "slug", slug)
		admin.ServerError(w, r, h.log)
	}
}

func (h *Handler) AddOption(w http.ResponseWriter, r *http.Request) {
	h.optionWrite(w, r, "?option=1", func(slug string) (map[string]string, error) {
		return h.store.AddOption(r.Context(), slug, OptionDraft{
			Name:   r.PostFormValue("name"),
			NameEn: r.PostFormValue("name_en"),
		})
	})
}

func (h *Handler) AddOptionValue(w http.ResponseWriter, r *http.Request) {
	h.optionWrite(w, r, "?optionvalue=1", func(slug string) (map[string]string, error) {
		return h.store.AddOptionValue(r.Context(), slug, OptionDraft{
			OptionID:  r.PostFormValue("option"),
			Name:      r.PostFormValue("value"),
			NameEn:    r.PostFormValue("value_en"),
			SwatchHex: r.PostFormValue("swatch_hex"),
		})
	})
}

// optionWrite answers an option write; done is the query success goes to, so the
// page says which of the two it was.
func (h *Handler) optionWrite(
	w http.ResponseWriter, r *http.Request, done string, write func(slug string) (map[string]string, error),
) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	draft := VariantDraft{
		OptionName: r.PostFormValue("name"), OptionNameEn: r.PostFormValue("name_en"),
		ValueOption: r.PostFormValue("option"), Value: r.PostFormValue("value"),
		ValueEn: r.PostFormValue("value_en"), Swatch: r.PostFormValue("swatch_hex"),
	}
	errs, err := write(slug)
	switch {
	case err != nil:
		// A constraint the form has a control for is a refusal and not a
		// missing product. Without this the page tells a staff member who
		// mistyped a colour that the product they are looking at is gone.
		if refused := optionRefusal(r.Context(), err); len(refused) > 0 {
			h.editProductWithErrors(w, r, slug, refused, &productDrafts{variant: draft})
			return
		}
		if errors.Is(err, ErrNotFound) {
			admin.NotFound(w, r, h.log)
			return
		}
		if errors.Is(err, ErrRefused) {
			h.log.WarnContext(r.Context(), "write product option refused", "error", err, "slug", slug)
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, "/admin/products/"+slug+"?refused=1"+SectionOptions.Query(), http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "write product option", "error", err, "slug", slug)
		admin.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, &productDrafts{variant: draft})
	default:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+done+SectionOptions.Query(), http.StatusSeeOther)
	}
}

func (h *Handler) AddSpec(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	draft := SpecDraft{
		Label:   r.PostFormValue("label"),
		Value:   r.PostFormValue("value"),
		LabelEn: r.PostFormValue("label_en"),
		ValueEn: r.PostFormValue("value_en"),
	}
	errs, err := h.store.AddSpec(r.Context(), slug, draft)
	switch {
	case errors.Is(err, ErrNotFound):
		admin.NotFound(w, r, h.log)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "add spec refused", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?specfailed=1"+SectionSpecs.Query(), http.StatusSeeOther)
	case err != nil:
		h.log.ErrorContext(r.Context(), "add spec", "error", err, "slug", slug)
		admin.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, &productDrafts{spec: draft})
	default:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?spec=1"+SectionSpecs.Query(), http.StatusSeeOther)
	}
}

func (h *Handler) RemoveSpec(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	if err := h.store.RemoveSpec(r.Context(), slug, r.PostFormValue("spec")); err != nil {
		if errors.Is(err, ErrNotFound) {
			admin.NotFound(w, r, h.log)
			return
		}
		if errors.Is(err, ErrRefused) {
			h.log.WarnContext(r.Context(), "remove spec refused", "error", err, "slug", slug)
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, "/admin/products/"+slug+"?refused=1", http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "remove spec", "error", err, "slug", slug)
		admin.ServerError(w, r, h.log)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?specremoved=1", http.StatusSeeOther)
}

type productDrafts struct {
	variant VariantDraft
	spec    SpecDraft
	upload  ImageUploadDraft
	reuse   ImageReuseDraft
}

func (h *Handler) editProductWithErrors(
	w http.ResponseWriter, r *http.Request, slug string, errs map[string]string, draft *productDrafts,
) {
	view, err := h.productView(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			admin.NotFound(w, r, h.log)
			return
		}
		h.log.ErrorContext(r.Context(), "rebuild product form", "slug", slug, "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.Errors = errs
	view.VariantDraft, view.SpecDraft = draft.variant, draft.spec
	view.ImageUploadDraft, view.ImageReuseDraft = draft.upload, draft.reuse
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Product(
		frame.Page{Refused: true, Title: view.Title(r.Context())}, view))
}

func attachImageRefusal(err error, alt string) (string, i18n.Key) {
	switch {
	case errors.Is(err, ErrInvalid):
		alt = strings.TrimSpace(alt)
		if alt == "" || utf8.RuneCountInString(alt) > MaxAltRunes {
			return "alt", i18n.KeyFormHeroAlt
		}
		return "alt_en", i18n.KeyFormCampaignAltEnLong
	case errors.Is(err, ErrNotThisProductsOption):
		return "image_option", i18n.KeyAdminNoticeBadOption
	case errors.Is(err, ErrRefused):
		return "image", i18n.KeyAdminNoticeAttachRefused
	default:
		return "", ""
	}
}

func (h *Handler) respondToUploadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, formtext.ErrUnstorable):
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
	case errors.Is(err, media.ErrUploadAborted):
		// The browser went away mid-upload; there is no one to answer.
	case media.IsRefusal(err):
		h.log.WarnContext(r.Context(), "image upload", "error", err, "slug", r.PathValue("slug"))
		h.rejectImageUpload(w, r, "image", media.UploadNotice(err))
	default:
		h.log.ErrorContext(r.Context(), "image upload", "error", err, "slug", r.PathValue("slug"))
		admin.ServerError(w, r, h.log)
	}
}
