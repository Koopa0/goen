package db_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// textBound pairs a _bounded CHECK in migrations/001 with the constant the Go
// validator in front of the column compares against.
type textBound struct {
	constraint string
	// bytes is true when the validator counts len(s), so the CHECK must use
	// octet_length; otherwise it counts runes, and the CHECK uses char_length
	// (or length, the same function on text).
	bytes bool
	// file is the Go file from the repository root, and name the constant in it.
	file, name string
}

var textBounds = []textBound{
	// Storefront and account writers: the store role, behind these validators.
	{constraint: "users_email_bounded", bytes: true, file: "internal/email/email.go", name: "MaxBytes"},
	{constraint: "email_verifications_email_bounded", bytes: true, file: "internal/email/email.go", name: "MaxBytes"},
	{constraint: "stock_notifications_email_bounded", bytes: true, file: "internal/email/email.go", name: "MaxBytes"},
	{constraint: "order_private_data_email_bounded", bytes: true, file: "internal/email/email.go", name: "MaxBytes"},
	{constraint: "contact_messages_email_bounded", bytes: true, file: "internal/email/email.go", name: "MaxBytes"},
	{constraint: "newsletter_subscribers_email_bounded", bytes: true, file: "internal/email/email.go", name: "MaxBytes"},
	{constraint: "newsletter_confirmations_email_bounded", bytes: true, file: "internal/email/email.go", name: "MaxBytes"},
	{constraint: "users_full_name_bounded", file: "internal/user/user.go", name: "MaxNameRunes"},
	{constraint: "users_phone_bounded", file: "internal/account/account.go", name: "maxPhoneRunes"},
	{constraint: "user_identities_subject_bounded", file: "internal/auth/auth.go", name: "maxOAuthSubjectRunes"},
	{constraint: "sessions_user_agent_bounded", file: "internal/auth/auth.go", name: "maxUserAgentRunes"},
	{constraint: "addresses_label_bounded", file: "internal/account/account.go", name: "maxAddressLabelRunes"},
	{constraint: "orders_customer_note_bounded", file: "internal/order/delivery/delivery.go", name: "maxNoteRunes"},
	{constraint: "order_private_data_pickup_store_name_bounded", file: "internal/order/delivery/delivery.go", name: "maxStoreNameRunes"},
	{constraint: "invoice_preferences_customer_email_bounded", bytes: true, file: "internal/order/delivery/delivery.go", name: "maxInvoiceEmailBytes"},
	{constraint: "return_requests_reason_bounded", file: "internal/order/buyer/buyer.go", name: "MaxReturnReasonRunes"},
	{constraint: "product_reviews_title_bounded", file: "internal/catalog/review.go", name: "MaxReviewTitleRunes"},
	{constraint: "product_reviews_body_bounded", file: "internal/catalog/review.go", name: "MaxReviewBodyRunes"},
	{constraint: "product_questions_body_bounded", file: "internal/catalog/question.go", name: "MaxQuestionRunes"},
	{constraint: "contact_messages_name_bounded", file: "internal/contact/contact.go", name: "maxName"},
	{constraint: "contact_messages_order_ref_bounded", file: "internal/contact/contact.go", name: "maxOrderRef"},
	{constraint: "contact_messages_message_bounded", file: "internal/contact/contact.go", name: "maxMessage"},

	// Back-office writers: the admin role, behind the desks' validators.
	{constraint: "brands_name_bounded", file: "internal/admin/taxonomy/taxonomy.go", name: "MaxNameRunes"},
	{constraint: "categories_name_bounded", file: "internal/admin/taxonomy/taxonomy.go", name: "MaxNameRunes"},
	{constraint: "categories_name_en_bounded", file: "internal/admin/taxonomy/taxonomy.go", name: "MaxNameRunes"},
	{constraint: "categories_image_alt_bounded", file: "internal/admin/taxonomy/category_image.go", name: "MaxAltRunes"},
	{constraint: "categories_image_alt_en_bounded", file: "internal/admin/taxonomy/category_image.go", name: "MaxAltRunes"},
	{constraint: "products_slug_bounded", file: "internal/admin/products/product.go", name: "maxSlugRunes"},
	{constraint: "products_name_bounded", file: "internal/admin/products/product.go", name: "maxNameRunes"},
	{constraint: "products_name_en_bounded", file: "internal/admin/products/product.go", name: "maxNameRunes"},
	{constraint: "products_summary_bounded", file: "internal/admin/products/product.go", name: "maxSummaryRunes"},
	{constraint: "products_summary_en_bounded", file: "internal/admin/products/product.go", name: "maxSummaryRunes"},
	{constraint: "products_description_bounded", file: "internal/admin/products/product.go", name: "maxDescriptionRunes"},
	{constraint: "products_description_en_bounded", file: "internal/admin/products/product.go", name: "maxDescriptionRunes"},
	{constraint: "product_specs_label_bounded", file: "internal/admin/products/product.go", name: "SpecLabelRunes"},
	{constraint: "product_specs_label_en_bounded", file: "internal/admin/products/product.go", name: "SpecLabelRunes"},
	{constraint: "product_specs_value_bounded", file: "internal/admin/products/product.go", name: "SpecValueRunes"},
	{constraint: "product_specs_value_en_bounded", file: "internal/admin/products/product.go", name: "SpecValueRunes"},
	{constraint: "product_options_name_bounded", file: "internal/admin/products/product.go", name: "MaxOptionNameRunes"},
	{constraint: "product_options_name_en_bounded", file: "internal/admin/products/product.go", name: "MaxOptionNameRunes"},
	{constraint: "product_option_values_value_bounded", file: "internal/admin/products/product.go", name: "MaxOptionNameRunes"},
	{constraint: "product_option_values_value_en_bounded", file: "internal/admin/products/product.go", name: "MaxOptionNameRunes"},
	{constraint: "product_images_alt_bounded", file: "internal/admin/products/image.go", name: "MaxAltRunes"},
	{constraint: "product_images_alt_en_bounded", file: "internal/admin/products/image.go", name: "MaxAltRunes"},
	{constraint: "product_variants_sku_bounded", file: "internal/admin/products/variant.go", name: "maxSKURunes"},
	{constraint: "product_answers_body_bounded", file: "internal/admin/feedback/question.go", name: "MaxStaffAnswerRunes"},
	{constraint: "sale_campaigns_title_bounded", file: "internal/admin/campaigns/campaign.go", name: "MaxTitleRunes"},
	{constraint: "sale_campaigns_title_en_bounded", file: "internal/admin/campaigns/campaign.go", name: "MaxTitleRunes"},
	{constraint: "sale_campaigns_image_alt_bounded", file: "internal/admin/campaigns/campaign.go", name: "MaxAltRunes"},
	{constraint: "sale_campaigns_image_alt_en_bounded", file: "internal/admin/campaigns/campaign.go", name: "MaxAltRunes"},
	{constraint: "coupons_description_bounded", file: "internal/admin/coupons/coupon.go", name: "maxDescriptionRunes"},
	{constraint: "hero_slides_headline_bounded", file: "internal/admin/content/hero.go", name: "MaxHeadlineRunes"},
	{constraint: "promo_banners_message_bounded", file: "internal/admin/content/banner.go", name: "MaxBannerRunes"},
	{constraint: "promo_banners_message_short_bounded", file: "internal/admin/content/banner.go", name: "MaxBannerRunes"},
	{constraint: "promo_banners_message_en_bounded", file: "internal/admin/content/banner.go", name: "MaxBannerRunes"},
	{constraint: "promo_banners_message_short_en_bounded", file: "internal/admin/content/banner.go", name: "MaxBannerRunes"},
	{constraint: "faq_categories_name_bounded", file: "internal/admin/content/faq.go", name: "MaxFAQCategoryRunes"},
	{constraint: "faq_categories_name_en_bounded", file: "internal/admin/content/faq.go", name: "MaxFAQCategoryRunes"},
	{constraint: "faq_entries_question_bounded", file: "internal/admin/content/faq.go", name: "MaxFAQQuestionRunes"},
	{constraint: "faq_entries_question_en_bounded", file: "internal/admin/content/faq.go", name: "MaxFAQQuestionRunes"},
	{constraint: "faq_entries_answer_bounded", file: "internal/admin/content/faq.go", name: "MaxFAQAnswerRunes"},
	{constraint: "faq_entries_answer_en_bounded", file: "internal/admin/content/faq.go", name: "MaxFAQAnswerRunes"},
	{constraint: "newsletter_issues_subject_bounded", file: "internal/admin/content/newsletter.go", name: "MaxNewsletterSubjectRunes"},
	{constraint: "newsletter_issues_body_bounded", file: "internal/admin/content/newsletter.go", name: "MaxNewsletterBodyRunes"},
	{constraint: "shipping_method_versions_name_bounded", file: "internal/admin/shipping/store.go", name: "MaxNameRunes"},
	{constraint: "shipping_zones_name_bounded", file: "internal/admin/shipping/store.go", name: "MaxNameRunes"},
	{constraint: "store_credit_entries_reason_bounded", file: "internal/admin/credits/credit.go", name: "MaxCreditReasonRunes"},
	{constraint: "return_requests_resolution_bounded", file: "internal/returns/policy.go", name: "MaxResolutionRunes"},
	{constraint: "return_eligibility_assessments_basis_bounded", file: "internal/admin/returns/store.go", name: "maxAssessmentBasisRunes"},
}

