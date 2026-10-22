package repocheck_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// maxTestNameLen is the most characters a top-level test name may have after
// "Test". It is goen's own limit, measured rather than cited: the standard
// library's test names run to 42 at the 99th percentile and 14 at the median,
// and no style guide states a number.
const maxTestNameLen = 40

// migratedTestPackages lists, by directory relative to the repository root,
// the packages whose test names have been migrated to the convention in
// CONTRIBUTING.md. Only these packages are checked: a package joins the list in
// the pull request that renames its tests, so a name cannot drift back, and
// nothing else in the repository is held to the rule until then.
//
// TestMigratedTestNames reads every listed package and fails if one has no
// tests to read; TestCheckTestNames runs the same checks over fixtures that
// fail them, so the guard cannot pass without looking.
var migratedTestPackages = []string{
	"assets",
	"internal/account",
	"internal/admin",
	"internal/admin/audit",
	"internal/admin/banners",
	"internal/admin/campaigns",
	"internal/admin/coupons",
	"internal/admin/credits",
	"internal/admin/customers",
	"internal/admin/dashboard",
	"internal/admin/faq",
	"internal/admin/health",
	"internal/admin/messages",
	"internal/admin/newsletters",
	"internal/admin/orders",
	"internal/admin/products",
	"internal/admin/questions",
	"internal/admin/refunds",
	"internal/admin/reports",
	"internal/admin/returns",
	"internal/admin/reviews",
	"internal/admin/shipping",
	"internal/admin/slides",
	"internal/admin/staff",
	"internal/admin/stock",
	"internal/admin/taxonomy",
	"internal/admin/tiers",
	"internal/admin/twofactor",
	"internal/admin/warranties",
	"internal/backinstock",
	"internal/bearer",
	"internal/checkout",
	"internal/contact",
	"internal/credit",
	"internal/discount/campaign",
	"internal/discount/coupon",
	"internal/email",
	"internal/i18n",
	"internal/invoice",
	"internal/invoice/issuer",
	"internal/keyset",
	"internal/letter",
	"internal/loyalty",
	"internal/media",
	"internal/money",
	"internal/newsletter",
	"internal/order",
	"internal/order/access",
	"internal/order/cancellation",
	"internal/order/delivery",
	"internal/order/returns",
	"internal/order/warranty",
	"internal/outbox",
	"internal/payment",
	"internal/policy",
	"internal/product",
	"internal/product/copurchase",
	"internal/product/inventory",
	"internal/product/labelling",
	"internal/shipping",
	"internal/shipping/postcode",
	"internal/shipping/storemap",
	"internal/shoptime",
	"internal/site",
	"internal/ui/chart",
	"internal/ui/fieldrule",
	"internal/ui/frame",
	"internal/ui/icons",
	"internal/ui/orderstatus",
	"internal/ui/stockrisk",
	"internal/ui/tile",
	"internal/ui/tone",
	"internal/ui/uitest",
	"internal/ui/widget",
	"internal/user",
	"internal/web/clientip",
	"internal/web/compress",
	"internal/web/formtext",
	"internal/web/probe",
	"internal/web/ratelimit",
	"internal/web/requestid",
	"internal/web/respond",
	"internal/web/siteurl",
	"internal/wishlist",
}

