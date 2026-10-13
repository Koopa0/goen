package pages_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// These fixtures bind each method-only expression to its production receiver.
// Both arms of conditional actions matter; strings copied from routes would
// leave a broken method invisible to the route gate.
func methodFormActions(t *testing.T) map[string][]string {
	t.Helper()
	product := &admin.ProductView{Slug: "product"}
	newProduct := &admin.ProductView{IsNew: true}
	pdp := &pages.ProductView{Slug: "product"}
	ret := &admin.Return{ID: "return"}
	return map[string][]string{
		"pages/admin/product_label.templ:templ.URL(v.LabelAction())":                        {product.LabelAction()},
		"layouts/banner.templ:templ.SafeURL(b.DismissAction())":                             {(layouts.Banner{}).DismissAction()},
		"pages/email_link.templ:templ.SafeURL(v.Action)":                                    newsletterFormActions(t),
		"pages/admin/product.templ:templ.SafeURL(v.Action() + \"#sec-details\")":            {product.Action() + "#sec-details", newProduct.Action() + "#sec-details"},
		"pages/admin/campaign.templ:templ.SafeURL(v.ToneAction())":                          {(&admin.CampaignView{Slug: "campaign"}).ToneAction()},
		"pages/admin/campaign.templ:templ.SafeURL(v.WindowAction())":                        {(&admin.CampaignView{Slug: "campaign"}).WindowAction()},
		"pages/admin/campaign.templ:templ.SafeURL(v.ActiveAction())":                        {(&admin.CampaignView{Slug: "campaign"}).ActiveAction()},
		"pages/admin/category.templ:templ.SafeURL(v.ImageAction())":                         {(admin.CategoryView{Slug: "category"}).ImageAction()},
		"pages/admin/category.templ:templ.SafeURL(v.ImageRemoveAction())":                   {(admin.CategoryView{Slug: "category"}).ImageRemoveAction()},
		"pages/admin/campaign.templ:templ.SafeURL(v.ImageAction())":                         {(&admin.CampaignView{Slug: "campaign"}).ImageAction()},
		"pages/admin/campaign.templ:templ.SafeURL(v.ImageRemoveAction())":                   {(&admin.CampaignView{Slug: "campaign"}).ImageRemoveAction()},
		"pages/admin/product.templ:templ.SafeURL(v.ImageAction())":                          {product.ImageAction()},
		"pages/admin/product.templ:templ.SafeURL(v.ImageRemoveAction())":                    {product.ImageRemoveAction()},
		"pages/admin/product.templ:templ.SafeURL(v.ImageOptionAction())":                    {product.ImageOptionAction()},
		"pages/admin/product.templ:templ.SafeURL(v.ImageMoveAction())":                      {product.ImageMoveAction()},
		"pages/admin/product.templ:templ.SafeURL(v.ReuseAction())":                          {product.ReuseAction()},
		"pages/admin/product.templ:templ.SafeURL(v.OptionAction() + \"#sec-options\")":      {product.OptionAction() + "#sec-options"},
		"pages/admin/product.templ:templ.SafeURL(v.OptionValueAction() + \"#sec-options\")": {product.OptionValueAction() + "#sec-options"},
		"pages/admin/product.templ:templ.SafeURL(v.SpecAction() + \"#sec-specs\")":          {product.SpecAction() + "#sec-specs"},
		"pages/admin/product.templ:templ.SafeURL(v.SpecRemoveAction())":                     {product.SpecRemoveAction()},
		"pages/product.templ:templ.SafeURL(v.NotifyAction())":                               {pdp.NotifyAction()},
		"pages/product.templ:templ.SafeURL(v.AskAction())":                                  {pdp.AskAction()},
		"pages/product.templ:templ.SafeURL(v.ReviewAction())":                               {pdp.ReviewAction()},
		"pages/admin/returns.templ:templ.SafeURL(r.Action())":                               {ret.Action()},
		"pages/admin/returnconfirm.templ:templ.SafeURL(v.Action())":                         {(admin.ReturnConfirmation{ID: "return"}).Action()},
		"pages/admin/refundconfirm.templ:templ.SafeURL(v.Action())":                         {(admin.RefundConfirmation{OrderNumber: "order"}).Action()},
		"pages/admin/returns.templ:templ.SafeURL(r.AssessAction())":                         {ret.AssessAction()},
		"pages/admin/returns.templ:templ.SafeURL(r.InspectAction() + \"#inspect-\" + r.ID)": {ret.InspectAction() + "#inspect-" + ret.ID},
		"pages/admin/returns.templ:templ.SafeURL(r.CompleteAction())":                       {ret.CompleteAction()},
		"pages/admin/message.templ:templ.SafeURL(m.Action())":                               {(admin.Message{}).Action(), (admin.Message{Handled: true}).Action()},
		"pages/admin/review.templ:templ.SafeURL(r.Action())":                                {(admin.Review{}).Action(), (admin.Review{Hidden: true}).Action()},
		"pages/admin/coupon.templ:templ.SafeURL(c.Action())":                                {(admin.Coupon{Code: "coupon"}).Action()},
		"pages/admin/newsletter.templ:templ.SafeURL(issue.SendAction())":                    {(admin.NewsletterIssue{ID: "issue"}).SendAction()},
		"pages/admin/campaign.templ:templ.SafeURL(c.ToggleAction())":                        {(admin.CampaignRow{Slug: "campaign"}).ToggleAction()},
		"pages/admin/campaign.templ:templ.SafeURL(v.FeatureAction())":                       {(&admin.CampaignView{Slug: "campaign"}).FeatureAction()},
		"pages/admin/home.templ:templ.SafeURL(slide.PromoteAction())":                       {(admin.HeroSlide{ID: "slide"}).PromoteAction()},
		"pages/admin/home.templ:templ.SafeURL(slide.ToggleAction())":                        {(admin.HeroSlide{ID: "slide"}).ToggleAction()},
		"pages/admin/question.templ:templ.SafeURL(q.AnswerAction())":                        {(admin.Question{ID: "question"}).AnswerAction()},
		"pages/listing.templ:templ.SafeURL(v.FilterAction())":                               {(pages.ListingView{Slug: "category"}).FilterAction()},
		"pages/warranty.templ:templ.SafeURL(v.Action())":                                    {(pages.WarrantyOrderView{Number: "order"}).Action()},
		"pages/pay.templ:templ.SafeURL(v.Action())":                                         {(&pages.PayView{Number: "order"}).Action()},
		"pages/returns.templ:templ.SafeURL(v.Action())":                                     {(pages.ReturnsView{Number: "order"}).Action()},
		"pages/admin/product_invoice_line.templ:templ.URL(v.InvoiceLineAction())":           {product.InvoiceLineAction()},
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