// unpairedBounds are the _bounded CHECKs with no Go constant to mirror, each
// with the reason. A new one belongs in textBounds unless it fits a reason here.
var unpairedBounds = map[string]string{
	"return_request_lines_received_bounded":       "bounds a quantity by the line's quantity, not a length",
	"return_request_lines_restocked_bounded":      "bounds a quantity by the received quantity, not a length",
	"invoice_operations_resend_authority_bounded": "bounds a count by another count, not a length",
	"carts_checkout_draft_bounded":                "bounds a JSON document goen writes itself, not a field a person types",
	"invoice_documents_number_bounded":            "the provider's invoice number, written from its reply",
	"invoice_documents_provider_ref_bounded":      "the provider's reference, written from its reply",
	"invoice_documents_request_key_bounded":       "a key goen derives, not typed",
	"invoice_operations_error_bounded":            "a provider error goen truncates itself",
	"invoice_document_lines_description_bounded":  "derived by 001 from the order line; the issuer truncates to the same width",
	"invoice_preferences_customer_name_bounded":   "invoice.ValidCustomerName compares against the literal, not a constant",
	"return_request_lines_note_bounded":           "the inspection form trims the note but states no length; the browser's maxlength is the only other copy",
}

var (
	boundedName  = regexp.MustCompile(`\bCONSTRAINT\s+(\w+_bounded)\b`)
	boundedShape = regexp.MustCompile(`(?s)\bCONSTRAINT\s+(\w+_bounded)\s+CHECK\s*\(\s*` +
		`(?:\w+\s+IS\s+NULL\s+OR\s+)?(char_length|length|octet_length)\(\s*\w+\s*\)\s*<=\s*(\d+)\s*\)`)
)