// testNameExceptions is the shrink-only allow list: for a listed package, each
// test that breaks the rule and why. A row is stale, and fails, once its test is
// renamed or deleted or no longer breaks the rule. Do not add a row to let a
// new name pass; rename the test. A row belongs here only for a test whose unit
// is not a Go identifier, such as a SQL object or a repository-wide rule.
var testNameExceptions = map[string]map[string]string{
	"assets": {
		"TestEveryCustomPropertyUsedIsDeclared":    "a rule of the whole stylesheet set: it walks every file under css/, so no one declared identifier is its unit",
		"TestEveryFocusOutlineColourIsTheRing":     "a rule of two stylesheets at once: it reads AppCSS and AdminCSS alike, so naming it after either would misstate its unit",
		"TestEveryFontFamilyCarriesItsLicence":     "a rule about the embedded font directories and their OFL.txt files, not about a function or type; no declared identifier names fonts/",
		"TestFieldBoundariesHoldContrast":          "a rule of three stylesheets at once: it reads one boundary rule each from AppCSS, AdminCSS and BaseCSS, so naming it after one would misstate its unit",
		"TestNoStylesheetFetchesFromAnotherOrigin": "a rule of the whole stylesheet set (the style-src 'self' promise): it walks every file under css/, so no one declared identifier is its unit",
		"TestSharedLayoutRulesAreDeclaredOnce":     "a rule of two stylesheets at once: it counts one selector in AppCSS and one in AdminCSS, so naming it after either would misstate its unit",
	},
	"internal/admin": {
		"TestClosedDatabaseWritesNotRefused":           "a rule across the back-office stores: it closes the database under products and shipping writes and asks that neither answers ErrRefused or ErrNotFound; its units are those stores' methods in other packages, and no identifier of package admin is one of them",
		"TestEveryAdminQueueReachesBeyondItsFirstPage": "a rule across every back-office queue: it seeds 101 rows and follows each desk's next link through its own handler; the desks are other packages, and no identifier of package admin is its unit",
		"TestEveryBackOfficeWriteLeavesATrail":         "a rule across the back-office stores: it runs one write of each (stock, product, credit, campaign and the rest) and counts an audit row for every action; the stores are other packages, and no identifier of package admin is its unit",
	},
	"internal/admin/orders": {
		"TestDeskRefusalPOSTs":                     "a rule across the back-office desks: it posts real refusals to the banners, FAQ, media, products, returns, shipping, slides, stock and taxonomy handlers and asks that each keeps the draft and locates its form; those desks are other packages, and no identifier of package orders is its unit",
		"TestEntry_LabelParcelArrival":             "the unit is audit.Entry.Label and EntityLabel (and the audit page that renders them) of package audit, which this package does not declare; moving the test there is a reorganisation this pass does not do",
		"TestFulfillmentStatus_NextMatchesTrigger": "the unit is order.FulfillmentStatus.Next of package order, held against the orders_legal_transition trigger through the database; this package declares neither",
		"TestNotFoundKeepsStaffInBackOffice":       "the unit is admin.NotFound, declared in package admin, driven through the campaigns, customers, media, orders and products desks; no identifier of package orders is its unit",
		"TestRecipients_OfDelivered":               "the unit is notice.Recipients.Of of package notice (with the OrderTracking query beside it), which this package does not declare; the test lives here for the shipment fixtures",
		"TestRecipients_OfNoParcel":                "the unit is notice.Recipients.Of of package notice, which this package does not declare; the test lives here for the order fixtures",
		"TestRecipients_OfUndelivered":             "the unit is notice.Recipients.Of of package notice (with the OrderTracking query beside it), which this package does not declare; the test lives here for the shipment fixtures",
		"TestTerminalOrderRecipient":               "the unit is the TerminalOrderRecipient query (a sqlc statement declared in internal/db), called directly through db.Queries, not a Go identifier of this package",
	},
	"internal/admin/products": {
		"TestLocalizedNameAltText":   "the unit is the SQL function localized_name, which picks an image's alt text by the page's language; the test reads it through the pool after AttachImage stores the two texts, and calls no Go identifier of this package for the result",
		"TestLockProductInvoiceLine": "the unit is the LockProductInvoiceLine query (internal/admin/products/query.sql, generated into internal/db), a SQL object the test calls directly to hold the lock that Store.SetProductInvoiceLine takes, and it asserts that a row referencing the product stays writable meanwhile; Store.SetProductInvoiceLine is not called",
		"TestLockProductLabel":       "the unit is the LockProductLabel query (internal/admin/products/query.sql, generated into internal/db), a SQL object the test calls directly to hold the lock that Store.SetProductLabel takes, and it asserts that a row referencing the product stays writable meanwhile; Store.SetProductLabel is not called",
		"TestParseBoundedCeilings":   "the unit is formtext.ParseBounded of package formtext, called with this desk's own ceilings (MaxWarrantyMonths, safetyStockCeiling and the parcel ceilings of package shipping); this package declares no identifier the test calls, and moving it is a reorganisation this pass does not do",
	},
	"internal/admin/refunds": {
		"TestClaimReturnRefundExecutionConcurrent": "the unit is the SQL function claim_return_refund_execution, called straight through the pool; no Go identifier of this package is what the test drives",
		"TestOrderPageIssuedInvoiceNoProvider":     "the unit is Store.Order of package orders, which this package does not declare; moving the test there is a reorganisation this pass does not do",
		"TestRecordRefundOutcomes":                 "the unit is the SQL functions record_refund_pending, _requires_action, _succeeded, _failed, _cancelled and _api_rejection, called straight through the pool, not a Go identifier",
		"TestRecordRefundSucceededLockOrder":       "the unit is the SQL function record_refund_succeeded, raced against claim_return_refund_execution through the pool, not a Go identifier",
		"TestRecordRefundSucceededReplay":          "the unit is the SQL function record_refund_succeeded, called straight through the pool, not a Go identifier",
		"TestReverseReturnPointsErasureLock":       "the unit is the SQL function reverse_return_points, raced against an erasure through the pool, not a Go identifier",
	},
	"internal/admin/returns": {
		"TestCompensateReturnTwice":       "the unit is the SQL function compensate_return_with_credit, called a second time straight through the pool after one Store.Decide; no Go identifier of this package is what the second call exercises",
		"TestEvaluateApprovalEntitlement": "the unit is returns.Evaluate of internal/order/returns, which this package does not declare; the test sits in this package's decision_test.go and moving it to the policy package is a reorganisation this pass does not do",
		"TestEvaluateStatutoryRejection":  "the unit is returns.Evaluate of internal/order/returns, which this package does not declare; the test sits in this package's decision_test.go and moving it to the policy package is a reorganisation this pass does not do",
		"TestReportAtRefundFigure":        "the unit is Store.ReportAt of package reports and the order_refunds view it reads, which this package does not declare; Store.Decide only makes the refund the figure must count",
	},
	"internal/admin/reports": {
		"TestStockDaysCoverRefundedBeforeShipment": "the unit is Store.DaysCover of package stock, which this package does not declare (it declares only the Cover interface it calls through); moving the test there is a reorganisation this pass does not do",
	},
	"internal/admin/staff": {
		"TestRequireAdminStaffPage": "its unit is admin.Control.RequireAdmin guarding the staff page's path, declared in package admin; it calls nothing this package declares",
		"TestUpsertStaffExisting":   "its unit is the upsert_staff SQL function and the users_staff_already_exists constraint, called straight through an admin-role pool; Store.AddStaff is not what it exercises",
	},
	"internal/admin/stock": {
		"TestProductVariantsRestockNoticesRollback": "the unit is the product_variants_restock_notices trigger, a SQL object, driven by plain UPDATE statements on the admin role's pool, not a Go identifier",
	},
	"internal/admin/taxonomy": {
		"TestCategoriesIconKeyKnown": "the unit is the categories_icon_key_known CHECK constraint of the categories table, a SQL object, not a Go identifier: the test inserts rows directly and never calls the Store",
	},
	"internal/admin/tiers": {
		"TestAdminMembershipTiers": "its unit is the AdminMembershipTiers query (internal/admin/tiers/query.sql, generated into internal/db), a SQL object it calls directly in a read-only transaction, not Store.Tiers",
	},
	"internal/admin/twofactor": {
		"TestRecordTOTPStepConcurrent": "its unit is the SQL statement RecordTOTPStep (a sqlc query, declared in internal/db), driven directly through db.Queries from concurrent goroutines; Store.Verify is only the final check that the spent step is refused, because staggered Verify calls would hide a guard missing from the SQL",
	},
	"internal/backinstock": {
		"TestSweepNotifiedStockNotificationsRetention": "the unit is the SQL function sweep_notified_stock_notifications, called straight through the maintenance pool with a zero and a negative retention; Store.Sweep is not what it exercises",
	},
	//nolint:gosec // G101: test names such as TestReverseOrderCreditShipped match the credential pattern; none is a credential
	"internal/checkout": {
		"TestAccessStore_GrantSecondOrder":             "the unit is access.Store.Grant of package order/access, a different type from this package's Store; the test lives here for the orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestAccessStore_GrantStaleCarried":            "the unit is access.Store.Grant of package order/access, a different type from this package's Store; the test lives here for the orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_FindOrderAddressBound":       "the unit is buyer.Handler.FindOrder of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_FindOrderFullWidth":          "the unit is buyer.Handler.FindOrder of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_FindOrderGrantFailure":       "the unit is buyer.Handler.FindOrder of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_FindOrderRandomNumber":       "the unit is buyer.Handler.FindOrder of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_OrderPageEnumeration":        "the unit is buyer.Handler.OrderPage of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_OrderPageForgedCookie":       "the unit is buyer.Handler.OrderPage of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_OrderPageOtherOrderToken":    "the unit is buyer.Handler.OrderPage of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_OrderPagePaymentReturn":      "the unit is buyer.Handler.OrderPage of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_CancelOrderCredit":             "the unit is buyer.Store.CancelOrder of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_CancelOrderNoVoidDue":          "the unit is buyer.Store.CancelOrder of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_OrderDiscountReason":           "the unit is buyer.Store.Order of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_OrderNoDiscountReason":         "the unit is buyer.Store.Order of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_OrderParcelDays":               "the unit is buyer.Store.Order of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_OrderPickupDeliveryTo":         "the unit is buyer.Store.Order of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_OrderRefundedSettled":          "the unit is buyer.Store.Order of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_OrderSecondReturn":             "the unit is buyer.Store.Order of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_OrderShippingName":             "the unit is buyer.Store.Order of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestCartStore_ShippingChoicesLocale":          "the unit is cart.Store.ShippingChoices of package cart, a different type from this package's Store; the test lives here for the checkout shipping fixtures, and moving it is a reorganisation this pass does not do",
		"TestCartStore_ShippingChoicesParcel":          "the unit is cart.Store.ShippingChoices of package cart, a different type from this package's Store; the test lives here for the checkout shipping fixtures, and moving it is a reorganisation this pass does not do",
		"TestCouponsAmountPositive":                    "the unit is the coupons_amount_positive CHECK constraint of the coupons table, a SQL object: the test reads its ceiling from pg_constraint; this package declares no identifier for it",
		"TestDetails_DropOtherDestination":             "the unit is delivery.Details.DropOtherDestination of package delivery, which this package does not declare; the test lives here with the checkout form it guards, and moving it is a reorganisation this pass does not do",
		"TestDetails_ValidateAccepts":                  "the unit is delivery.Details.Validate of package delivery, which this package does not declare; the test lives here with the checkout form it guards, and moving it is a reorganisation this pass does not do",
		"TestDetails_ValidateBlankCity":                "the unit is delivery.Details.Validate of package delivery, which this package does not declare; the test lives here with the checkout form it guards, and moving it is a reorganisation this pass does not do",
		"TestDetails_ValidateBlankPostcode":            "the unit is delivery.Details.Validate of package delivery, which this package does not declare; the test lives here with the checkout form it guards, and moving it is a reorganisation this pass does not do",
		"TestDetails_ValidateDestination":              "the unit is delivery.Details.Validate of package delivery, which this package does not declare; the test lives here with the checkout form it guards, and moving it is a reorganisation this pass does not do",
		"TestDetails_ValidatePickupChain":              "the unit is delivery.Details.Validate of package delivery, which this package does not declare; the test lives here with the checkout form it guards, and moving it is a reorganisation this pass does not do",
		"TestDetails_ValidateStoreCode":                "the unit is delivery.Details.Validate of package delivery, which this package does not declare; the test lives here with the checkout form it guards, and moving it is a reorganisation this pass does not do",
		"TestDetails_ValidateStorePair":                "the unit is delivery.Details.Validate of package delivery, which this package does not declare; the test lives here with the checkout form it guards, and moving it is a reorganisation this pass does not do",
		"TestHoldInventoryLastUnit":                    "the unit is the SQL function hold_inventory, called directly through the pool; this package declares no identifier for it",
		"TestHoldInventoryLockOrder":                   "the unit is the SQL function hold_inventory, called directly through the pool; this package declares no identifier for it",
		"TestIssuerStore_ClaimVoidDue":                 "the unit is issuer.Store.ClaimVoidDue of package invoice/issuer, a different type from this package's Store; the test lives here for the credit-paid orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestIssuerStore_ClaimVoidDueNoProvider":       "the unit is issuer.Store.ClaimVoidDue of package invoice/issuer, a different type from this package's Store; the test lives here for the credit-paid orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestIssuerStore_ClaimVoidDuePastWindow":       "the unit is issuer.Store.ClaimVoidDue of package invoice/issuer, a different type from this package's Store; the test lives here for the credit-paid orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestIssuerStore_ClaimVoidDueWithdraw":         "the unit is issuer.Store.ClaimVoidDue of package invoice/issuer, a different type from this package's Store; the test lives here for the credit-paid orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestOrderAccessibleWithRetain":                "the unit is the OrderAccessibleWith query (a sqlc statement declared in internal/db), called through db.Queries inside one transaction, not a Go identifier of this package",
		"TestOrderLinesWrittenWhilePlacing":            "the unit is the order_lines_written_while_placing constraint (with inventory_hold_within_order_lines and coupon_redemptions_written_while_placing, which it also asserts), SQL objects driven as the store role against an order a committed checkout placed; this package declares no identifier for them",
		"TestOrderLinesWrittenWhilePlacingRestored":    "the unit is the order_lines_written_while_placing constraint, a SQL object driven as the store role against an order a committed checkout placed; this package declares no identifier for it",
		"TestOrderLinesWrittenWhilePlacingSameInstant": "the unit is the order_lines_written_while_placing constraint, a SQL object driven as the store role against an order a committed checkout placed; this package declares no identifier for it",
		"TestParseDestination":                         "the unit is shipping.ParseDestination of package shipping, called directly; this package declares no identifier for it, and moving the test is a reorganisation this pass does not do",
		"TestReleaseReservationLockOrder":              "the unit is the SQL function release_reservation, called directly through the pool; this package declares no identifier for it",
		"TestReturnWindowEnds":                         "the unit is the SQL functions return_window_ends and return_line_policy_window, called as the store role; this package declares no identifier for them",
		"TestReverseOrderCreditIdempotent":             "the unit is the SQL function reverse_order_credit, called directly through the pool on an order the test's own fixture built; this package declares no identifier for it",
		"TestReverseOrderCreditShipped":                "the unit is the SQL function reverse_order_credit, called directly through the pool on an order the test's own fixture built; this package declares no identifier for it",
		"TestSessionSweeper_SweepLostExpiry":           "the unit is payment.SessionSweeper.Sweep of package payment, which this package does not declare; the test lives here for the part-credit orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestSessionSweeper_SweepNotLostExpiry":        "the unit is payment.SessionSweeper.Sweep of package payment, which this package does not declare; the test lives here for the part-credit orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestSessionSweeper_SweepTwice":                "the unit is payment.SessionSweeper.Sweep of package payment, which this package does not declare; the test lives here for the part-credit orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestSessionSweeper_SweepWebhookRace":          "the unit is payment.SessionSweeper.Sweep of package payment, which this package does not declare; the test lives here for the part-credit orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestStripeReturnPages":                        "the unit is the two pages Stripe sends a customer back to, buyer.Handler.OrderPage and payment.Handler.Page, held to one cross-site reload rule; both are other packages' handlers, and no identifier of this package is its unit",
		"TestSweeper_SweepCustomerCancelRace":          "the unit is lapse.Sweeper.Sweep of package order/lapse, which this package does not declare; the test lives here for the orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestSweeper_SweepKeepsMoneyOrders":            "the unit is lapse.Sweeper.Sweep of package order/lapse, which this package does not declare; the test lives here for the orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestSweeper_SweepLapsedOrder":                 "the unit is lapse.Sweeper.Sweep of package order/lapse, which this package does not declare; the test lives here for the orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestSweeper_SweepLateRefund":                  "the unit is lapse.Sweeper.Sweep of package order/lapse, which this package does not declare; the test lives here for the orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestSweeper_SweepPaymentAfterLock":            "the unit is lapse.Sweeper.Sweep of package order/lapse, which this package does not declare; the test lives here for the orders checkout places, and moving it is a reorganisation this pass does not do",
	},
	"internal/discount/campaign": {
		"TestImage_URLHeaderKey":        "the unit is assets.Image.URL and Srcset of package assets, which resolve a campaign's header key as they resolve a product image; this package declares no identifier for it, and moving the test to assets is a reorganisation this pass does not do",
		"TestSaleCampaignNeedsDiscount": "the unit is the sale_campaign_needs_discount trigger on sale_campaign_products, a SQL object the test provokes by featuring products through the pool, not a Go identifier",
	},
	"internal/i18n": {
		"TestNoHardCodedGenericChrome": "a repository rule: no customer-facing .templ source hard-codes a generic English word; it reads every source file, and no function of the package is its unit",
		"TestNoHardCodedHan":           "a repository rule: no customer-facing source file hard-codes a Han string; it reads every source file, and no function of the package is its unit",
		"TestPendingTranslationDebt":   "a rule about pendingTranslation, a list declared in the test file beside the two rules above, so there is no package identifier to start from",
	},
	"internal/invoice/issuer": {
		"TestCanonicalInvoiceLinesDeliveryFee":       "its unit is the SQL function canonical_invoice_lines, run through the database; the package declares no identifier for it",
		"TestCanonicalInvoiceLinesExempt":            "its unit is the SQL function canonical_invoice_lines, run through the database; the package declares no identifier for it",
		"TestCanonicalInvoiceLinesMultiply":          "its unit is the SQL function canonical_invoice_lines, run through the database; the package declares no identifier for it",
		"TestCanonicalInvoiceLinesSchemaLimits":      "its unit is the SQL function canonical_invoice_lines, run through the database; the package declares no identifier for it",
		"TestCanonicalInvoiceLinesSum":               "its unit is the SQL function canonical_invoice_lines, run through the database; the package declares no identifier for it",
		"TestClaimInvoiceAllowanceRoundsOnce":        "its unit is the SQL function claim_invoice_allowance, called directly as the admin role; Store.FileAllowance is not what it exercises",
		"TestClaimInvoiceAllowanceSubtractsPrior":    "its unit is the SQL function claim_invoice_allowance, called directly as the admin role; Store.FileAllowance is not what it exercises",
		"TestInvoiceDoorNullInputs":                  "its unit is the invoice persistence-door SQL functions (claim_*, lease_*, reschedule_*), called directly as the admin role; they are not Go identifiers",
		"TestInvoiceDoorPrivileges":                  "its unit is the EXECUTE grants on the invoice door functions and the INSERT grants on the invoice tables, SQL objects, not Go identifiers",
		"TestInvoiceDoorRuleNames":                   "its unit is the rule branches of the invoice persistence-door SQL functions, each named by its constraint, called directly as the admin role",
		"TestInvoiceIssueItemCount":                  "its unit is the invoice_issue_item_count constraint, a SQL object enforced at commit through canonical_invoice_lines, not a Go identifier",
		"TestInvoicePreferencesClosedSet":            "its unit is the CHECK constraints of the invoice_preferences table (invoice_preferences_type_known), a SQL object, not a Go identifier",
		"TestReconcileInvalidInvoiceAllowance":       "its unit is the SQL function reconcile_invalid_invoice_allowance, called directly; the package declares no identifier for it",
		"TestReconcileInvalidInvoiceAllowanceExempt": "its unit is the SQL function reconcile_invalid_invoice_allowance, called directly; the package declares no identifier for it",
		"TestRelateNumber":                           "its unit is relateNumber, a helper declared in the test file that mirrors the RelateNumber rule claim_invoice_issue applies in SQL; no non-test identifier names it",
		"TestSettleInvoiceAllowanceLineFields":       "its unit is the SQL function settle_invoice_allowance, called directly as the admin role; the package declares no identifier for it",
		"TestSettleInvoiceAllowanceLineTerms":        "its unit is the SQL functions settle_invoice_allowance and record_invalid_invoice_allowance, called directly as the admin role",
		"TestSettleInvoiceIssueErasedActor":          "its unit is the SQL function settle_invoice_issue, called directly as the admin role after erase_user; Store.Issue is not what it exercises",
		"TestSettleInvoiceIssueLineFields":           "its unit is the SQL function settle_invoice_issue, called directly as the admin role; the package declares no identifier for it",
		"TestTestOrderNumberIsUnique":                "its unit is testOrderNumber, the order-number helper of the integration tests, declared in the test file, so the package declares no identifier for it",
		"TestValidBusinessTaxID":                     "its unit is the SQL function valid_business_tax_id, held to invoice.ValidTaxID, which package invoice declares; this package declares no identifier for it",
	},
	"internal/loyalty": {
		"TestAwardLoyaltyPointsGuest":         "the unit is the SQL function award_loyalty_points, called straight through the pool with a guest order's id; no Go identifier of this package is what the test drives",
		"TestAwardLoyaltyPointsOnce":          "the unit is the SQL function award_loyalty_points, called straight through the pool three times for one order to hold that it awards once; no Go identifier of this package is what the test drives",
		"TestEraseUserRedeemer":               "the unit is the SQL function erase_user, called through the pool after Store.Redeem has made the redemption it erases; the test asserts what erasure leaves in the ledgers, not what Redeem does",
		"TestLoyaltyEntriesAppendOnly":        "the unit is the append-only rule of the loyalty_entries table, a SQL object: the test sends UPDATE and DELETE straight through the pool and expects both refused",
		"TestLoyaltyEntriesLapsedLot":         "the unit is the loyalty_entries constraints loyalty_entries_within_balance and loyalty_entries_lot_not_expired, reached through redeem_loyalty_points and a direct INSERT; both are SQL objects, not Go identifiers",
		"TestMoneyPostingDoors":               "a rule across the database's money posting functions (post_store_credit, spend_store_credit, grant_store_credit, compensate_return_with_credit, redeem_loyalty_points, award_loyalty_points, reverse_return_points, hold_inventory): which role may execute each and which constraint refuses each bad call; they are SQL objects, not identifiers of this package",
		"TestRedeemLoyaltyPointsAcrossLots":   "the unit is the SQL function redeem_loyalty_points, called straight through the pool to hold how it allocates one spend across lots; Store.History is read only to show the spend as one grouped row",
		"TestRedeemLoyaltyPointsLotOrder":     "the unit is the SQL function redeem_loyalty_points, called straight through the pool to hold which of two same-day lots it spends first; no Go identifier of this package is what the test drives",
		"TestRedeemLoyaltyPointsRace":         "the unit is the SQL function redeem_loyalty_points, raced from eight goroutines through the pool to hold that the account lock serialises spends; no Go identifier of this package is what the test drives",
		"TestReverseReturnPointsPostingOrder": "the unit is the SQL function reverse_return_points (with return_loyalty_points_allocation), driven through the pool across two interleaved settlements; no Go identifier of this package is what the test drives",
		"TestReverseReturnPointsRemainder":    "the unit is the SQL function reverse_return_points, called straight through the pool after award_loyalty_points and redeem_loyalty_points; no Go identifier of this package is what the test drives",
	},
	"internal/media": {
		"TestImage_SrcsetOfUpload":    "the unit is assets.Image.Srcset, which package assets declares; this package declares no identifier for it, and moving the test to assets is a reorganisation this pass does not do",
		"TestMediaObjectsConstraints": "the unit is the CHECK constraints of the media_objects table, a SQL object, not a Go identifier",
		"TestMediaObjectsImmutable":   "the unit is the media_objects_immutable trigger, a SQL object, not a Go identifier",
	},
	"internal/newsletter": {
		"TestEraseUserNewsletterList":       "the unit is the SQL function erase_user, called through the pool after Store.Request and Store.Confirm have put the address on the list; the test asserts what erasure leaves of the subscriber and its pending confirmation",
		"TestNewsletterSubscribersNoDelete": "the unit is the privilege of role store on the newsletter_subscribers table, a SQL object: the test sets the role and expects a DELETE refused with insufficient_privilege",
		"TestNewsletterTablesAdminReadOnly": "the unit is the privileges of role admin on the newsletter_subscribers and newsletter_confirmations tables, SQL objects: the test reads has_table_privilege for INSERT, UPDATE, DELETE and SELECT",
	},
	"internal/outbox": {
		"TestOutboxMessagesDedupeKey": "its unit is the unique index outbox_messages_dedupe_key, a SQL object, driven by the test's own INSERT ... ON CONFLICT helper, not by outbox.Enqueue",
		"TestRescheduleOutboxClock":   "its unit is the SQL statement RescheduleOutbox (a sqlc query, declared in internal/db), run directly in a transaction; Store.Drain is not what it exercises",
	},
	"internal/payment": {
		"TestCompletePaymentResolutionRoles": "the unit is the SQL functions record_complete_payment, attribute_complete_payment_paid and release_complete_payment, called straight under the store and admin roles to show which role may run which; no Go identifier of this package is what the test drives",
		"TestOpenPaymentRefusals":            "the unit is the SQL posting functions open_payment, capture_payment, record_expired_payment and record_complete_payment, called straight through the pool to bind the constraint each admission refusal names; Store.OpenPayment only sets the fixtures up",
		"TestRecordCompletePaymentPinsStock": "the unit is the SQL function record_complete_payment held against release_reservation through the order lock, with the back office's paid and unpaid outcomes and the lapse sweeper around it; no Go identifier of this package is what the test drives",
		"TestRecordExpiredPayment":           "the unit is the SQL function record_expired_payment (the expired-payment tombstone), called through the pool beside capture_payment; Store.OpenPayment only sets the fixtures up",
		"TestReleasePaymentEventRoles":       "the unit is the SQL functions release_payment_event and mark_payment_event_unreconciled and the grants on payment_webhook_events, driven under the store and admin roles; no Go identifier of this package is what the test drives",
		"TestUninvoicedOrdersAmountFiled":    "the unit is health.Store.UninvoicedOrders of package health, which this package does not declare; the test lives here for the capture fixtures, and moving it is a reorganisation this pass does not do",
		"TestUninvoicedOrdersLostDue":        "the unit is health.Store.UninvoicedOrders of package health, which this package does not declare; the test lives here for the capture and credit-funded fixtures, and moving it is a reorganisation this pass does not do",
	},
	"internal/policy": {
		"TestFAQSeedHold":          "the unit is the stock-hold answer of the FAQ seed in seed/dev_catalog.sql, read as text; the package declares no identifier for it",
		"TestFAQSeedInvoiceIssuer": "the unit is the invoice answer of the FAQ seed in seed/dev_catalog.sql, read as text and held against issuer.Gateway; the package declares no identifier for it",
		"TestFAQSeedPayment":       "the unit is the payment answer of the FAQ seed in seed/dev_catalog.sql, read as text; the package declares no identifier for it",
		"TestFAQSeedShopRules":     "the unit is the discount, registration and company-tax-ID answers of the FAQ seed in seed/dev_catalog.sql, read as text; the package declares no identifier for them",
	},
	"internal/product/copurchase": {
		"TestRefreshCopurchasesOverlap": "its unit is the SQL function refresh_copurchases, called directly through the pool; Store.Refresh is not what it exercises",
	},
	"internal/shipping/storemap": {
		"TestPickupNonceComparison": "a repository rule: no source file of this package or of internal/checkout compares a pickup nonce with == or !=; it reads those files, and no one declared identifier is its unit",
	},
	"internal/shoptime": {
		"TestAmbientFormat":   "its unit is ambientFormat, a helper declared in the guard's test file, not part of the package",
		"TestNoAmbientFormat": "a repository rule: no .Format( call outside this package renders a time in the process's zone; it reads every source file, and no function of the package is its unit",
	},
	"internal/site": {
		"TestPathOrFallback": "the unit is siteurl.PathOr of package siteurl, called with this feature's own fallback; this package declares no identifier the test calls, and moving it is a reorganisation this pass does not do",
	},
	"internal/ui/frame": {
		"TestDisclosuresWorkWithKeyboard": "its unit is the back office's content disclosures (the <details> behaviour of the desk stylesheet and goen.js), driven in a real browser by scripts/admin-disclosure-check.mjs against the health and twofactor pages; no identifier of this package declares them, and it uses frame.Page only as a fixture",
	},
	"internal/ui/icons": {
		"TestLineIconsAreSizedByClass": "the unit is the family of line icons: it renders Close, CheckCircle and Smartphone as a sample, so naming it after one of them would claim that icon alone",
	},
	"internal/wishlist": {
		"TestPathOrFallback": "the unit is siteurl.PathOr of package siteurl, called with this feature's own fallback; this package declares no identifier the test calls, and moving it is a reorganisation this pass does not do",
	},
}

