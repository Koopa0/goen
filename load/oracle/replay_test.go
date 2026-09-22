package oracle_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/load/oracle"
)

type evidenceOrder struct{ number, key string }

func stockEvidence(t *testing.T, runID string, started time.Time, orders []evidenceOrder) string {
	t.Helper()
	records := []map[string]any{{"kind": "start", "run_id": runID, "started_at": started, "buyers": 2, "replays": 6}}
	for i, order := range orders {
		kind, buyer := "placement", "0"
		if i == 0 {
			kind, buyer = "anchor", "replay"
		}
		records = append(records, map[string]any{"kind": kind, "run_id": runID, "key": order.key, "order": order.number, "email": "load-" + runID + "-" + buyer + "@goen.invalid", "variant_id": oracle.FlashSaleVariantID.String(), "quantity": 1, "total_cents": 107000, "body_hash": strings.Repeat("a", 64), "cookie_hash": strings.Repeat("b", 64)})
	}
	rejected := make(map[string]any)
	for key, value := range records[2] {
		rejected[key] = value
	}
	rejected["kind"], rejected["email"], rejected["order"], rejected["key"] = "rejected", "load-"+runID+"-1@goen.invalid", "", strings.Repeat("c", 22)
	records = append(records, rejected)
	for i := range 6 {
		replay := make(map[string]any)
		for key, value := range records[1] {
			replay[key] = value
		}
		replay["kind"], replay["replay_index"] = "replay", i
		records = append(records, replay)
	}
	var out strings.Builder
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(encoded)
		out.WriteByte('\n')
	}
	return out.String()
}

func TestReadStockRun(t *testing.T) {
	t.Parallel()
	const runID = "current-run-1234"
	evidence := stockEvidence(t, runID, time.Now().UTC(), []evidenceOrder{{"GO-260922-000001", strings.Repeat("a", 22)}, {"GO-260922-000002", strings.Repeat("b", 22)}})
	if _, err := oracle.ReadStockRun(strings.NewReader(evidence), runID); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(evidence), "\n")
	for name, bad := range map[string]string{
		"empty": "", "no replay": strings.Join(lines[:4], "\n"),
		"no successful competing placement": strings.Join(append(append([]string{}, lines[:2]...), lines[3:]...), "\n"),
		"other run":                         strings.ReplaceAll(evidence, runID, "previous-run-1234"),
		"changed replay order":              strings.Join(lines[:4], "\n") + "\n" + strings.ReplaceAll(strings.Join(lines[4:], "\n"), "GO-260922-000001", "GO-260922-000099"),
		"changed cookies":                   strings.Join(lines[:4], "\n") + "\n" + strings.ReplaceAll(strings.Join(lines[4:], "\n"), strings.Repeat("b", 64), strings.Repeat("c", 64)),
		"changed payload":                   strings.Join(lines[:4], "\n") + "\n" + strings.ReplaceAll(strings.Join(lines[4:], "\n"), strings.Repeat("a", 64), strings.Repeat("d", 64)),
		"repeated replay":                   evidence + lines[4] + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := oracle.ReadStockRun(strings.NewReader(bad), runID); err == nil {
				t.Fatal("incomplete or changed run accepted")
			}
		})
	}
}
