package repocheck_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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

// minTestPackages is the floor under the walk: main has 95 directories holding
// a _test.go file, so a walk that finds far fewer has stopped looking.
const minTestPackages = 90

// testNameExceptions is the shrink-only allow list: for a package, each
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
	"cmd/goen": {
		"TestCookieNames":               "a rule across every cookie goen sets: it probes the cookie writers of several internal packages (auth, cart, i18n, order/access among them) and walks the production source for the names, so no one identifier of package main is its unit",
		"TestSellerVariablesDocumented": "a rule about two files a deployer reads, .env.example and deploy/demo/manifest.env: it exercises no function, so no identifier of package main is its unit",
		"TestSpeculationRulesCatalogue": "its unit is the speculation-rules document assets/speculation/rules.json, read from disk, which is a file and not a Go identifier",
		"TestSpeculationRulesRefusals":  "its unit is the speculation-rules document assets/speculation/rules.json, read from disk, which is a file and not a Go identifier",
		"TestStorefrontPatterns":        "its unit is storefrontPatterns, the route walk declared in routeinventory_test.go, not part of the package",
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
	"internal/auth": {
		"TestCartStore_AdoptClampsQuantity": "its unit is cart.Store.Adopt, declared in package cart and called directly; the test sits here only because it registers its accounts through this package's Store, and no identifier of package auth is its unit",
		"TestCartStore_AdoptConcurrent":     "its unit is cart.Store.Adopt, declared in package cart and called directly from two accounts at once; the test sits here only because it registers its accounts through this package's Store, and no identifier of package auth is its unit",
		"TestCartStore_AdoptForeignCart":    "its unit is cart.Store.Adopt, declared in package cart and called directly with a cart another account owns; the test sits here only because it registers its accounts through this package's Store, and no identifier of package auth is its unit",
		"TestCartStore_AdoptInactive":       "its unit is cart.Store.Adopt, declared in package cart and called directly; the test sits here only because it registers its accounts through this package's Store, and no identifier of package auth is its unit",
		"TestCartStore_AdoptMerges":         "its unit is cart.Store.Adopt, declared in package cart and called directly; the test sits here only because it registers its accounts through this package's Store, and no identifier of package auth is its unit",
		"TestCartStore_AdoptUnavailable":    "its unit is cart.Store.Adopt, declared in package cart and called directly; the test sits here only because it registers its accounts through this package's Store, and no identifier of package auth is its unit",
	},
	"internal/backinstock": {
		"TestSweepNotifiedStockNotificationsRetention": "the unit is the SQL function sweep_notified_stock_notifications, called straight through the maintenance pool with a zero and a negative retention; Store.Sweep is not what it exercises",
	},
	"internal/cart": {
		"TestOrderBelongsToEmailErased":             "the unit is Store.OrderBelongsToEmail of package buyer, which this package does not declare; the test sits here for the order fixtures, and moving it is a reorganisation this pass does not do",
		"TestOrderBelongsToEmailMatch":              "the unit is Store.OrderBelongsToEmail of package buyer, which this package does not declare; the test sits here for the order fixtures, and moving it is a reorganisation this pass does not do",
		"TestOrderBelongsToEmailWrong":              "the unit is Store.OrderBelongsToEmail of package buyer, which this package does not declare; the test sits here for the order fixtures, and moving it is a reorganisation this pass does not do",
		"TestOrderRoutesSignedInGuest":              "the unit is five order routes of three packages at once (buyer OrderPage, ReturnPage and CancelOrder, payment Page, and this package's ReorderItems), each asked for the same recovery actions; ReorderItems is one of the five, so no identifier of this package is its unit",
		"TestOrderTimelineRefundID":                 "the unit is Store.Order of package buyer (the Timeline it returns), which this package does not declare; the test sits here for the cancelled-order fixture, and moving it is a reorganisation this pass does not do",
		"TestProductReviewsVerifiedIsReal":          "the unit is the product_reviews_verified_is_real constraint of the product_reviews table, a SQL object the test provokes by claiming the verified badge for a cancelled order, not a Go identifier",
		"TestProductVariantsRestockNoticesLastHold": "the unit is the product_variants_restock_notices trigger, a SQL object, fired by releasing the last hold through Sweeper.Sweep of package order/lapse and Handler.CancelOrder of package buyer; this package declares neither",
		"TestReleaseReservationFundedOrder":         "the unit is the SQL function release_reservation, called straight through the pool on a fully credit-funded order's hold; no Go identifier of this package is what the test drives",
	},
	"internal/catalog": {
		"TestBuyBarObserver":         "the unit is the buyBar function of assets/js/goen.js, read as text because nothing here runs the script; the package declares no identifier for it",
		"TestRelatedProductsRanking": "the unit is the sqlc query RelatedProducts of query.sql, called straight through db.Queries on fixtures of its own; the package declares no identifier for it",
		"TestSeedReviews":            "the unit is the product_reviews rows that seed/dev_catalog.sql loads, read through SQL; the package declares no identifier for them",
		"TestStepperFocusScript":     "the unit is the quantity stepper of assets/js/goen.js, read as text because nothing here runs the script; the package declares no identifier for it",
	},
	"internal/checkout": {
		"TestAccessStore_GrantSecondOrder":             "the unit is access.Store.Grant of package order/access, a different type from this package's Store; the test lives here for the orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestAccessStore_GrantStaleCarried":            "the unit is access.Store.Grant of package order/access, a different type from this package's Store; the test lives here for the orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_FindOrderAddressBound":       "the unit is buyer.Handler.FindOrder of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_FindOrderFullWidth":          "the unit is buyer.Handler.FindOrder of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_FindOrderGrantFailure":       "the unit is buyer.Handler.FindOrder of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_FindOrderRandomNumber":       "the unit is buyer.Handler.FindOrder of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_OrderPageEnumeration":        "the unit is buyer.Handler.OrderPage of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_OrderPageForgedCookie":       "the unit is buyer.Handler.OrderPage of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_OrderPageOtherOrder":         "the unit is buyer.Handler.OrderPage of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerHandler_OrderPagePaymentReturn":      "the unit is buyer.Handler.OrderPage of package buyer, a different type from this package's Handler; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_CancelOrderNoVoidDue":          "the unit is buyer.Store.CancelOrder of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
		"TestBuyerStore_CancelOrderReversal":           "the unit is buyer.Store.CancelOrder of package buyer, a different type from this package's Store; the test lives here for the checkout fixtures it places orders with, and moving it is a reorganisation this pass does not do",
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
		"TestOrderAccessibleWithRetain":                "the unit is the OrderAccessibleWith query (a sqlc statement declared in internal/db), called through db.Queries inside one transaction, not a Go identifier of this package",
		"TestOrderLinesWrittenWhilePlacing":            "the unit is the order_lines_written_while_placing constraint (with inventory_hold_within_order_lines and coupon_redemptions_written_while_placing, which it also asserts), SQL objects driven as the store role against an order a committed checkout placed; this package declares no identifier for them",
		"TestOrderLinesWrittenWhilePlacingRestored":    "the unit is the order_lines_written_while_placing constraint, a SQL object driven as the store role against an order a committed checkout placed; this package declares no identifier for it",
		"TestOrderLinesWrittenWhilePlacingSameInstant": "the unit is the order_lines_written_while_placing constraint, a SQL object driven as the store role against an order a committed checkout placed; this package declares no identifier for it",
		"TestParseDestination":                         "the unit is shipping.ParseDestination of package shipping, called directly; this package declares no identifier for it, and moving the test is a reorganisation this pass does not do",
		"TestReleaseReservationLockOrder":              "the unit is the SQL function release_reservation, called directly through the pool; this package declares no identifier for it",
		"TestReturnWindowEnds":                         "the unit is the SQL functions return_window_ends and return_line_policy_window, called as the store role; this package declares no identifier for them",
		"TestReverseOrderLedgerIdempotent":             "the unit is the SQL function reverse_order_credit, called directly through the pool on an order the test's own fixture built; this package declares no identifier for it",
		"TestReverseOrderLedgerShipped":                "the unit is the SQL function reverse_order_credit, called directly through the pool on an order the test's own fixture built; this package declares no identifier for it",
		"TestSessionSweeper_SweepLateExpiryRace":       "the unit is payment.SessionSweeper.Sweep of package payment, which this package does not declare; the test lives here for the part-credit orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestSessionSweeper_SweepLostExpiry":           "the unit is payment.SessionSweeper.Sweep of package payment, which this package does not declare; the test lives here for the part-credit orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestSessionSweeper_SweepNotLostExpiry":        "the unit is payment.SessionSweeper.Sweep of package payment, which this package does not declare; the test lives here for the part-credit orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestSessionSweeper_SweepTwice":                "the unit is payment.SessionSweeper.Sweep of package payment, which this package does not declare; the test lives here for the part-credit orders checkout places, and moving it is a reorganisation this pass does not do",
		"TestStripeReturnPages":                        "the unit is the two pages Stripe sends a customer back to, buyer.Handler.OrderPage and payment.Handler.Page, held to one cross-site reload rule; both are other packages' handlers, and no identifier of this package is its unit",
	},
	"internal/db": {
		"TestActorNamedByEmail":                          "a repository rule over every query.sql file (no statement names an event's actor by users.full_name); no Go identifier of this package is its unit",
		"TestAdminCannotSetStockQuantity":                "the unit is the privilege of role admin on the product_variants.stock_quantity column, a SQL object, not a Go identifier",
		"TestAdminCannotWriteMoneyOrStockDirectly":       "the unit is the privileges of role admin on the money and stock tables, SQL objects, not Go identifiers",
		"TestAdminCannotWriteStaffColumns":               "the unit is the column privileges of role admin on users, SQL objects, not Go identifiers",
		"TestAdminContactMessagesHandledAt":              "the unit is the column privileges of role admin on contact_messages, SQL objects, not Go identifiers",
		"TestAdminHasNoDirectWriteToMoney":               "the unit is the privileges of role admin on the money tables, SQL objects, not Go identifiers",
		"TestAdminIsNotASuperuser":                       "the unit is the attributes of role admin, a SQL object, not a Go identifier",
		"TestAppendOnlyTablesDenyUpdateDelete":           "the unit is the privileges of role store on the append-only tables, SQL objects, not Go identifiers",
		"TestCampaignViewsRunningDeals":                  "the units are the SQL views running_campaigns, campaign_deals and listed_campaigns, read through the pool; no Go identifier of this package is the view",
		"TestCheckConstraintNamesAreUnique":              "the unit is the catalogue of CHECK constraint names, SQL objects, not a Go identifier",
		"TestCheckConstraintsAccept":                     "the unit is the set of CHECK constraints, SQL objects, not a Go identifier",
		"TestCheckConstraintsExercised":                  "the unit is the set of CHECK constraints in the catalogue, SQL objects, not a Go identifier",
		"TestCheckConstraintsReject":                     "the unit is the set of CHECK constraints, SQL objects, not a Go identifier",
		"TestCheckFunctionHeaders":                       "the unit is checkFunctionHeaders, a helper declared in a test file of this package, so the package declares no identifier for it",
		"TestColumnExemptionsNotStale":                   "the unit is columnExemptions, a table declared in a test file of this package, so the package declares no identifier for it",
		"TestColumnWrites":                               "the unit is columnWrites, a helper declared in a test file of this package, so the package declares no identifier for it",
		"TestColumnsReadOrWritten":                       "a repository rule over every column of the schema and every SQL statement; no Go identifier of this package is its unit",
		"TestCommittedOrdersExists":                      "a repository rule over the SQL files (how committed_orders is queried); the view is SQL and no Go identifier of this package is the unit",
		"TestContactMessagesSubjectKnown":                "the unit is the CHECK contact_messages_subject_known held to contact.Choices; the constraint is SQL and no identifier of this package names it",
		"TestDefinerWrittenTables":                       "the unit is definerWrittenTables, a helper declared in a test file of this package, so the package declares no identifier for it",
		"TestDefinerWrittenTablesRevoked":                "the unit is the privileges on the tables the SECURITY DEFINER functions write, SQL objects, not Go identifiers",
		"TestDemoBackdatingSetsOnlyTimes":                "the unit is the seed script seed/demo_backdating.sql, a SQL file, not a Go identifier",
		"TestDemoCatalogueIntentAccepted":                "the unit is the seed script seed/dev_catalog.sql, a SQL file, not a Go identifier",
		"TestDemoCatalogueIntentRequired":                "the unit is the seed script seed/dev_catalog.sql, a SQL file, not a Go identifier",
		"TestDemoCatalogueLoadsIntoSchemaOnlyShop":       "the unit is the seed script seed/dev_catalog.sql, a SQL file, not a Go identifier",
		"TestDemoCatalogueRequiresNoProductsOrOrders":    "the unit is the seed script seed/dev_catalog.sql, a SQL file, not a Go identifier",
		"TestDemoHistoryEarlierOrders":                   "the unit is the demo history seed script, run through psql; it is a SQL file, not a Go identifier",
		"TestDemoHistoryInvariants":                      "the unit is the demo history seed script, run through psql; it is a SQL file, not a Go identifier",
		"TestDemoHistoryRefuses":                         "the unit is the demo history seed script, run through psql; it is a SQL file, not a Go identifier",
		"TestDemoShiftMovesOnlyTimes":                    "the unit is the seed script seed/demo_shift_columns.sql, a SQL file, not a Go identifier",
		"TestDemoShiftRefuses":                           "the unit is the seed script seed/demo_shift.sql, a SQL file, not a Go identifier",
		"TestDemoShiftRefusesSecondRun":                  "the unit is the seed script seed/demo_shift.sql, a SQL file, not a Go identifier",
		"TestDemoShiftRestoresNextDay":                   "the unit is the seed script seed/demo_shift.sql, a SQL file, not a Go identifier",
		"TestDemoShiftShippingFromMidnight":              "the unit is the seed script seed/demo_shift.sql, a SQL file, not a Go identifier",
		"TestDynamicDefiners":                            "the unit is dynamicDefiners, a helper declared in a test file of this package, so the package declares no identifier for it",
		"TestEveryCreditBalanceReadsTheOneView":          "a repository rule over how SQL reads store_credit_balances; the name stays because a comment in internal/order/buyer/query.sql cites it and this pass does not touch SQL, so the rename waits for the change that retargets the comment",
		"TestEveryOrderTotalReadsTheOneView":             "a repository rule over how SQL reads order_totals; no Go identifier of this package is its unit. The name stays because a comment in migrations/001 cites it and 001 cannot be edited without changing its schema digest, so the rename waits for the 001 change that retargets the comment",
		"TestEveryRoleCanRunItsOwnQueries":               "the unit is the grants of each role held to the queries its pool runs; no Go identifier of this package is its unit. The name stays because a comment in migrations/001 cites it and 001 cannot be edited without changing its schema digest, so the rename waits for the 001 change that retargets the comment",
		"TestForeignKeysComplete":                        "the unit is the set of foreign keys in the catalogue, SQL objects, not a Go identifier",
		"TestForeignKeysIndexed":                         "the unit is the foreign keys and indexes in the catalogue, SQL objects, not Go identifiers",
		"TestFunctionExecuteMatrix":                      "the unit is the EXECUTE grants on the stored functions held to the production queries, SQL objects, not Go identifiers",
		"TestFunctionSearchPathPgTemp":                   "the unit is the search_path setting of every stored function, a SQL object, not a Go identifier",
		"TestFunctionSearchPathTempShadow":               "the unit is the search_path pin of the stored functions, a SQL object, not a Go identifier",
		"TestFunctionsNotPublicExecute":                  "the unit is the EXECUTE privilege of every stored function, a SQL object, not a Go identifier",
		"TestGeneratedQueryProductionCaller":             "a repository rule over the generated query methods and the production Go files; no Go identifier of this package is its unit",
		"TestGoStringLiterals":                           "the unit is goStringLiterals, a helper declared in a test file of this package, so the package declares no identifier for it",
		"TestGrantsAfterRoleCreation":                    "the unit is the order of statements in migration 001, a SQL file, not a Go identifier",
		"TestHardenSchemaForeignRole":                    "the unit is the SQL function harden_schema, not a Go identifier",
		"TestHardenSchemaLaterFunction":                  "the unit is the SQL function harden_schema and the default privileges of the schema, not a Go identifier",
		"TestHoldInventoryOrderBound":                    "the unit is the SQL function hold_inventory, run as role store; no Go identifier of this package declares it",
		"TestHoldInventoryRefusesSettledOrders":          "the unit is the SQL function hold_inventory, called straight through the pool; no Go identifier of this package declares it",
		"TestInitialDownConcurrentRow":                   "the unit is migration 001.down, a SQL file, not a Go identifier",
		"TestInitialDownRefusesData":                     "the unit is migration 001.down, a SQL file, not a Go identifier",
		"TestInitialSchemaLibcCollation":                 "the unit is migration 001, applied to a database of its own; it is a SQL file, not a Go identifier",
		"TestInvoiceUnitDomain":                          "the unit is the invoice-unit domain and its constraint invoice_unit_valid, SQL objects, not Go identifiers",
		"TestKnownDevicesSignInOnly":                     "the unit is the privileges of three roles on the known_devices table, SQL objects, not Go identifiers",
		"TestLocaleParamAssigned":                        "a repository rule over the call sites of the generated localized queries; no Go identifier of this package is its unit",
		"TestLocalizedNameCategory":                      "the unit is the SQL function localized_name as every query uses it, a repository rule over query.sql; no Go identifier of this package is its unit",
		"TestLocalizedNameOption":                        "the unit is the SQL function localized_name as every query uses it, a repository rule over query.sql; no Go identifier of this package is its unit",
		"TestLocalizedNameProduct":                       "the unit is the SQL function localized_name as every query uses it, a repository rule over query.sql; no Go identifier of this package is its unit",
		"TestLocalizedNameSpec":                          "the unit is the SQL function localized_name as every query uses it, a repository rule over query.sql; no Go identifier of this package is its unit",
		"TestLoyaltyPolicyFunctions":                     "the unit is the SQL functions membership_window_days and points_lifetime_days and the award path that reads them; no Go identifier of this package names them",
		"TestMemberSpendOneView":                         "the unit is a repository rule over how SQL reads committed_order_spend and membership_tier_for; no Go identifier of this package is its unit",
		"TestMigrationFunctionHeaders":                   "a repository rule over the migration files; no Go identifier of this package is its unit",
		"TestMoneyColumnCeiling":                         "the unit is the CHECKs on every money column of the schema, SQL objects held to money.MaxCents; no Go identifier of this package is its unit",
		"TestNoRoleHoldsAColumnWriteItsQueriesNeverMake": "the unit is the column privileges of each role held to its production statements, SQL objects, not Go identifiers; the name is also 42 characters after Test. It stays because a comment in migrations/001 cites it and 001 cannot be edited without changing its schema digest, so the rename waits for the 001 change that retargets the comment",
		"TestNoRoleHoldsAWriteItsQueriesNeverMake":       "the unit is the table privileges of each role held to its production statements, SQL objects, not Go identifiers. The name stays because a comment in migrations/001 cites it and 001 cannot be edited without changing its schema digest, so the rename waits for the 001 change that retargets the comment",
		"TestOnlyApplicationRolesConnect":                "the unit is the CONNECT privilege on the database and the application roles, SQL objects, not Go identifiers",
		"TestOrdersStorefrontCancelsOnly":                "the unit is the trigger orders_storefront_cancels_only and its grants, SQL objects, not Go identifiers",
		"TestPickupChainKnown":                           "the unit is the CHECK order_private_data_pickup_chain_known held to shipping.Chains; the constraint is SQL and no identifier of this package names it",
		"TestPoolMapComplete":                            "the unit is the pool maps declared in a test file of this package and the directory tree, a repository rule; no Go identifier of this package declares them",
		"TestQueryCalledByOwner":                         "a repository rule over the generated queries and the production Go files; no Go identifier of this package is its unit",
		"TestRaisedRulesAsserted":                        "the unit is the rules raised in the bodies of the catalogue's functions, SQL objects, not a Go identifier",
		"TestRefundsCannotRacePastCapture":               "the unit is the refund-against-capture rule of the refunds table under concurrency, a SQL object, not a Go identifier",
		"TestReportingCannotReadCredentialsOrPII":        "the unit is the column privileges of role reporting, SQL objects, not Go identifiers; the name stays because a comment in migrations/001 cites it and 001 cannot be edited without changing its schema digest (TestFrozenMigrationsAreUnchanged), so the rename waits for the 001 change that retargets the comment",
		"TestReservationLockOrder":                       "the unit is the lock order inside the reservation SQL functions, read from their source; no Go identifier of this package is its unit",
		"TestRestockSweepMaintenanceOnly":                "the unit is the EXECUTE grant on the SQL function sweep_notified_stock_notifications, not a Go identifier",
		"TestReturnRequestsClaimFrozen":                  "the unit is the trigger return_requests_claim_frozen and the grants on return_requests, SQL objects, not Go identifiers",
		"TestReturnWindows":                              "the unit is the SQL functions rescission_days and return_goodwill_days; no Go identifier of this package names them",
		"TestRoleReadsItsQueries":                        "the unit is the grants of each role on the views and tables its queries read, SQL objects, not Go identifiers",
		"TestRolesCannotWriteFixedColumns":               "the unit is the column privileges of roles store and admin, SQL objects, not Go identifiers",
		"TestRolesCannotWriteStockQuantity":              "the unit is the column privileges of three roles on product_variants.stock_quantity, SQL objects, not Go identifiers",
		"TestRuleTriggersExercised":                      "the unit is the set of rule triggers in the catalogue, SQL objects, not a Go identifier",
		"TestRulesAccept":                                "the unit is the set of rule triggers in the catalogue, SQL objects, not a Go identifier",
		"TestRulesReject":                                "the unit is the set of rule triggers in the catalogue, SQL objects, not a Go identifier",
		"TestRunExemptionsNotStale":                      "the unit is runExemptions, a table declared in a test file of this package, so the package declares no identifier for it",
		"TestRunExemptionsNotUnused":                     "the unit is runExemptions, a table declared in a test file of this package, so the package declares no identifier for it",
		"TestSecurityDefinerSet":                         "the unit is the set of SECURITY DEFINER functions in the catalogue, SQL objects, not a Go identifier",
		"TestSetUpdatedAt":                               "the unit is the trigger function set_updated_at, a SQL object, not a Go identifier",
		"TestShopDayCalendar":                            "a repository rule over the SQL files (how shop_day and shop_today are used); no Go identifier of this package is its unit",
		"TestStockCannotOversell":                        "the unit is the stock rule of product_variants under concurrent holds, a SQL object, not a Go identifier",
		"TestStoreCannotDeleteUsers":                     "the unit is the privileges of role store on the users table, SQL objects, not Go identifiers",
		"TestStoreCannotDeleteWebhookEvents":             "the unit is the privilege of role store on payment_webhook_events, a SQL object, not a Go identifier",
		"TestStoreCannotDisableTriggers":                 "the unit is the privilege of role store to set session_replication_role, a SQL object, not a Go identifier",
		"TestStoreCannotRecordMovement":                  "the unit is the EXECUTE privilege of role store on record_inventory_movement, a SQL object, not a Go identifier",
		"TestStoreCannotSetAnswerIsStaff":                "the unit is the privilege of role store on product_answers, a SQL object, not a Go identifier",
		"TestStoreCannotWriteOrDeleteProductAnswers":     "the unit is the privileges of role store on product_answers, SQL objects, not Go identifiers",
		"TestStoreHasNoTempPrivilege":                    "the unit is the privilege of role store on the database, a SQL object, not a Go identifier",
		"TestStoreInvoicePreferencesInsertOnly":          "the unit is the privileges of role store on invoice_preferences, SQL objects, not Go identifiers",
		"TestStoreIsNotSuperuser":                        "the unit is the attributes of role store, a SQL object, not a Go identifier",
		"TestSuiteDatabaseCollation":                     "the unit is the database dbtest creates (package dbtest, not this package), read from pg_database; no identifier of this package declares it",
		"TestTablesRead":                                 "the unit is every table of the schema held to the statements that read it; a repository rule, no Go identifier of this package is its unit",
		"TestTablesWritten":                              "the unit is every table of the schema held to the statements that write it; a repository rule, no Go identifier of this package is its unit",
		"TestTextBoundsMirrorTheirValidators":            "the unit is the length CHECKs of the schema held to Go validators in other packages; no Go identifier of this package is its unit",
		"TestTileQueriesColours":                         "the unit is the colours subquery copied into each tile query of query.sql files; no Go identifier of this package is its unit",
		"TestTileQueriesInCampaign":                      "the unit is the in_campaign predicate copied into each tile query of query.sql files; no Go identifier of this package is its unit",
		"TestTransactionsIn":                             "the unit is transactionsIn, a helper declared in a test file of this package, so the package declares no identifier for it",
		"TestTransactionsRolledBack":                     "a repository rule over every non-test Go file under internal and cmd; no Go identifier of this package is its unit",
		"TestUnboundedRollback":                          "the unit is unboundedRollback, a helper declared in a test file of this package, so the package declares no identifier for it",
		"TestUniqueConstraintsAccept":                    "the unit is the set of unique indexes, SQL objects, not a Go identifier",
		"TestUniqueConstraintsExercised":                 "the unit is the set of unique indexes in the catalogue, SQL objects, not a Go identifier",
		"TestUniqueConstraintsReject":                    "the unit is the set of unique indexes, SQL objects, not a Go identifier",
		"TestUnsentRefundDoorGrants":                     "the unit is the EXECUTE grants on the SQL functions open_unsent_refund and settle_unsent_refund, not Go identifiers",
		"TestUnsentRefundParcelRefused":                  "the unit is the constraint shipment_within_purchase as an unsent refund tightens it, a SQL object, not a Go identifier",
		"TestUnsentRefundPayout":                         "the unit is the unsent-refund path as a whole, open_unsent_refund through settle_unsent_refund and the order's completion, not one Go identifier",
		"TestUnsentRefundShippedReturnable":              "the unit is the unsent-refund and customer-return rules together over return_request_lines, SQL objects, not one Go identifier",
		"TestUnsentRefundThenReturnRest":                 "the unit is the unsent-refund path followed by a customer return, SQL functions across two doors, not one Go identifier",
		"TestUnsentRefundWriteDenied":                    "the unit is the privileges on return_request_lines and the unsent-refund doors, SQL objects, not Go identifiers",
		"TestUnusedFunctionDoorsStayRevoked":             "the unit is the EXECUTE privileges on the stored functions, SQL objects, not Go identifiers",
		"TestUnusedTableVerbsStayRevoked":                "the unit is the table privileges of the roles, SQL objects, not Go identifiers",
		"TestUsersTriggerKeepsOneAdmin":                  "the unit is the users triggers users_keep_one_admin_on_role and users_keep_one_admin_on_delete, SQL objects, not Go identifiers",
		"TestVariantStockLock":                           "the unit is the row locks the lock queries take on product_variants, SQL objects, not one Go identifier",
		"TestWindowFunctionGrants":                       "the unit is the EXECUTE grants on the SQL window functions, run as three roles; no Go identifier of this package names them",
		"TestWriteExemptionsNotStale":                    "the unit is writeExemptions, a table declared in a test file of this package, so the package declares no identifier for it",
		"TestWriteVerbTargetsRowLocks":                   "the unit is writeVerbTargets, a helper declared in a test file of this package, so the package declares no identifier for it",
		"TestWriteVerbTargetsUpserts":                    "the unit is writeVerbTargets, a helper declared in a test file of this package, so the package declares no identifier for it",
	},
	"internal/discount/campaign": {
		"TestImage_URLHeaderKey":        "the unit is picture.Image.URL and Srcset of package picture, which resolve a campaign's header key as they resolve a product image; this package declares no identifier for it, and moving the test to picture is a reorganisation this pass does not do",
		"TestSaleCampaignNeedsDiscount": "the unit is the sale_campaign_needs_discount trigger on sale_campaign_products, a SQL object the test provokes by featuring products through the pool, not a Go identifier",
	},
	"internal/home": {
		"TestHomeTilesCampaignRow":      "the unit is the HomeTiles query (internal/home/query.sql, generated into internal/db), called directly through db.Queries; Store.Load is not what it exercises",
		"TestStorefrontSectionsAsDrawn": "a browser measurement of the stylesheet as drawn on several pages (the home page, the deals page, search, the cart, a department, a product and the header) through their own renderers; the home page is only one of them, so no one declared identifier of this package is its unit",
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
		"TestMediaObjectsConstraints": "the unit is the CHECK constraints of the media_objects table, a SQL object, not a Go identifier",
		"TestMediaObjectsImmutable":   "the unit is the media_objects_immutable trigger, a SQL object, not a Go identifier",
	},
	"internal/newsletter": {
		"TestEraseUserNewsletterList":       "the unit is the SQL function erase_user, called through the pool after Store.Request and Store.Confirm have put the address on the list; the test asserts what erasure leaves of the subscriber and its pending confirmation",
		"TestNewsletterSubscribersNoDelete": "the unit is the privilege of role store on the newsletter_subscribers table, a SQL object: the test sets the role and expects a DELETE refused with insufficient_privilege",
		"TestNewsletterTablesAdminReadOnly": "the unit is the privileges of role admin on the newsletter_subscribers and newsletter_confirmations tables, SQL objects: the test reads has_table_privilege for INSERT, UPDATE, DELETE and SELECT",
	},
	"internal/order/buyer": {
		"TestReturnCreditRequiresLiveAccount": "the unit is the constraint trigger return_credit_requires_live_account, a SQL object; the test drives it through auth.Store.Erase and Store.OpenReturn at once to race a credit-funded return against account erasure, so no one identifier of package buyer is its unit",
		"TestReturnRefundableAmount":          "the unit is the SQL function return_refundable_amount; the test calls it directly after opening one request through Store.OpenReturn and inserting the other with SQL, and package buyer declares no identifier for it",
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
	"migrations": {
		"TestFrozenMigrations": "a repository rule: no migration file changes once it has merged, and none is added or removed without frozen saying so; it hashes every file of the directory and exercises no function, so no one declared identifier is its unit",
	},
}

// TestEveryTestNameFollowsTheRule holds every package with a _test.go file to
// the rule that a top-level test name is at most 40 characters after "Test" and
// starts with the identifier of a function or type the package declares. The
// second part is checked only for a package that has non-test Go files, because
// only there is there an identifier to compare with; a package of guards has
// none and is held to length alone. Whether the identifier is the unit the test
// really exercises is left to review.
//
// The packages are found by walking the repository (testPackages), so a new
// package is held to the rule from its first test; the walk must find at least
// minTestPackages of them.
//
// It is not a count rule: it asks for no number of tests and no coverage.
func TestEveryTestNameFollowsTheRule(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	pkgs, err := testPackages(root)
	if err != nil {
		t.Fatalf("walk the repository for test files: %v", err)
	}
	if len(pkgs) < minTestPackages {
		t.Fatalf("the walk found %d directories holding a _test.go file, want at least %d: it has stopped looking at the repository", len(pkgs), minTestPackages)
	}
	for _, problem := range checkTestNames(root, pkgs, testNameExceptions) {
		t.Error(problem)
	}
}

// testPackages returns, slash-separated and relative to root, every directory
// under root that holds a _test.go file.
func testPackages(root string) ([]string, error) {
	return goDirs(root, func(name string) bool { return strings.HasSuffix(name, "_test.go") })
}

// goDirs returns, slash-separated and relative to root, every directory under
// root that holds a file whose name keep accepts. It skips what the go command
// skips (skippedDir: names starting with "." or "_", such as the .worktrees and
// .claude copies of the repository a checkout may hold, testdata, and
// node_modules) and vendor.
func goDirs(root string, keep func(name string) bool) ([]string, error) {
	found := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skippedDir(d.Name()) || d.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !keep(d.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		found[filepath.ToSlash(rel)] = true
		return nil
	})
	return slices.Sorted(maps.Keys(found)), err
}