// TestMigratedTestNames holds every package in migratedTestPackages to the rule
// that a top-level test name is at most 40 characters after "Test" and starts
// with the identifier of a function or type the package declares. The second
// part is checked only for a package that has non-test Go files, because only
// there is there an identifier to compare with; a package of guards has none
// and is held to length alone. Whether the identifier is the unit the test
// really exercises is left to review.
//
// It is not a count rule: it asks for no number of tests and no coverage.
func TestMigratedTestNames(t *testing.T) {
	t.Parallel()
	if len(migratedTestPackages) == 0 {
		t.Log("no package is listed in migratedTestPackages yet, so nothing is checked")
	}
	for _, problem := range checkTestNames(repoRoot(t), migratedTestPackages, testNameExceptions) {
		t.Error(problem)
	}
}

// checkTestNames returns what is wrong with the test names of each package in
// pkgs, and with the exceptions that do not hold.
func checkTestNames(root string, pkgs []string, exceptions map[string]map[string]string) []string {
	var problems []string
	listed := map[string]bool{}
	for _, pkg := range pkgs {
		if listed[pkg] {
			problems = append(problems, pkg+" is listed twice in migratedTestPackages")
			continue
		}
		listed[pkg] = true
		problems = append(problems, checkPackageTestNames(root, pkg, exceptions[pkg])...)
	}
	for _, pkg := range slices.Sorted(maps.Keys(exceptions)) {
		if !listed[pkg] {
			problems = append(problems, pkg+" has rows in testNameExceptions but is not in migratedTestPackages: delete them")
		}
	}
	return problems
}

