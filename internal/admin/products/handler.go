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

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
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

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
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

var notices = map[string]web.NoticeEntry{
	"ok":         web.Done(i18n.KeyAdminNoticeOK),
	"refused":    web.Refused(i18n.KeyAdminNoticeRefused),
	"imageneeds": web.Refused(i18n.KeyAdminNoticeImageNeeds),
	"badoption":  web.Refused(i18n.KeyAdminNoticeBadOption),
	"specfailed": web.Failed(i18n.KeyAdminNoticeSpecFailed),
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.List(r.Context(), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read products", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Products(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageProducts)}, view))
}

func (h *Handler) New(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.NewForm(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "new product form", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, admin.ProductForm(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewProduct)}, view))
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseLongTextForm(w, r, 2*maxDescriptionRunes); err != nil {
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
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectProduct(w, r, f, errs, true)
	default:
		//nolint:gosec // G710: slug came back from the database
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) Edit(w http.ResponseWriter, r *http.Request) {
	h.renderProduct(w, r, http.StatusOK, web.Notice(r, notices))
}

// product reads the editor's product; a failure to read its sales or reviews is
// logged, and the view says what is missing.
func (h *Handler) product(ctx context.Context, slug string) (admin.ProductView, error) {
	view, err := h.store.Product(ctx, slug)
	if errors.Is(err, ErrStanding) {
		h.log.ErrorContext(ctx, "read product sales and reviews", "error", err)
		err = nil
	}
	return view, err
}

func (h *Handler) productView(ctx context.Context, slug string) (admin.ProductView, error) {
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
			view.Library = append(view.Library, admin.Image{
				Key: obj.Digest, Width: obj.Width, Height: obj.Height,
			})
		}
	}
	return view, nil
}

func (h *Handler) renderProduct(w http.ResponseWriter, r *http.Request, status int, notice components.Result) {
	view, err := h.productView(r.Context(), r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			web.Render(w, r, h.log, http.StatusNotFound, admin.MissingRecord(
				layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminMissingProduct)},
				admin.MissingRecordView{Section: "products", Heading: i18n.T(r.Context(), i18n.KeyAdminMissingProduct), Body: i18n.T(r.Context(), i18n.KeyAdminNotFoundBody), BackLabel: i18n.KeyAdminBackProducts}))
			return
		}
		h.log.ErrorContext(r.Context(), "read product", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = notice

	web.Render(w, r, h.log, status, admin.ProductForm(
		layouts.Page{Title: view.Name}, view))
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseLongTextForm(w, r, 2*maxDescriptionRunes); err != nil {
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
		access.NotFound(w, r, h.log)
	case err != nil:
		h.log.ErrorContext(r.Context(), "update product", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectProduct(w, r, f, errs, false)
	default:
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+f.Slug+"?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	switch err := h.store.SetStatus(r.Context(), slug, r.PostFormValue("status")); {
	case err == nil:
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "set product status", "slug", slug, "error", err)
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "set product status", "slug", slug, "error", err)
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set product status", "slug", slug, "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) AddVariant(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
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
	case err != nil:
		h.log.ErrorContext(r.Context(), "add variant", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, &productDrafts{variant: draft})
	default:
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

func variantFormOf(r *http.Request) (*VariantForm, admin.VariantDraft, map[string]string) {
	draft := admin.VariantDraft{
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
	longest := parseVariantCount(draft.ParcelLongest, carrier.MaxParcelLongestMM,
		"parcel_longest", fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormParcelMeasurement), carrier.MaxParcelLongestMM), errs)
	sum := parseVariantCount(draft.ParcelSum, carrier.MaxParcelSumMM,
		"parcel_sum", fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormParcelMeasurement), carrier.MaxParcelSumMM), errs)
	weight := parseVariantCount(draft.ParcelWeight, carrier.MaxParcelWeightG,
		"parcel_weight", fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormParcelMeasurement), carrier.MaxParcelWeightG), errs)
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
	value, ok := web.ParseBounded(raw, ceiling)
	if !ok {
		errs[field] = message
	}
	return value
}

