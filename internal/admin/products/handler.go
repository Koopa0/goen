package products

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/money"
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

var notices = map[string]i18n.Key{
	"ok":            i18n.KeyAdminNoticeOK,
	"refused":       i18n.KeyAdminNoticeRefused,
	"imageneeds":    i18n.KeyAdminNoticeImageNeeds,
	"toobig":        i18n.KeyAdminNoticeTooBig,
	"notimage":      i18n.KeyAdminNoticeNotImage,
	"losslesswebp":  i18n.KeyAdminNoticeLosslessWebP,
	"uploadfailed":  i18n.KeyAdminNoticeUploadFailed,
	"uploadbusy":    i18n.KeyAdminNoticeUploadBusy,
	"attachrefused": i18n.KeyAdminNoticeAttachRefused,
	"noalt":         i18n.KeyAdminNoticeNoAlt,
	"badoption":     i18n.KeyAdminNoticeBadOption,
	"specfailed":    i18n.KeyAdminNoticeSpecFailed,
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
	if err := web.ParseForm(w, r); err != nil {
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

func (h *Handler) renderProduct(w http.ResponseWriter, r *http.Request, status int, notice string) {
	view, err := h.store.Product(r.Context(), r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			access.NotFound(w, r, h.log)
			return
		}
		h.log.ErrorContext(r.Context(), "read product", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = notice
	if images, imgErr := h.store.Images(r.Context(), r.PathValue("slug")); imgErr != nil {
		// Not fatal: losing the image strip is smaller than losing the page.
		h.log.ErrorContext(r.Context(), "read product images", "error", imgErr)
	} else {
		view.Images = images
	}
	if recent, recentErr := h.images.Recent(r.Context()); recentErr != nil {
		h.log.ErrorContext(r.Context(), "read recent uploads", "error", recentErr)
	} else {
		for _, obj := range recent {
			view.Library = append(view.Library, admin.Image{
				Key: obj.Digest, Width: obj.Width, Height: obj.Height,
			})
		}
	}
	web.Render(w, r, h.log, status, admin.ProductForm(
		layouts.Page{Title: view.Name}, view))
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
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
	err := h.store.SetStatus(r.Context(), slug, r.PostFormValue("status"))
	if err != nil {
		h.log.WarnContext(r.Context(), "set product status", "slug", slug, "error", err)
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?refused=1", http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: validated by the route's own slug
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
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
		h.editProductWithErrors(w, r, slug, errs, &draft)
		return
	}
	errs, err := h.store.AddVariant(r.Context(), slug, f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "add variant", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, &draft)
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
		view, err = h.store.Product(r.Context(), f.Slug)
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "rebuild product form", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Slug, view.Name, view.Summary = f.Slug, f.Name, f.Summary
	view.Description, view.WarrantyNote = f.Description, f.WarrantyNote
	view.WarrantyMonthsRaw = f.WarrantyMonthsRaw
	view.WarrantyMonths = f.WarrantyMonths
	view.NameEn, view.SummaryEn = f.NameEn, f.SummaryEn
	view.DescriptionEn = f.DescriptionEn
	view.BrandID, view.CategoryID = f.BrandID, f.CategoryID
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
		h.log.WarnContext(r.Context(), "image upload", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+media.UploadQuery(err), http.StatusSeeOther)
		return
	}

	alt := r.PostFormValue("alt")
	if err := h.store.AttachImage(r.Context(), slug, obj.Digest, alt,
		r.PostFormValue("alt_en"), r.PostFormValue("option_value"),
		obj.Width, obj.Height); err != nil {
		h.log.WarnContext(r.Context(), "attach image", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+attachReason(err), http.StatusSeeOther)
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
		h.log.WarnContext(r.Context(), "attach reused image", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+attachReason(err), http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

func (h *Handler) SetImageOption(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	if err := h.store.SetImageOption(r.Context(), slug,
		r.PostFormValue("digest"), r.PostFormValue("option_value")); err != nil {
		h.log.WarnContext(r.Context(), "set image option", "error", err, "slug", slug)
		reason := "refused=1"
		if errors.Is(err, ErrNotThisProductsOption) {
			reason = "badoption=1"
		}
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+reason, http.StatusSeeOther)
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
		h.log.WarnContext(r.Context(), "detach image", "error", err, "slug", slug)
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
		h.renderProduct(w, r, http.StatusUnprocessableEntity, i18n.T(r.Context(), i18n.KeyAdminNoticeImageStale))
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
		h.log.WarnContext(r.Context(), "write product option", "error", err, "slug", slug)
		// A constraint the form has a control for is a refusal and not a
		// missing product. Without this the page tells a staff member who
		// mistyped a colour that the product they are looking at is gone.
		if refused := optionRefusal(r.Context(), err); len(refused) > 0 {
			h.editProductWithErrors(w, r, slug, refused, &draft)
			return
		}
		access.NotFound(w, r, h.log)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, &draft)
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
	errs, err := h.store.AddSpec(r.Context(), slug, SpecDraft{
		Label:   r.PostFormValue("label"),
		Value:   r.PostFormValue("value"),
		LabelEn: r.PostFormValue("label_en"),
		ValueEn: r.PostFormValue("value_en"),
	})
	switch {
	case err != nil:
		h.log.WarnContext(r.Context(), "add spec", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?specfailed=1", http.StatusSeeOther)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, &admin.VariantDraft{})
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
		h.log.WarnContext(r.Context(), "remove spec", "error", err, "slug", slug)
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

func (h *Handler) editProductWithErrors(
	w http.ResponseWriter, r *http.Request, slug string, errs map[string]string, draft *admin.VariantDraft,
) {
	view, err := h.store.Product(r.Context(), slug)
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
	view.VariantDraft = *draft
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.ProductForm(
		layouts.Page{Title: view.Title(r.Context())}, view))
}

func attachReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalid):
		return "noalt=1"
	case errors.Is(err, ErrNotThisProductsOption):
		return "badoption=1"
	case errors.Is(err, ErrRefused):
		return "attachrefused=1"
	default:
		return "uploadfailed=1"
	}
}