func checkPackageTestNames(root, pkg string, exceptions map[string]string) []string {
	tests, idents, err := scanTestPackage(filepath.Join(root, filepath.FromSlash(pkg)))
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", pkg, err)}
	}
	if len(tests) == 0 {
		// A scan that finds nothing is asserting nothing: the walk has stopped
		// matching, or the package was listed before it had tests.
		return []string{pkg + " is listed in migratedTestPackages but no top-level test was found in it"}
	}
	var problems []string
	for _, name := range slices.Sorted(maps.Keys(exceptions)) {
		switch {
		case strings.TrimSpace(exceptions[name]) == "":
			problems = append(problems, fmt.Sprintf("%s: the exception for %s gives no reason", pkg, name))
		case !slices.Contains(tests, name):
			problems = append(problems, fmt.Sprintf("%s: the exception for %s is stale: no such test any more; delete the row", pkg, name))
		case testNameFault(name, idents) == "":
			problems = append(problems, fmt.Sprintf("%s: the exception for %s is stale: the name now follows the rule; delete the row", pkg, name))
		}
	}
	for _, name := range tests {
		if fault := testNameFault(name, idents); fault != "" {
			if _, excepted := exceptions[name]; !excepted {
				problems = append(problems, fmt.Sprintf("%s: %s %s.\nName the test after the unit it exercises and move the sentence into its doc comment (CONTRIBUTING.md, Tests).", pkg, name, fault))
			}
		}
	}
	return problems
}

