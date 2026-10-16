package repocheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/admin/coupons"
	"github.com/koopa0/goen/internal/admin/feedback"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/admin/returns"
	"github.com/koopa0/goen/internal/admin/taxonomy"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/warranty"
)

// These fixtures bind each method-only expression to its production receiver.
// Both arms of conditional actions matter; strings copied from routes would
// leave a broken method invisible to the route gate.
func methodFormActions(t *testing.T) map[string][]string {
	t.Helper()
	product := &products.ProductView{Slug: "product"}
	newProduct := &products.ProductView{IsNew: true}
	pdp := &pages.ProductView{Slug: "product"}
	ret := &returns.Return{ID: "return"}
	return map[string][]string{
		"internal/account/page.templ:v.AddressAction()":                                                      {(&account.AccountView{}).AddressAction(), (&account.AccountView{AddressDraft: &account.AddressDraft{ID: "address"}}).AddressAction()},
		"internal/admin/products/product_label_page.templ:templ.URL(v.LabelAction())":                        {product.LabelAction()},
		"internal/ui/layouts/banner.templ:templ.SafeURL(b.DismissAction())":                                  {(layouts.Banner{}).DismissAction()},
		"internal/ui/layouts/email_link.templ:templ.SafeURL(v.Action)":                                       newsletterFormActions(t),
		"internal/admin/products/product_page.templ:templ.SafeURL(v.Action() + \"#sec-details\")":            {product.Action() + "#sec-details", newProduct.Action() + "#sec-details"},
		"internal/admin/campaigns/page.templ:templ.SafeURL(v.ToneAction())":                                  {(&campaigns.CampaignView{Slug: "campaign"}).ToneAction()},
		"internal/admin/campaigns/page.templ:templ.SafeURL(v.WindowAction())":                                {(&campaigns.CampaignView{Slug: "campaign"}).WindowAction()},
		"internal/admin/campaigns/page.templ:templ.SafeURL(v.ActiveAction())":                                {(&campaigns.CampaignView{Slug: "campaign"}).ActiveAction()},
		"internal/admin/taxonomy/category.templ:templ.SafeURL(v.ImageAction())":                              {(taxonomy.CategoryView{Slug: "category"}).ImageAction()},
		"internal/admin/taxonomy/category.templ:templ.SafeURL(v.ImageRemoveAction())":                        {(taxonomy.CategoryView{Slug: "category"}).ImageRemoveAction()},
		"internal/admin/campaigns/page.templ:templ.SafeURL(v.ImageAction())":                                 {(&campaigns.CampaignView{Slug: "campaign"}).ImageAction()},
		"internal/admin/campaigns/page.templ:templ.SafeURL(v.ImageRemoveAction())":                           {(&campaigns.CampaignView{Slug: "campaign"}).ImageRemoveAction()},
		"internal/admin/products/product_page.templ:templ.SafeURL(v.ImageAction())":                          {product.ImageAction()},
		"internal/admin/products/product_page.templ:templ.SafeURL(v.ImageRemoveAction())":                    {product.ImageRemoveAction()},
		"internal/admin/products/product_page.templ:templ.SafeURL(v.ImageOptionAction())":                    {product.ImageOptionAction()},
		"internal/admin/products/product_page.templ:templ.SafeURL(v.ImageMoveAction())":                      {product.ImageMoveAction()},
		"internal/admin/products/product_page.templ:templ.SafeURL(v.ReuseAction())":                          {product.ReuseAction()},
		"internal/admin/products/product_page.templ:templ.SafeURL(v.OptionAction() + \"#sec-options\")":      {product.OptionAction() + "#sec-options"},
		"internal/admin/products/product_page.templ:templ.SafeURL(v.OptionValueAction() + \"#sec-options\")": {product.OptionValueAction() + "#sec-options"},
		"internal/admin/products/product_page.templ:templ.SafeURL(v.SpecAction() + \"#sec-specs\")":          {product.SpecAction() + "#sec-specs"},
		"internal/admin/products/product_page.templ:templ.SafeURL(v.SpecRemoveAction())":                     {product.SpecRemoveAction()},
		"internal/ui/pages/product.templ:templ.SafeURL(v.NotifyAction())":                                    {pdp.NotifyAction()},
		"internal/ui/pages/product.templ:templ.SafeURL(v.AskAction())":                                       {pdp.AskAction()},
		"internal/ui/pages/product.templ:templ.SafeURL(v.ReviewAction())":                                    {pdp.ReviewAction()},
		"internal/admin/returns/page.templ:templ.SafeURL(r.Action())":                                        {ret.Action()},
		"internal/admin/returns/returnconfirm.templ:templ.SafeURL(v.Action())":                               {(returns.ReturnConfirmation{ID: "return"}).Action()},
		"internal/admin/refunds/refundconfirm.templ:templ.SafeURL(v.Action())":                               {(refunds.RefundConfirmation{OrderNumber: "order"}).Action()},
		"internal/admin/refunds/unsentconfirm.templ:templ.SafeURL(v.Action())":                               {(refunds.UnsentRefundConfirmation{OrderNumber: "order"}).Action()},
		"internal/admin/returns/page.templ:templ.SafeURL(r.AssessAction())":                                  {ret.AssessAction()},
		"internal/admin/returns/page.templ:templ.SafeURL(r.InspectAction() + \"#inspect-\" + r.ID)":          {ret.InspectAction() + "#inspect-" + ret.ID},
		"internal/admin/returns/page.templ:templ.SafeURL(r.CompleteAction())":                                {ret.CompleteAction()},
		"internal/admin/feedback/message_page.templ:templ.SafeURL(m.Action())":                               {(feedback.Message{}).Action(), (feedback.Message{Handled: true}).Action()},
		"internal/admin/feedback/review_page.templ:templ.SafeURL(r.Action())":                                {(feedback.Review{}).Action(), (feedback.Review{Hidden: true}).Action()},
		"internal/admin/coupons/page.templ:templ.SafeURL(c.Action())":                                        {(coupons.Coupon{Code: "coupon"}).Action()},
		"internal/admin/content/newsletter_page.templ:templ.SafeURL(issue.SendAction())":                     {(content.NewsletterIssue{ID: "issue"}).SendAction()},
		"internal/admin/campaigns/page.templ:templ.SafeURL(c.ToggleAction())":                                {(campaigns.CampaignRow{Slug: "campaign"}).ToggleAction()},
		"internal/admin/campaigns/page.templ:templ.SafeURL(v.FeatureAction())":                               {(&campaigns.CampaignView{Slug: "campaign"}).FeatureAction()},
		"internal/admin/content/home_page.templ:templ.SafeURL(slide.PromoteAction())":                        {(content.HeroSlide{ID: "slide"}).PromoteAction()},
		"internal/admin/content/home_page.templ:templ.SafeURL(slide.ToggleAction())":                         {(content.HeroSlide{ID: "slide"}).ToggleAction()},
		"internal/admin/feedback/question_page.templ:templ.SafeURL(q.AnswerAction())":                        {(feedback.Question{ID: "question"}).AnswerAction()},
		"internal/ui/pages/listing.templ:templ.SafeURL(v.FilterAction())":                                    {(pages.ListingView{Slug: "category"}).FilterAction()},
		"internal/warranty/page.templ:templ.SafeURL(v.Action())":                                             {(warranty.WarrantyOrderView{Number: "order"}).Action()},
		"internal/payment/pay.templ:templ.SafeURL(v.Action())":                                               {(&payment.PayView{Number: "order"}).Action()},
		"internal/order/buyer/returns.templ:templ.SafeURL(v.Action())":                                       {(buyer.ReturnsView{Number: "order"}).Action()},
		"internal/admin/products/product_invoice_line.templ:templ.URL(v.InvoiceLineAction())":                {product.InvoiceLineAction()},
	}
}

// Newsletter actions are fields supplied by its handler, so read that
// production assignment instead of duplicating the two URLs in fixtures.
func newsletterFormActions(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(repoRoot(t), "internal", "newsletter", "handler.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		typ, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || typ.Sel.Name != "EmailLinkView" {
			return true
		}
		for _, element := range lit.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := pair.Key.(*ast.Ident)
			if !ok || key.Name != "Action" {
				continue
			}
			value, ok := pair.Value.(*ast.BasicLit)
			if !ok || value.Kind != token.STRING {
				t.Error("newsletter Action needs a resolvable production value")
				continue
			}
			action, err := strconv.Unquote(value.Value)
			if err != nil {
				t.Fatal(err)
			}
			actions = append(actions, action)
		}
		return true
	})
	if len(actions) == 0 {
		t.Fatal("no newsletter form actions found")
	}
	return actions
}