func productFormOf(r *http.Request) (form *Form, errs map[string]string) {
	raw := r.PostFormValue("warranty_months")
	warranty, ok := web.ParseBounded(raw, MaxWarrantyMonths)
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
	var view admin.ProductView
	var err error
	if isNew {
		view, err = h.store.NewForm(r.Context())
	} else {
		view, err = h.productView(r.Context(), f.Slug)
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "rebuild product form", "error", err)
		access.ServerError(w, r, h.log)
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
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.ProductForm(
		layouts.Page{Title: view.Title(r.Context())}, view))
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
			access.ServerError(w, r, h.log)
			return
		}
		h.log.WarnContext(r.Context(), "attach image", "error", err, "slug", slug)
		h.rejectImageUpload(w, r, field, key)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

func (h *Handler) ReuseImage(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
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
		http.Redirect(w, r, "/admin/products/"+slug+"?imageneeds=1", http.StatusSeeOther)
		return
	}
	if err := h.store.AttachImage(r.Context(), slug, obj.Digest,
		r.PostFormValue("alt"), r.PostFormValue("alt_en"), "",
		obj.Width, obj.Height); err != nil {
		field, key := attachImageRefusal(err, r.PostFormValue("alt"))
		if field == "" {
			h.log.ErrorContext(r.Context(), "attach reused image", "error", err, "slug", slug)
			access.ServerError(w, r, h.log)
			return
		}
		h.log.WarnContext(r.Context(), "attach reused image", "error", err, "slug", slug)
		h.rejectImageReuse(w, r, field, key)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

func (h *Handler) rejectImageUpload(w http.ResponseWriter, r *http.Request, field string, key i18n.Key) {
	h.editProductWithErrors(w, r, r.PathValue("slug"), map[string]string{field: i18n.T(r.Context(), key)}, &productDrafts{
		upload: admin.ProductImageUploadDraft{Alt: r.PostFormValue("alt"), AltEn: r.PostFormValue("alt_en"), OptionValue: r.PostFormValue("option_value")},
	})
}

func (h *Handler) rejectImageReuse(w http.ResponseWriter, r *http.Request, field string, key i18n.Key) {
	h.editProductWithErrors(w, r, r.PathValue("slug"), map[string]string{"reuse_" + field: i18n.T(r.Context(), key)}, &productDrafts{
		reuse: admin.ProductImageReuseDraft{Digest: r.PostFormValue("digest"), Alt: r.PostFormValue("alt"), AltEn: r.PostFormValue("alt_en")},
	})
}

func (h *Handler) SetImageOption(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	if err := h.store.SetImageOption(r.Context(), slug,
		r.PostFormValue("digest"), r.PostFormValue("option_value")); err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			access.NotFound(w, r, h.log)
		case errors.Is(err, ErrNotThisProductsOption):
			h.log.WarnContext(r.Context(), "set image option refused", "error", err, "slug", slug)
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, "/admin/products/"+slug+"?badoption=1", http.StatusSeeOther)
		case errors.Is(err, ErrRefused):
			h.log.WarnContext(r.Context(), "set image option refused", "error", err, "slug", slug)
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, "/admin/products/"+slug+"?refused=1", http.StatusSeeOther)
		default:
			h.log.ErrorContext(r.Context(), "set image option", "error", err, "slug", slug)
			access.ServerError(w, r, h.log)
		}
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