// TestTextBoundsMirrorTheirValidators holds each length CHECK on a column
// people type into to the limit the Go validator in front of it enforces, in
// the same unit. The two had drifted: product_questions allowed 1000
// characters behind a 300-rune form, product_answers 2000 behind 1000.
func TestTextBoundsMirrorTheirValidators(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	src, err := os.ReadFile("../../migrations/001_initial_schema.up.sql")
	if err != nil {
		t.Fatalf("read migrations/001: %v", err)
	}
	schema := maskCalendarSQL(string(src), true)

	type bound struct {
		fn string
		n  int
	}
	shapes := map[string]bound{}
	for _, m := range boundedShape.FindAllStringSubmatch(schema, -1) {
		n, convErr := strconv.Atoi(m[3])
		if convErr != nil {
			t.Fatalf("%s: bound %q: %v", m[1], m[3], convErr)
		}
		shapes[m[1]] = bound{fn: m[2], n: n}
	}

	paired := map[string]bool{}
	consts := map[string]map[string]int{}
	for _, b := range textBounds {
		paired[b.constraint] = true
		got, ok := shapes[b.constraint]
		if !ok {
			t.Errorf("%s: not found in migrations/001 as CHECK (char_length|octet_length(col) <= N)", b.constraint)
			continue
		}
		if consts[b.file] == nil {
			consts[b.file] = intConsts(t, filepath.Join(root, filepath.FromSlash(b.file)))
		}
		want, ok := consts[b.file][b.name]
		if !ok {
			t.Errorf("%s: %s declares no integer constant %s", b.constraint, b.file, b.name)
			continue
		}
		if got.n != want {
			t.Errorf("%s allows %d, but %s.%s is %d: the database and the form disagree on the limit",
				b.constraint, got.n, b.file, b.name, want)
		}
		if b.bytes != (got.fn == "octet_length") {
			unit := "runes, so the CHECK must use char_length"
			if b.bytes {
				unit = "bytes, so the CHECK must use octet_length"
			}
			t.Errorf("%s counts with %s, but %s.%s counts %s", b.constraint, got.fn, b.file, b.name, unit)
		}
	}

	declared := map[string]bool{}
	for _, m := range boundedName.FindAllStringSubmatch(schema, -1) {
		name := m[1]
		declared[name] = true
		if paired[name] {
			continue
		}
		if _, ok := unpairedBounds[name]; !ok {
			t.Errorf("%s has no Go limit beside it: add it to textBounds with the validator's "+
				"constant, or to unpairedBounds with the reason none exists", name)
		}
	}
	for name := range unpairedBounds {
		if !declared[name] {
			t.Errorf("unpairedBounds names %s, which migrations/001 no longer declares", name)
		}
	}
}

// intConsts returns the integer constants a Go file declares with a literal value.
func intConsts(t *testing.T, path string) map[string]int {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]int{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.INT {
					continue
				}
				// Base 0 reads Go's own literal syntax: 0x, 0o, 1_000.
				n, convErr := strconv.ParseInt(lit.Value, 0, 64)
				if convErr != nil {
					t.Fatalf("%s: constant %s = %s: %v", path, name.Name, lit.Value, convErr)
				}
				out[name.Name] = int(n)
			}
		}
	}
	return out
}