// testNameFault says how name breaks the rule, or returns "" when it does not.
// idents holds the package's own top-level identifiers with their first letter
// upper-cased, or is empty when the package has no non-test file.
func testNameFault(name string, idents map[string]bool) string {
	rest := strings.TrimPrefix(name, "Test")
	var faults []string
	if len(rest) > maxTestNameLen {
		faults = append(faults, fmt.Sprintf("has %d characters after Test, over the limit of %d", len(rest), maxTestNameLen))
	}
	if len(idents) > 0 && !startsWithIdent(rest, idents) {
		faults = append(faults, "does not start with the name of a function or type the package declares")
	}
	return strings.Join(faults, ", and ")
}

// startsWithIdent reports whether rest, up to its first underscore, begins with
// one of idents ending at a word boundary: TestParseQuantity begins with Parse,
// TestParsed does not.
func startsWithIdent(rest string, idents map[string]bool) bool {
	head, _, _ := strings.Cut(rest, "_")
	for i := 1; i <= len(head); i++ {
		if !idents[head[:i]] {
			continue
		}
		if i == len(head) {
			return true
		}
		if next, _ := utf8.DecodeRuneInString(head[i:]); !unicode.IsLower(next) {
			return true
		}
	}
	return false
}

// scanTestPackage parses every Go file in dir whatever its build tags and
// returns the names of its top-level tests (TestMain is not one) and the
// package's own top-level identifiers.
func scanTestPackage(dir string) (tests []string, idents map[string]bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read the package directory: %w", err)
	}
	idents = map[string]bool{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", entry.Name(), parseErr)
		}
		isTest := strings.HasSuffix(entry.Name(), "_test.go")
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil {
					continue
				}
				if isTest {
					if isTestName(d.Name.Name) {
						tests = append(tests, d.Name.Name)
					}
				} else {
					addIdent(idents, d.Name.Name)
				}
			case *ast.GenDecl:
				if isTest {
					continue
				}
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						addIdent(idents, s.Name.Name)
					case *ast.ValueSpec:
						for _, n := range s.Names {
							addIdent(idents, n.Name)
						}
					}
				}
			}
		}
	}
	slices.Sort(tests)
	return slices.Compact(tests), idents, nil
}

