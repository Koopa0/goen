package invoice

import (
	"context"
	"errors"
	"time"
)

// BarcodeStatus is an existence verdict, separate from a successful API call.
type BarcodeStatus uint8

const (
	BarcodeUnknown BarcodeStatus = iota
	BarcodeExists
	BarcodeMissing
)

const barcodeCheckTimeout = 3 * time.Second

// CheckBarcode asks only whether a mobile barcode exists; it never issues an
// invoice. ECPay calls this an auxiliary check because Ministry maintenance can
// prevent a verdict: https://developers.ecpay.com.tw/7886/.
func (g *Gateway) CheckBarcode(ctx context.Context, barcode string) (BarcodeStatus, error) {
	if !ValidMobileBarcode(barcode) {
		return BarcodeUnknown, errors.New("invoice: malformed mobile barcode")
	}
	if !g.Enabled() {
		return BarcodeUnknown, ErrDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, barcodeCheckTimeout)
	defer cancel()
	res, err := g.call[struct {
		IsExist string `json:"IsExist"`
	}](ctx, "/B2CInvoice/CheckBarcode", struct {
		MerchantID string `json:"MerchantID"`
		BarCode    string `json:"BarCode"`
	}{MerchantID: g.merchantID, BarCode: barcode})
	if err != nil {
		return BarcodeUnknown, err
	}
	// call requires both TransCode and RtnCode 1. Neither means the barcode
	// exists; only the documented IsExist values may decide that.
	switch res.IsExist {
	case "Y":
		return BarcodeExists, nil
	case "N":
		return BarcodeMissing, nil
	default:
		return BarcodeUnknown, errors.New("invoice: barcode check returned no existence verdict")
	}
}
