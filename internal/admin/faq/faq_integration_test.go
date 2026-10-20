//go:build integration

package faq_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/faq"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/site"
)

func TestSupportCanAnswerAQuestionWithoutADeploy(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := faq.NewStore(pool)
	category := "測試分類-" + uuid.NewString()[:8]
	categoryID := newFAQCategory(t, ctx, s, category, "Testing "+category)

	if errs, err := s.CreateFAQEntry(ctx, &faq.Form{
		CategoryID: categoryID, Question: "可以貨到付款嗎?", Answer: "目前只支援信用卡。",
		QuestionEn: "Can I pay on delivery?",
		AnswerEn:   "Card only for now.",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateFAQEntry: %v %v", err, errs)
	}

	view, err := s.FAQ(ctx)
	if err != nil {
		t.Fatalf("FAQ: %v", err)
	}
	var made *faq.Entry
	for i := range view.Rows {
		if view.Rows[i].Category == category {
			made = &view.Rows[i]
		}
	}
	if made == nil {
		t.Fatal("the entry is not in the back office's list")
	}
	if !made.Translated() {
		t.Error("an entry with an English answer reads as untranslated")
	}

	siteStore := site.NewStore(pool)
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Chinese", locale: i18n.ZhHant, want: "目前只支援信用卡。"},
		{name: "English", locale: i18n.En, want: "Card only for now."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows, faqErr := siteStore.FAQEntries(i18n.WithLocale(ctx, tt.locale))
			if faqErr != nil {
				t.Fatalf("FAQEntries: %v", faqErr)
			}
			var found bool
			for i := range rows {
				if rows[i].Answer == tt.want {
					found = true
				}
			}
			if !found {
				t.Errorf("/faq does not answer %q in %s", tt.want, tt.locale)
			}
		})
	}

	if errs, updErr := s.UpdateFAQEntry(ctx, &faq.Form{
		ID:       made.ID,
		Question: made.Question, Answer: "現在也支援超商取貨付款。",
		QuestionEn: made.QuestionEn, AnswerEn: "Store pickup payment works now.",
	}); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateFAQEntry: %v %v", updErr, errs)
	}
	rows, err := siteStore.FAQEntries(i18n.WithLocale(ctx, i18n.En))
	if err != nil {
		t.Fatalf("FAQEntries: %v", err)
	}
	var rewritten bool
	for i := range rows {
		if rows[i].Answer == "Store pickup payment works now." {
			rewritten = true
		}
	}
	if !rewritten {
		t.Error("the rewritten answer is not on the page")
	}

	if err := s.DeleteFAQEntry(ctx, made.ID); err != nil {
		t.Fatalf("DeleteFAQEntry: %v", err)
	}
	var rows2 int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM faq_entries WHERE category_id = $1`, categoryID).Scan(&rows2); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows2 != 0 {
		t.Errorf("%d entries survived the delete", rows2)
	}
}

func TestTwoFAQEntriesInOneCategoryDoNotCollide(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := faq.NewStore(pool)
	category := newFAQCategory(t, ctx, s, "順序分類-"+uuid.NewString()[:8], "")

	for _, q := range []string{"第一個問題", "第二個問題", "第三個問題"} {
		if errs, err := s.CreateFAQEntry(ctx, &faq.Form{
			CategoryID: category, Question: q, Answer: "答案",
		}); err != nil || len(errs) > 0 {
			t.Fatalf("CreateFAQEntry(%s): %v %v", q, err, errs)
		}
	}

	var positions []int32
	rows, err := pool.Query(ctx,
		`SELECT position FROM faq_entries WHERE category_id = $1 ORDER BY position`, category)
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p int32
		if scanErr := rows.Scan(&p); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate FAQ positions: %v", err)
	}
	if diff := cmp.Diff([]int32{1, 2, 3}, positions); diff != "" {
		t.Errorf("positions (-want +got):\n%s", diff)
	}
}

// TestTwoConcurrentFAQEntriesInOneCategoryTakeDistinctPositions proves the
// advisory lock is in the statement: without it two staff members adding at once
// both read the same max(position) and faq_entries_position_key refuses one.
func TestTwoConcurrentFAQEntriesInOneCategoryTakeDistinctPositions(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	category := newFAQCategory(t, ctx, faq.NewStore(pool), "併發分類-"+uuid.NewString()[:8], "")
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM faq_entries WHERE category_id = $1`, category)
	})
	poolA := admintest.NamedPool(t, pool, "faq-append-a-"+uuid.NewString()[:8])
	poolB := admintest.NamedPool(t, pool, "faq-append-b-"+uuid.NewString()[:8])
	storeA := faq.NewStore(poolA)
	storeB := faq.NewStore(poolB)

	start := make(chan struct{})
	type result struct {
		errs map[string]string
		err  error
	}
	done := make(chan result, 2)
	go func() {
		<-start
		errs, err := storeA.CreateFAQEntry(ctx, &faq.Form{
			CategoryID: category, Question: "問題甲", Answer: "答案",
		})
		done <- result{errs: errs, err: err}
	}()
	go func() {
		<-start
		errs, err := storeB.CreateFAQEntry(ctx, &faq.Form{
			CategoryID: category, Question: "問題乙", Answer: "答案",
		})
		done <- result{errs: errs, err: err}
	}()
	close(start)

	for range 2 {
		got := <-done
		if got.err != nil || len(got.errs) > 0 {
			t.Fatalf("CreateFAQEntry: %v %v", got.err, got.errs)
		}
	}

	var positions []int32
	rows, err := pool.Query(ctx,
		`SELECT position FROM faq_entries WHERE category_id = $1 ORDER BY position`, category)
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p int32
		if scanErr := rows.Scan(&p); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		positions = append(positions, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate FAQ positions: %v", err)
	}
	if len(positions) != 2 || positions[0] == positions[1] {
		t.Errorf("positions = %v, want two distinct values — the lock is not in "+
			"the statement, so two concurrent inserts collided on max(position)+1",
			positions)
	}
}

// newFAQCategory adds a FAQ category through the desk's store and returns its id.
func newFAQCategory(t *testing.T, ctx context.Context, s *faq.Store, name, nameEn string) string {
	t.Helper()
	if errs, err := s.CreateFAQCategory(ctx, &faq.CategoryForm{Name: name, NameEn: nameEn}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateFAQCategory(%q): %v %v", name, err, errs)
	}
	return categoryID(t, ctx, s, name)
}
