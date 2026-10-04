package stock

import (
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/shoptime"
)

// maxAdjustment bounds one stock correction: large enough for a delivery,
// small enough that a typo cannot invent a warehouse.
const maxAdjustment = 10000

func ParseAdjustment(s string) (int32, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || n == 0 || n > maxAdjustment || n < -maxAdjustment {
		return 0, false
	}
	return int32(n), true
}

// ParseReceipt reads a goods-receipt quantity, which is always POSITIVE.
// inventory_movements_delta_direction stays the authority; this turns a mistyped
// minus sign into a form the shop can correct rather than a constraint name.
func ParseReceipt(s string) (int32, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || n <= 0 || n > maxAdjustment {
		return 0, false
	}
	return int32(n), true
}

func parseArrival(raw string) (pgtype.Date, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.Date{}, true
	}
	day, ok := shoptime.ParseInputDay(raw)
	return pgtype.Date{Time: day, Valid: ok}, ok
}

func arrivalInput(day pgtype.Date) string {
	if !day.Valid || day.InfinityModifier != pgtype.Finite {
		return ""
	}
	return shoptime.Day(day.Time)
}
