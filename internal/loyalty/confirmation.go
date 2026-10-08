package loyalty

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/money"
)

const confirmationLifetime = 5 * time.Minute
const maxConfirmationToken = 256

type redemptionConfirmation struct {
	OperationID uuid.UUID
	Points      int64
	CreditCents int64
	IssuedAt    int64
}

func encodeRedemptionConfirmation(key, owner string, result redemptionConfirmation) string {
	body := fmt.Sprintf("%s:%d:%d:%d", result.OperationID, result.Points, result.CreditCents, result.IssuedAt)
	mac := confirmationMAC(key, owner, []byte(body))
	return base64.RawURLEncoding.EncodeToString([]byte(body)) + "." + base64.RawURLEncoding.EncodeToString(mac)
}

func readRedemptionConfirmation(key, owner, token string, now time.Time) (redemptionConfirmation, bool) {
	var zero redemptionConfirmation
	if key == "" || owner == "" || token == "" || len(token) > maxConfirmationToken {
		return zero, false
	}
	encoded, signature, found := strings.Cut(token, ".")
	if !found {
		return zero, false
	}
	body, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return zero, false
	}
	mac, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil || !hmac.Equal(mac, confirmationMAC(key, owner, body)) {
		return zero, false
	}
	return parseRedemptionConfirmation(body, now)
}

func parseRedemptionConfirmation(body []byte, now time.Time) (redemptionConfirmation, bool) {
	fields := strings.Split(string(body), ":")
	if len(fields) != 4 {
		return redemptionConfirmation{}, false
	}
	operation, operationErr := uuid.Parse(fields[0])
	points, pointsErr := strconv.ParseInt(fields[1], 10, 64)
	cents, centsErr := strconv.ParseInt(fields[2], 10, 64)
	issued, issuedErr := strconv.ParseInt(fields[3], 10, 64)
	if operationErr != nil || pointsErr != nil || centsErr != nil || issuedErr != nil {
		return redemptionConfirmation{}, false
	}
	result := redemptionConfirmation{OperationID: operation, Points: points, CreditCents: cents, IssuedAt: issued}
	if !result.valid(now) {
		return redemptionConfirmation{}, false
	}
	return result, true
}

func (r redemptionConfirmation) valid(now time.Time) bool {
	return r.OperationID != uuid.Nil && r.Points >= MinRedemption && r.Points <= MaxRedemptionPoints &&
		r.Points%PointsPerCredit == 0 && r.CreditCents > 0 && r.CreditCents <= money.MaxCents &&
		r.IssuedAt > 0 && r.IssuedAt <= now.Unix() && r.IssuedAt >= now.Unix()-int64(confirmationLifetime/time.Second)
}

func confirmationMAC(key, owner string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(key))
	// A result copied to another account or feature is not that reader's result.
	_, _ = mac.Write([]byte("loyalty:redemption\n" + owner + "\n"))
	_, _ = mac.Write(body)
	return mac.Sum(nil)
}
