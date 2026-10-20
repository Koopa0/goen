package audit

import "errors"

// State is what an audit row records as a write's before or after: each field
// and its value, stored as one jsonb object. A nil State stores NULL.
type State map[Field]any

// Field is a key a [State] records. Only this package can make one, so a
// writer can record only a field declared below: a key typed in place, or a
// misspelt one, does not compile.
//
// Every key is also in the ledger of stored keys (fieldledger_test.go), which
// gives it a label on /admin/audit. audit_events is append-only, so a key
// keeps its spelling for good: rename the variable, never the string.
//
// email is deliberately not a Field: rows about staff keep ids and roles only
// (#1897), and older rows that hold an address keep their label in the ledger.
type Field struct{ key string }

var errZeroField = errors.New("audit: a recorded field has no key")

// MarshalText is the key as stored, which is how encoding/json writes a State.
func (f Field) MarshalText() ([]byte, error) {
	if f.key == "" {
		return nil, errZeroField
	}
	return []byte(f.key), nil
}

// The variable is named after the key it stores.
var (
	FieldActive               = Field{"active"}
	FieldAlt                  = Field{"alt"}
	FieldAltEn                = Field{"alt_en"}
	FieldAmountCents          = Field{"amount_cents"}
	FieldAssessmentVersion    = Field{"assessment_version"}
	FieldBrandID              = Field{"brand_id"}
	FieldCampaign             = Field{"campaign"}
	FieldCarrier              = Field{"carrier"}
	FieldCarrierEn            = Field{"carrier_en"}
	FieldCategory             = Field{"category"}
	FieldCategoryID           = Field{"category_id"}
	FieldCode                 = Field{"code"}
	FieldComparable           = Field{"comparable"}
	FieldCompareAtCents       = Field{"compare_at_cents"}
	FieldCreditReturnedCents  = Field{"credit_returned_cents"}
	FieldCTA                  = Field{"cta"}
	FieldDays                 = Field{"days"}
	FieldDecision             = Field{"decision"}
	FieldDeliveredAt          = Field{"delivered_at"}
	FieldDelta                = Field{"delta"}
	FieldDestination          = Field{"destination"}
	FieldDestinationKind      = Field{"destination_kind"}
	FieldDigest               = Field{"digest"}
	FieldDomesticPartyAddress = Field{"domestic_party_address"}
	FieldDomesticPartyName    = Field{"domestic_party_name"}
	FieldDomesticPartyPhone   = Field{"domestic_party_phone"}
	FieldEndsAt               = Field{"ends_at"}
	FieldEntitlement          = Field{"entitlement"}
	FieldEvent                = Field{"event"}
	FieldFeeCents             = Field{"fee_cents"}
	FieldFreeOverCents        = Field{"free_over_cents"}
	FieldHandled              = Field{"handled"}
	FieldHeadline             = Field{"headline"}
	FieldHidden               = Field{"hidden"}
	FieldIconKey              = Field{"icon_key"}
	FieldID                   = Field{"id"}
	FieldInvoiceUnit          = Field{"invoice_unit"}
	FieldIssueID              = Field{"issue_id"}
	FieldKind                 = Field{"kind"}
	FieldLabel                = Field{"label"}
	FieldLength               = Field{"length"}
	FieldLines                = Field{"lines"}
	FieldMessage              = Field{"message"}
	FieldMessageID            = Field{"message_id"}
	FieldMethodID             = Field{"method_id"}
	FieldMinAgeMonths         = Field{"min_age_months"}
	FieldMinSpendCents        = Field{"min_spend_cents"}
	FieldMove                 = Field{"move"}
	FieldMultiplierBP         = Field{"multiplier_bp"}
	FieldName                 = Field{"name"}
	FieldNameEn               = Field{"name_en"}
	FieldNetQuantity          = Field{"net_quantity"}
	FieldNetUnit              = Field{"net_unit"}
	FieldNote                 = Field{"note"}
	FieldNumber               = Field{"number"}
	FieldOptionValue          = Field{"option_value"}
	FieldOrder                = Field{"order"}
	FieldOrderNumber          = Field{"order_number"}
	FieldOrigin               = Field{"origin"}
	FieldOriginEn             = Field{"origin_en"}
	FieldParent               = Field{"parent"}
	FieldPolicyWindow         = Field{"policy_window"}
	FieldPrefixes             = Field{"prefixes"}
	FieldPreorderReleaseOn    = Field{"preorder_release_on"}
	FieldPriceCents           = Field{"price_cents"}
	FieldProduct              = Field{"product"}
	FieldProviderRef          = Field{"provider_ref"}
	FieldQuestion             = Field{"question"}
	FieldQuestionID           = Field{"question_id"}
	FieldReason               = Field{"reason"}
	FieldReceived             = Field{"received"}
	FieldRecipients           = Field{"recipients"}
	FieldResolution           = Field{"resolution"}
	FieldRestocked            = Field{"restocked"}
	FieldReturnRequestID      = Field{"return_request_id"}
	FieldReviewID             = Field{"review_id"}
	FieldRole                 = Field{"role"}
	FieldSKU                  = Field{"sku"}
	FieldSlug                 = Field{"slug"}
	FieldStartsAt             = Field{"starts_at"}
	FieldStatus               = Field{"status"}
	FieldStock                = Field{"stock"}
	FieldSubject              = Field{"subject"}
	FieldSurchargeCents       = Field{"surcharge_cents"}
	FieldTaxType              = Field{"tax_type"}
	FieldTitle                = Field{"title"}
	FieldTitleEn              = Field{"title_en"}
	FieldTone                 = Field{"tone"}
	FieldTracking             = Field{"tracking"}
	FieldUserID               = Field{"user_id"}
	FieldValue                = Field{"value"}
	FieldVersionID            = Field{"version_id"}
	FieldWarrantyMonths       = Field{"warranty_months"}
	FieldZoneID               = Field{"zone_id"}
)
