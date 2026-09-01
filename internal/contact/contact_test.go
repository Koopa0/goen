package contact_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/contact"
)

func TestClean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   contact.Message
		want contact.Message
	}{
		{
			name: "trims every field",
			in: contact.Message{
				Name:     "  王小明 ",
				Email:    "  Me@Example.COM ",
				Subject:  " 訂單問題 ",
				OrderRef: " #GO-1024 ",
				Body:     "\n訂單還沒出貨\n",
			},
			want: contact.Message{
				Name:     "王小明",
				Email:    "me@example.com",
				Subject:  "訂單問題",
				OrderRef: "#GO-1024",
				Body:     "訂單還沒出貨",
			},
		},
		{
			name: "whitespace-only fields become empty",
			in:   contact.Message{Name: "   ", Email: "\t", Body: "\n "},
			want: contact.Message{},
		},
		{
			name: "already clean input is unchanged",
			in: contact.Message{
				Name: "王小明", Email: "me@example.com",
				Subject: "退換貨", OrderRef: "", Body: "想退貨",
			},
			want: contact.Message{
				Name: "王小明", Email: "me@example.com",
				Subject: "退換貨", OrderRef: "", Body: "想退貨",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, contact.Clean(tt.in)); diff != "" {
				t.Errorf("Clean() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// valid is a submission every field of which passes, so a case can change one
// field and attribute the failure to it.
func valid() contact.Message {
	return contact.Message{
		Name:     "王小明",
		Email:    "me@example.com",
		Subject:  "訂單問題",
		OrderRef: "#GO-1024",
		Body:     "訂單 #GO-1024 已經三天沒有出貨,想確認狀況。",
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(m *contact.Message)
		wantFields []string
	}{
		{name: "accepts a complete message", mutate: func(*contact.Message) {}, wantFields: nil},
		{
			name:       "accepts an omitted order reference",
			mutate:     func(m *contact.Message) { m.OrderRef = "" },
			wantFields: nil,
		},
		{
			name:       "rejects a missing name",
			mutate:     func(m *contact.Message) { m.Name = "" },
			wantFields: []string{"name"},
		},
		{
			name:       "rejects an over-long name",
			mutate:     func(m *contact.Message) { m.Name = strings.Repeat("名", 81) },
			wantFields: []string{"name"},
		},
		{
			name:       "accepts a name at the limit",
			mutate:     func(m *contact.Message) { m.Name = strings.Repeat("名", 80) },
			wantFields: nil,
		},
		{
			name:       "rejects a missing email",
			mutate:     func(m *contact.Message) { m.Email = "" },
			wantFields: []string{"email"},
		},
		{
			name:       "rejects an unparseable email",
			mutate:     func(m *contact.Message) { m.Email = "me@@example.com" },
			wantFields: []string{"email"},
		},
		{
			name:       "rejects an email carrying a display name",
			mutate:     func(m *contact.Message) { m.Email = "王小明 <me@example.com>" },
			wantFields: []string{"email"},
		},
		{
			name:       "rejects a subject outside the offered set",
			mutate:     func(m *contact.Message) { m.Subject = "退款詐騙" },
			wantFields: []string{"subject"},
		},
		{
			name:       "rejects a missing subject",
			mutate:     func(m *contact.Message) { m.Subject = "" },
			wantFields: []string{"subject"},
		},
		{
			name:       "rejects an over-long order reference",
			mutate:     func(m *contact.Message) { m.OrderRef = strings.Repeat("A", 33) },
			wantFields: []string{"order_ref"},
		},
		{
			name:       "rejects a missing message",
			mutate:     func(m *contact.Message) { m.Body = "" },
			wantFields: []string{"message"},
		},
		{
			name:       "rejects a message below the floor",
			mutate:     func(m *contact.Message) { m.Body = "壞了" },
			wantFields: []string{"message"},
		},
		{
			name:       "accepts a message at the floor",
			mutate:     func(m *contact.Message) { m.Body = "螢幕壞掉了" },
			wantFields: nil,
		},
		{
			name:       "rejects a message above the ceiling",
			mutate:     func(m *contact.Message) { m.Body = strings.Repeat("字", 2001) },
			wantFields: []string{"message"},
		},
		{
			name:       "rejects a newline in the name",
			mutate:     func(m *contact.Message) { m.Name = "王小明\n偽造的第二行" },
			wantFields: []string{"name"},
		},
		{
			name:       "rejects a NUL in the subject",
			mutate:     func(m *contact.Message) { m.Subject = "訂單問題\x00" },
			wantFields: []string{"subject"},
		},
		{
			name:       "rejects a control character in the order reference",
			mutate:     func(m *contact.Message) { m.OrderRef = "#GO-1024\r" },
			wantFields: []string{"order_ref"},
		},
		{
			name:       "accepts line breaks in the message body",
			mutate:     func(m *contact.Message) { m.Body = "第一行\n第二行\n第三行" },
			wantFields: nil,
		},
		{
			name:       "rejects a NUL in the message body",
			mutate:     func(m *contact.Message) { m.Body = "訂單有問題\x00" },
			wantFields: []string{"message"},
		},
		{
			name:       "rejects a newline smuggled into the email",
			mutate:     func(m *contact.Message) { m.Email = "me@example.com\nBCC: someone@else.com" },
			wantFields: []string{"email"},
		},
		{
			name: "reports every broken field at once",
			mutate: func(m *contact.Message) {
				m.Name = ""
				m.Email = "nope"
				m.Body = ""
			},
			wantFields: []string{"name", "email", "message"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			msg := valid()
			tt.mutate(&msg)
			got := contact.Validate(t.Context(), msg)

			for _, field := range tt.wantFields {
				if got[field] == "" {
					t.Errorf("Validate() reported no error for %q; want one", field)
				}
			}
			if len(got) != len(tt.wantFields) {
				t.Errorf("Validate() = %v; want errors on exactly %v", got, tt.wantFields)
			}
		})
	}
}