func (h *Handler) RemoveImage(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	if err := h.store.DetachImage(r.Context(), slug, r.PostFormValue("digest")); err != nil {
		if errors.Is(err, ErrNotFound) {
			access.NotFound(w, r, h.log)
			return
		}
		if errors.Is(err, ErrRefused) {
			h.log.WarnContext(r.Context(), "detach image refused", "error", err, "slug", slug)
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, "/admin/products/"+slug+"?refused=1", http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "detach image", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

func (h *Handler) MoveImage(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	err := h.store.MoveImage(r.Context(), slug, r.PostFormValue("digest"), ImageMove(r.PostFormValue("move")))
	switch {
	case err == nil:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrInvalid):
		h.log.WarnContext(r.Context(), "move image refused", "error", err, "slug", slug)
		h.renderProduct(w, r, http.StatusUnprocessableEntity,
			components.Result{Outcome: components.OutcomeRefused, Text: i18n.T(r.Context(), i18n.KeyAdminNoticeImageStale)})
	default:
		h.log.ErrorContext(r.Context(), "move image", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) AddOption(w http.ResponseWriter, r *http.Request) {
	h.optionWrite(w, r, func(slug string) (map[string]string, error) {
		return h.store.AddOption(r.Context(), slug, OptionDraft{
			Name:   r.PostFormValue("name"),
			NameEn: r.PostFormValue("name_en"),
		})
	})
}

func (h *Handler) AddOptionValue(w http.ResponseWriter, r *http.Request) {
	h.optionWrite(w, r, func(slug string) (map[string]string, error) {
		return h.store.AddOptionValue(r.Context(), slug, OptionDraft{
			OptionID:  r.PostFormValue("option"),
			Name:      r.PostFormValue("value"),
			NameEn:    r.PostFormValue("value_en"),
			SwatchHex: r.PostFormValue("swatch_hex"),
		})
	})
}

func (h *Handler) optionWrite(
	w http.ResponseWriter, r *http.Request, write func(slug string) (map[string]string, error),
) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	draft := admin.VariantDraft{
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
			access.NotFound(w, r, h.log)
			return
		}
		if errors.Is(err, ErrRefused) {
			h.log.WarnContext(r.Context(), "write product option refused", "error", err, "slug", slug)
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, "/admin/products/"+slug+"?refused=1", http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "write product option", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, &productDrafts{variant: draft})
	default:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) AddSpec(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
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
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "add spec refused", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?specfailed=1", http.StatusSeeOther)
	case err != nil:
		h.log.ErrorContext(r.Context(), "add spec", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, &productDrafts{spec: admin.SpecDraft{Label: draft.Label, Value: draft.Value, LabelEn: draft.LabelEn, ValueEn: draft.ValueEn}})
	default:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) RemoveSpec(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	if err := h.store.RemoveSpec(r.Context(), slug, r.PostFormValue("spec")); err != nil {
		if errors.Is(err, ErrNotFound) {
			access.NotFound(w, r, h.log)
			return
		}
		if errors.Is(err, ErrRefused) {
			h.log.WarnContext(r.Context(), "remove spec refused", "error", err, "slug", slug)
			//nolint:gosec // G710: slug is the route's own path value
			http.Redirect(w, r, "/admin/products/"+slug+"?refused=1", http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "remove spec", "error", err, "slug", slug)
		access.ServerError(w, r, h.log)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

type productDrafts struct {
	variant admin.VariantDraft
	spec    admin.SpecDraft
	upload  admin.ProductImageUploadDraft
	reuse   admin.ProductImageReuseDraft
}

func (h *Handler) editProductWithErrors(
	w http.ResponseWriter, r *http.Request, slug string, errs map[string]string, draft *productDrafts,
) {
	view, err := h.productView(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			access.NotFound(w, r, h.log)
			return
		}
		h.log.ErrorContext(r.Context(), "rebuild product form", "slug", slug, "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Errors = errs
	view.VariantDraft, view.SpecDraft = draft.variant, draft.spec
	view.ImageUploadDraft, view.ImageReuseDraft = draft.upload, draft.reuse
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.ProductForm(
		layouts.Page{Title: view.Title(r.Context())}, view))
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
	case errors.Is(err, web.ErrFormText):
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
	case media.IsRefusal(err):
		h.log.WarnContext(r.Context(), "image upload", "error", err, "slug", r.PathValue("slug"))
		h.rejectImageUpload(w, r, "image", media.UploadNotice(err))
	default:
		h.log.ErrorContext(r.Context(), "image upload", "error", err, "slug", r.PathValue("slug"))
		access.ServerError(w, r, h.log)
	}
}