// isTestName reports whether a func name is a test: Test followed by something
// that does not start with a lower-case letter, as go test requires. TestMain
// runs the package and is not a test.
func isTestName(name string) bool {
	rest, ok := strings.CutPrefix(name, "Test")
	if !ok || rest == "" || name == "TestMain" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLower(first)
}

func addIdent(idents map[string]bool, name string) {
	if name == "_" || name == "init" || name == "main" {
		return
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	idents[string(r)] = true
}

// TestCheckTestNames runs the guard's checks over fixture packages: each fault
// the guard exists to catch has a case that must report it, and each way the
// check could pass without looking has one that must fail.
func TestCheckTestNames(t *testing.T) {
	t.Parallel()
	const source = "package shop\n\nfunc Parse() {}\n\ntype Store struct{}\n\nfunc (Store) Decide() {}\n"
	const long = "TestParseEveryRowOfTheCustomersCartAndKeepsGoing"
	body := func(names ...string) string {
		var b strings.Builder
		b.WriteString("package shop_test\n\nimport \"testing\"\n")
		for _, n := range names {
			fmt.Fprintf(&b, "\nfunc %s(t *testing.T) {}\n", n)
		}
		return b.String()
	}
	for _, tc := range []struct {
		name       string
		files      map[string]string
		exceptions map[string]string
		want       []string // substrings, one per problem; nil means none
	}{
		{
			name:  "FollowsTheRule",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestParse", "TestParseQuantity", "TestStore_Decide", "TestMain")},
		},
		{
			name:  "BoundaryIsAWord",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestParsed")},
			want:  []string{"TestParsed does not start with the name"},
		},
		{
			name:  "TooLong",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestParse", long)},
			want:  []string{long + " has 44 characters after Test"},
		},
		{
			name:  "FortyIsAllowed",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestParse" + strings.Repeat("X", 35))},
		},
		{
			name:  "FortyOneIsNot",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestParse" + strings.Repeat("X", 36))},
			want:  []string{"has 41 characters after Test"},
		},
		{
			name:  "NoUnit",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestEveryRowIsKept")},
			want:  []string{"TestEveryRowIsKept does not start with the name"},
		},
		{
			name:  "UnexportedUnit",
			files: map[string]string{"shop.go": "package shop\n\nfunc roundUp() {}\n", "shop_test.go": body("TestRoundUp")},
		},
		{
			name:  "TaggedFileIsRead",
			files: map[string]string{"shop.go": source, "shop_integration_test.go": "//go:build integration\n\n" + body("TestParse", long)},
			want:  []string{long},
		},
		{
			name:  "OnlyTestsLengthOnly",
			files: map[string]string{"guard_test.go": body("TestDependencies", "TestEveryRowIsKept")},
		},
		{
			name:       "ExceptionHolds",
			files:      map[string]string{"shop.go": source, "shop_test.go": body("TestParse", long)},
			exceptions: map[string]string{long: "a rule of the whole repository"},
		},
		{
			name:       "ExceptionWithoutReason",
			files:      map[string]string{"shop.go": source, "shop_test.go": body("TestParse", long)},
			exceptions: map[string]string{long: " "},
			want:       []string{"the exception for " + long + " gives no reason"},
		},
		{
			name:       "ExceptionForARenamedTest",
			files:      map[string]string{"shop.go": source, "shop_test.go": body("TestParse")},
			exceptions: map[string]string{long: "a rule of the whole repository"},
			want:       []string{"the exception for " + long + " is stale: no such test"},
		},
		{
			name:       "ExceptionForANameThatNowFits",
			files:      map[string]string{"shop.go": source, "shop_test.go": body("TestParse", "TestParseQuantity")},
			exceptions: map[string]string{"TestParseQuantity": "was long once"},
			want:       []string{"the exception for TestParseQuantity is stale: the name now follows the rule"},
		},
		{
			name:  "NoTests",
			files: map[string]string{"shop.go": source, "shop_test.go": "package shop_test\n"},
			want:  []string{"no top-level test was found"},
		},
		{
			name:  "OnlyTestMain",
			files: map[string]string{"shop.go": source, "main_integration_test.go": body("TestMain")},
			want:  []string{"no top-level test was found"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "internal", "shop")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var exceptions map[string]map[string]string
			if tc.exceptions != nil {
				exceptions = map[string]map[string]string{"internal/shop": tc.exceptions}
			}
			got := checkTestNames(root, []string{"internal/shop"}, exceptions)
			if len(got) != len(tc.want) {
				t.Fatalf("checkTestNames(%q) = %q, want %d problems containing %q", tc.name, got, len(tc.want), tc.want)
			}
			for i, want := range tc.want {
				if !strings.Contains(got[i], want) {
					t.Errorf("checkTestNames(%q)[%d] = %q, want it to contain %q", tc.name, i, got[i], want)
				}
			}
		})
	}
}

// TestCheckTestNamesListing covers what a list can get wrong: a package that is
// not there, one listed twice, and exceptions for a package that is not listed.
func TestCheckTestNamesListing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "shop")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shop_test.go"), []byte("package shop_test\n\nimport \"testing\"\n\nfunc TestParse(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := checkTestNames(root, nil, nil); len(got) != 0 {
		t.Errorf("checkTestNames(root, nil, nil) = %q, want none: the empty list is allowed", got)
	}
	for _, tc := range []struct {
		name       string
		pkgs       []string
		exceptions map[string]map[string]string
		want       string
	}{
		{"Missing", []string{"gone"}, nil, "gone: read the package directory"},
		{"Twice", []string{"shop", "shop"}, nil, "shop is listed twice"},
		{"Unlisted", []string{"shop"}, map[string]map[string]string{"other": {"TestX": "why"}}, "other has rows in testNameExceptions but is not in migratedTestPackages"},
	} {
		got := checkTestNames(root, tc.pkgs, tc.exceptions)
		if len(got) != 1 || !strings.Contains(got[0], tc.want) {
			t.Errorf("checkTestNames(%q) = %q, want one problem containing %q", tc.name, got, tc.want)
		}
	}
}
