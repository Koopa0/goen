package admin

import (
	"errors"
	"fmt"
	"maps"
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Products serves GET /admin/products.
func (h *Handler) Products(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Products(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read products", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminProducts(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageProducts)}, view))
}

// NewProduct serves GET /admin/products/new.
func (h *Handler) NewProduct(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.NewProduct(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "new product form", "error", err)
		h.serverError(w, r)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminProductForm(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageNewProduct)}, view))
}

// CreateProduct serves POST /admin/products.
func (h *Handler) CreateProduct(w http.ResponseWriter, r *http.Request) {
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
	slug, errs, err := h.store.CreateProduct(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create product", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectProduct(w, r, f, errs, true)
	default:
		//nolint:gosec // G710: slug came back from the database
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

// EditProduct serves GET /admin/products/{slug}.
func (h *Handler) EditProduct(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Product(r.Context(), r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "read product", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	if images, imgErr := h.store.ProductImages(r.Context(), r.PathValue("slug")); imgErr != nil {
		// Not fatal: losing the image strip is smaller than losing the page.
		h.log.ErrorContext(r.Context(), "read product images", "error", imgErr)
	} else {
		view.Images = images
	}
	if recent, recentErr := h.images.Recent(r.Context()); recentErr != nil {
		h.log.ErrorContext(r.Context(), "read recent uploads", "error", recentErr)
	} else {
		for _, obj := range recent {
			view.Library = append(view.Library, pages.AdminImage{
				Key: obj.Digest, Width: obj.Width, Height: obj.Height,
			})
		}
	}
	web.Render(w, r, h.log, http.StatusOK, pages.AdminProductForm(
		layouts.Page{Title: view.Name}, view))
}

// UpdateProduct serves POST /admin/products/{slug}.
func (h *Handler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
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

	errs, err := h.store.UpdateProduct(r.Context(), f)
	switch {
	case errors.Is(err, ErrNotFound):
		h.notFound(w, r)
	case err != nil:
		h.log.ErrorContext(r.Context(), "update product", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectProduct(w, r, f, errs, false)
	default:
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+f.Slug+"?ok=1", http.StatusSeeOther)
	}
}

// PublishProduct serves POST /admin/products/{slug}/status.
func (h *Handler) PublishProduct(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	err := h.store.SetProductStatus(r.Context(), slug, r.PostFormValue("status"))
	if err != nil {
		h.log.WarnContext(r.Context(), "set product status", "slug", slug, "error", err)
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?refused=1", http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: validated by the route's own slug
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

// AddVariant serves POST /admin/products/{slug}/variants.
func (h *Handler) AddVariant(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	f, draft, errs := variantFormOf(r)
	maps.Copy(errs, f.Validate(r.Context()))
	if len(errs) > 0 {
		h.editProductWithErrors(w, r, slug, errs, draft)
		return
	}
	errs, err := h.store.AddVariant(r.Context(), slug, f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "add variant", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, draft)
	default:
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

// variantFormOf parses the variant while retaining every submitted value.
func variantFormOf(r *http.Request) (*VariantForm, pages.AdminVariantDraft, map[string]string) {
	draft := pages.AdminVariantDraft{
		SKU: r.PostFormValue("sku"), Price: r.PostFormValue("price"),
		Compare: r.PostFormValue("compare"), Safety: r.PostFormValue("safety"),
		ParcelLongest: r.PostFormValue("parcel_longest"),
		ParcelSum:     r.PostFormValue("parcel_sum"),
		ParcelWeight:  r.PostFormValue("parcel_weight"),
	}
	errs := map[string]string{}
	safety := parseVariantCount(draft.Safety, safetyStockCeiling,
		"safety", i18n.T(r.Context(), i18n.KeyFormSafetyStock), errs)
	longest := parseVariantCount(draft.ParcelLongest, parcelLongestCeilingMM,
		"parcel_longest", fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormParcelMeasurement), parcelLongestCeilingMM), errs)
	sum := parseVariantCount(draft.ParcelSum, parcelSumCeilingMM,
		"parcel_sum", fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormParcelMeasurement), parcelSumCeilingMM), errs)
	weight := parseVariantCount(draft.ParcelWeight, parcelWeightCeilingG,
		"parcel_weight", fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormParcelMeasurement), parcelWeightCeilingG), errs)
	// ParsePrice, not a parser returning the figure alone: blank is a legitimate
	// compare-at price and it stores zero, so a collapsed unreadable figure is
	// indistinguishable from "no discount". /admin/stock prices through this too.
	price, priceOK := ParsePrice(r.PostFormValue("price"))
	compare, compareOK := ParsePrice(r.PostFormValue("compare"))
	if !priceOK {
		errs["price"] = i18n.T(r.Context(), i18n.KeyFormPricePositive)
	}
	if !compareOK {
		errs["compare"] = fmt.Sprintf(
			i18n.T(r.Context(), i18n.KeyFormCompareAmount), MaxPriceCents/100)
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
	value, ok := parseBoundedInt(raw, ceiling)
	if !ok {
		errs[field] = message
	}
	return value
}

// productFormOf reads the product form off a request without losing a bad term.
func productFormOf(r *http.Request) (form *ProductForm, errs map[string]string) {
	raw := r.PostFormValue("warranty_months")
	warranty, ok := parseBoundedInt(raw, MaxWarrantyMonths)
	errs = map[string]string{}
	if !ok {
		errs["warranty_months"] = i18n.T(r.Context(), i18n.KeyFormWarrantyMonths)
	}
	return &ProductForm{
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

// rejectProduct re-renders the form at 422 with what was typed still in it.
func (h *Handler) rejectProduct(w http.ResponseWriter, r *http.Request, f *ProductForm, errs map[string]string, isNew bool) {
	var view pages.AdminProductView
	var err error
	if isNew {
		view, err = h.store.NewProduct(r.Context())
	} else {
		view, err = h.store.Product(r.Context(), f.Slug)
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "rebuild product form", "error", err)
		h.serverError(w, r)
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
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminProductForm(
		layouts.Page{Title: view.Title(r.Context())}, view))
}

// UploadImage serves POST /admin/products/{slug}/images. Store then attach,
// deliberately NOT one transaction: storing is idempotent by content, so a
// failure between them leaves only an orphan UnreferencedMedia reclaims.
func (h *Handler) UploadImage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	obj, err := h.images.ReadUpload(w, r, "image")
	if err != nil {
		h.log.WarnContext(r.Context(), "image upload", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+uploadReason(err), http.StatusSeeOther)
		return
	}

	alt := r.PostFormValue("alt")
	if err := h.store.AttachImage(r.Context(), slug, obj.Digest, alt,
		r.PostFormValue("alt_en"), obj.Width, obj.Height); err != nil {
		h.log.WarnContext(r.Context(), "attach image", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+attachReason(err), http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

// ReuseImage serves POST /admin/products/{slug}/images/reuse, attaching an
// image ALREADY uploaded to a second product.
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
		http.Redirect(w, r, "/admin/products/"+slug+"?needs=1", http.StatusSeeOther)
		return
	}
	if err := h.store.AttachImage(r.Context(), slug, obj.Digest,
		r.PostFormValue("alt"), r.PostFormValue("alt_en"),
		obj.Width, obj.Height); err != nil {
		h.log.WarnContext(r.Context(), "attach reused image", "error", err, "slug", slug)
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?"+attachReason(err), http.StatusSeeOther)
		return
	}
	//nolint:gosec // G710: slug is the route's own path value
	http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
}

// RemoveImage serves POST /admin/products/{slug}/images/remove.
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

// AddOption serves POST /admin/products/{slug}/options.
func (h *Handler) AddOption(w http.ResponseWriter, r *http.Request) {
	h.optionWrite(w, r, func(slug string) (map[string]string, error) {
		return h.store.AddOption(r.Context(), slug, OptionDraft{
			Name:   r.PostFormValue("name"),
			NameEn: r.PostFormValue("name_en"),
		})
	})
}

// AddOptionValue serves POST /admin/products/{slug}/options/values.
func (h *Handler) AddOptionValue(w http.ResponseWriter, r *http.Request) {
	h.optionWrite(w, r, func(slug string) (map[string]string, error) {
		return h.store.AddOptionValue(r.Context(), slug, OptionDraft{
			OptionID: r.PostFormValue("option"),
			Name:     r.PostFormValue("value"),
			NameEn:   r.PostFormValue("value_en"),
		})
	})
}

// optionWrite is the shape both option forms share.
func (h *Handler) optionWrite(
	w http.ResponseWriter, r *http.Request, write func(slug string) (map[string]string, error),
) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("slug")
	errs, err := write(slug)
	switch {
	case err != nil:
		h.log.WarnContext(r.Context(), "write product option", "error", err, "slug", slug)
		h.notFound(w, r)
	case len(errs) > 0:
		h.editProductWithErrors(w, r, slug, errs, pages.AdminVariantDraft{})
	default:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

// AddSpec serves POST /admin/products/{slug}/specs.
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
		h.editProductWithErrors(w, r, slug, errs, pages.AdminVariantDraft{})
	default:
		//nolint:gosec // G710: slug is the route's own path value
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1", http.StatusSeeOther)
	}
}

// RemoveSpec serves POST /admin/products/{slug}/specs/remove.
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

// editProductWithErrors re-renders the edit page at 422 with the refusals on it.
func (h *Handler) editProductWithErrors(
	w http.ResponseWriter, r *http.Request, slug string, errs map[string]string, draft pages.AdminVariantDraft,
) {
	view, err := h.store.Product(r.Context(), slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "rebuild product form", "slug", slug, "error", err)
		h.serverError(w, r)
		return
	}
	view.Errors = errs
	view.VariantDraft = draft
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminProductForm(
		layouts.Page{Title: view.Title(r.Context())}, view))
}

// attachReason turns an attach failure into the query the page reads.
func attachReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalid):
		return "noalt=1"
	case errors.Is(err, ErrRefused):
		return "attachrefused=1"
	default:
		return "uploadfailed=1"
	}
}

// uploadReason turns a rejection into the query the page reads. Naming WHICH
// decoder refused a file would tell an attacker which decoders are wired up.
func uploadReason(err error) string {
	switch {
	case errors.Is(err, media.ErrTooLarge):
		return "toobig=1"
	case errors.Is(err, media.ErrNotAnImage):
		return "notimage=1"
	default:
		return "uploadfailed=1"
	}
}
