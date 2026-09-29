package invoice

import (
	"context"
	"errors"
	"time"
)

// CarrierStatus is an existence verdict, separate from a successful API call.
type CarrierStatus uint8

const (
	CarrierUnknown CarrierStatus = iota
	CarrierExists
	CarrierMissing
)

const carrierCheckTimeout = 3 * time.Second

// CheckBarcode asks only whether a mobile carrier exists; it never issues an
// invoice. ECPay calls this an auxiliary check because Ministry maintenance can
// prevent a verdict: https://developers.ecpay.com.tw/7886/.
func (g *Gateway) CheckBarcode(ctx context.Context, barcode string) (CarrierStatus, error) {
	if !ValidMobileCarrier(barcode) {
		return CarrierUnknown, errors.New("invoice: malformed mobile carrier")
	}
	if !g.Enabled() {
		return CarrierUnknown, ErrDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, carrierCheckTimeout)
	defer cancel()
	res, err := g.call[struct {
		IsExist string `json:"IsExist"`
	}](ctx, "/B2CInvoice/CheckBarcode", struct {
		MerchantID string `json:"MerchantID"`
		BarCode    string `json:"BarCode"`
	}{MerchantID: g.merchantID, BarCode: barcode})
	if err != nil {
		return CarrierUnknown, err
	}
	// call requires both TransCode and RtnCode 1. Neither means the carrier
	// exists; only the documented IsExist values may decide that.
	switch res.IsExist {
	case "Y":
		return CarrierExists, nil
	case "N":
		return CarrierMissing, nil
	default:
		return CarrierUnknown, errors.New("invoice: carrier check returned no existence verdict")
	}
}
