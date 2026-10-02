package admin

import (
	"slices"
	"testing"

	pagesadmin "github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestAuditChangesReadsFieldsNotJSON(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name          string
		before, after string
		want          []pagesadmin.AuditChange
	}{
		{
			name:   "a pair keeps the fields that differ",
			before: `{"status":"pending","note":"same","qty":2}`,
			after:  `{"status":"picking","note":"same","qty":2}`,
			want:   []pagesadmin.AuditChange{{Field: "status", Before: "pending", After: "picking"}},
		},
		{
			name:  "one side lists every field in order",
			after: `{"b":1,"a":"x","c":null}`,
			want: []pagesadmin.AuditChange{
				{Field: "a", After: "x"}, {Field: "b", After: "1"}, {Field: "c", After: "—"},
			},
		},
		{name: "a field only one side has", before: `{"a":1}`, after: `{"a":1,"b":true}`, want: []pagesadmin.AuditChange{{Field: "b", After: "true"}}},
		{name: "not an object is one row", after: `[1,2]`, want: []pagesadmin.AuditChange{{After: "[1,2]"}}},
		{name: "nothing recorded", want: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := auditChanges([]byte(tt.before), []byte(tt.after))
			if !slices.Equal(got, tt.want) {
				t.Errorf("auditChanges = %+v, want %+v", got, tt.want)
			}
		})
	}
}