// checkTestNames returns what is wrong with the test names of each package in
// pkgs, and with the exceptions that do not hold.
func checkTestNames(root string, pkgs []string, exceptions map[string]map[string]string) []string {
	var problems []string
	for _, pkg := range pkgs {
		problems = append(problems, checkPackageTestNames(root, pkg, exceptions[pkg])...)
	}
	for _, pkg := range slices.Sorted(maps.Keys(exceptions)) {
		if !slices.Contains(pkgs, pkg) {
			problems = append(problems, pkg+" has rows in testNameExceptions but holds no _test.go file: delete them")
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
		// A scan that finds nothing is asserting nothing: the parse has stopped
		// matching, or the directory's test files hold only TestMain, helpers,
		// benchmarks, fuzz targets or examples.
		return []string{pkg + " holds a _test.go file but no top-level test was found in it"}
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
			pkgs, err := testPackages(root)
			if err != nil || !slices.Equal(pkgs, []string{"internal/shop"}) {
				t.Fatalf("testPackages(%q) = %q, %v, want [internal/shop]", tc.name, pkgs, err)
			}
			got := checkTestNames(root, pkgs, exceptions)
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

// TestTestPackagesWalk covers what the walk can get wrong: reading a copy of
// the repository or a fixture it should skip, and missing a nested package. It
// also covers exceptions for a package the walk did not find.
func TestTestPackagesWalk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const test = "package x_test\n\nimport \"testing\"\n\nfunc TestParse(t *testing.T) {}\n"
	for _, file := range []string{
		"shop/shop_test.go",
		"shop/cart/cart_test.go",
		"shop/cart/cart_integration_test.go",
		"shop/testdata/repo/repo_test.go",
		".worktrees/copy/shop/shop_test.go",
		".claude/worktrees/copy/shop/shop_test.go",
		"_scratch/scratch_test.go",
		"vendor/example.com/lib/lib_test.go",
		"node_modules/pkg/pkg_test.go",
	} {
		path := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(test), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "notests"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notests", "notests.go"), []byte("package notests\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkgs, err := testPackages(root)
	if want := []string{"shop", "shop/cart"}; err != nil || !slices.Equal(pkgs, want) {
		t.Fatalf("testPackages(root) = %q, %v, want %q", pkgs, err, want)
	}
	if got := checkTestNames(root, pkgs, nil); len(got) != 0 {
		t.Errorf("checkTestNames(root, %q, nil) = %q, want none", pkgs, got)
	}
	got := checkTestNames(root, pkgs, map[string]map[string]string{"other": {"TestX": "why"}})
	if want := "other has rows in testNameExceptions but holds no _test.go file"; len(got) != 1 || !strings.Contains(got[0], want) {
		t.Errorf("checkTestNames(root, %q, exceptions for other) = %q, want one problem containing %q", pkgs, got, want)
	}
}
