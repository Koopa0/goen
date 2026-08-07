package invoice

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ECPay's B2C e-invoice endpoints.
//
// Staging is the default and the only one a demonstration deployment should
// reach. Production is named here so the switch is a configuration change rather
// than an edit, which is the whole point of integrating the real API.
const (
	StagingBaseURL    = "https://einvoice-stage.ecpay.com.tw"
	ProductionBaseURL = "https://einvoice.ecpay.com.tw"
)

// requestTimeout bounds a call to the 加值中心.
//
// Issuing runs inside a request a staff member is waiting on, and an invoice
// that has not been filed is recoverable — the order stands, the preference is
// recorded, and it can be issued again. Hanging is not: it holds a connection
// and a transaction open for as long as somebody else's server feels like.
const requestTimeout = 20 * time.Second

// Gateway talks to ECPay. The zero value is DISABLED and answers ErrDisabled to
// everything, which is the shape payment.Gateway already has for a missing
// Stripe key.
type Gateway struct {
	merchantID string
	hashKey    []byte
	hashIV     []byte
	baseURL    string
	http       *http.Client
}

// NewGateway returns a Gateway for the given credentials.
//
// All three or none. A merchant id without its keys cannot sign a request, so a
// deployment that sets one and forgets the others would fail at the first
// issue — with money already taken and an order already placed. It refuses to
// start instead, which is what a Stripe key without its webhook secret does.
func NewGateway(merchantID, hashKey, hashIV, baseURL string) (*Gateway, error) {
	if merchantID == "" && hashKey == "" && hashIV == "" {
		return &Gateway{}, nil
	}
	if merchantID == "" || hashKey == "" || hashIV == "" {
		// i18n-exempt: a startup failure, read by whoever is deploying this. There
		// is no visitor and no locale to read one from.
		return nil, errors.New("invoice: a 加值中心 needs a merchant id, a hash key and a hash IV — " +
			"one without the others cannot sign a request")
	}
	// AES-128: ECPay's keys are exactly 16 bytes, and a wrong length here fails
	// at the first call with a cipher error nobody can act on.
	if len(hashKey) != 16 || len(hashIV) != 16 {
		return nil, fmt.Errorf("invoice: the hash key and IV must be 16 bytes "+
			"(AES-128); got %d and %d", len(hashKey), len(hashIV))
	}
	if baseURL == "" {
		baseURL = StagingBaseURL
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("invoice: base URL %q is not usable: %w", baseURL, err)
	}
	return &Gateway{
		merchantID: merchantID,
		hashKey:    []byte(hashKey),
		hashIV:     []byte(hashIV),
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		http:       &http.Client{Timeout: requestTimeout},
	}, nil
}

// Enabled reports whether this deployment can issue anything.
func (g *Gateway) Enabled() bool { return g != nil && g.merchantID != "" }

// envelope is ECPay's outer request: everything meaningful is inside the
// encrypted Data string.
type envelope struct {
	MerchantID string   `json:"MerchantID"`
	RqHeader   rqHeader `json:"RqHeader"`
	Data       string   `json:"Data"`
}

type rqHeader struct {
	// Timestamp is Unix SECONDS as a number. ECPay rejects a request whose
	// timestamp is far from its own clock, which is a replay bound rather than
	// a formality.
	Timestamp int64 `json:"Timestamp"`
}

// response is the outer reply. TransCode is about the ENVELOPE — whether ECPay
// could read the request at all — and the result of the operation is inside the
// encrypted Data.
type response struct {
	TransCode int    `json:"TransCode"`
	TransMsg  string `json:"TransMsg"`
	Data      string `json:"Data"`
}

// result is the decrypted inner reply every B2C endpoint shares.
type result struct {
	RtnCode     int    `json:"RtnCode"`
	RtnMsg      string `json:"RtnMsg"`
	InvoiceNo   string `json:"InvoiceNo"`
	InvoiceDate string `json:"InvoiceDate"`
	// RandomNumber is the four digits a VOID needs alongside the number, and the
	// issue reply is the only place ECPay ever returns them.
	RandomNumber string `json:"RandomNumber"`
	// IA_Allow_No is the 折讓單號. A different field from InvoiceNo, which the
	// allowance reply leaves EMPTY — found by the staging API, whose allowance
	// succeeded and came back with no number at all until this was read.
	AllowanceNo string `json:"IA_Allow_No"`
}

// call posts one request and returns the decrypted result.
//
// The three failure surfaces are kept apart because they mean different things
// to a caller: the transport failed (retry), ECPay could not read the envelope
// (a bug here), or ECPay read it and refused the document (fix the data). Only
// the third is ErrRejected.
func (g *Gateway) call(ctx context.Context, path string, data any) (*result, error) {
	if !g.Enabled() {
		return nil, ErrDisabled
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", path, err)
	}
	sealed, err := g.seal(payload)
	if err != nil {
		return nil, fmt.Errorf("encrypt %s request: %w", path, err)
	}

	body, err := json.Marshal(envelope{
		MerchantID: g.merchantID,
		RqHeader:   rqHeader{Timestamp: time.Now().Unix()},
		Data:       sealed,
	})
	if err != nil {
		return nil, fmt.Errorf("encode %s envelope: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", path, err)
	}
	// Nothing to do on a read-side close failure, and the reply is already read.
	defer func() { _ = resp.Body.Close() }()

	// Bounded, because the reply is a small JSON document and an unbounded read
	// from a third party is memory anybody can spend.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s reply: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", path, resp.StatusCode)
	}

	var outer response
	if unmarshalErr := json.Unmarshal(raw, &outer); unmarshalErr != nil {
		return nil, fmt.Errorf("decode %s reply: %w", path, unmarshalErr)
	}
	// TransCode is the ENVELOPE's verdict: 1 means ECPay could read the request.
	// Anything else is goen's own bug — a bad merchant id, a clock far from
	// theirs, an unreadable Data — and never something a staff member can fix.
	if outer.TransCode != 1 {
		return nil, fmt.Errorf("%s refused the envelope: %s (TransCode %d)",
			path, outer.TransMsg, outer.TransCode)
	}

	opened, err := g.open(outer.Data)
	if err != nil {
		return nil, fmt.Errorf("decrypt %s reply: %w", path, err)
	}
	var res result
	if decodeErr := json.Unmarshal(opened, &res); decodeErr != nil {
		return nil, fmt.Errorf("decode %s result: %w", path, decodeErr)
	}
	// RtnCode is the DOCUMENT's verdict. 1 is success and everything else names
	// what ECPay refused, which is what a staff member acts on.
	if res.RtnCode != 1 {
		return nil, fmt.Errorf("%w: %s (RtnCode %d)", ErrRejected, res.RtnMsg, res.RtnCode)
	}
	return &res, nil
}

// seal is ECPay's parameter encryption: URL-encode, AES-128-CBC with PKCS#7,
// then base64.
//
// The URL-ENCODE comes first and is the step that looks wrong and is not: ECPay
// specifies it, and skipping it produces a request they decrypt into something
// they cannot parse — a TransCode failure with no useful message. Their own
// encoder is .NET's UrlEncode, which lower-cases the hex digits and encodes a
// space as '+', so this matches that rather than Go's default.
func (g *Gateway) seal(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(g.hashKey)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad([]byte(dotNetURLEncode(string(plaintext))), block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, g.hashIV).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), nil
}

// open reverses seal.
func (g *Gateway) open(sealed string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, fmt.Errorf("base64: %w", err)
	}
	block, err := aes.NewCipher(g.hashKey)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("ciphertext is %d bytes, not a multiple of %d",
			len(raw), block.BlockSize())
	}
	out := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, g.hashIV).CryptBlocks(out, raw)
	unpadded, err := pkcs7Unpad(out, block.BlockSize())
	if err != nil {
		return nil, err
	}
	decoded, err := url.QueryUnescape(string(unpadded))
	if err != nil {
		return nil, fmt.Errorf("url-decode: %w", err)
	}
	return []byte(decoded), nil
}

// pkcs7Pad appends the padding AES-CBC needs.
func pkcs7Pad(b []byte, size int) []byte {
	n := size - len(b)%size
	//nolint:gosec // G115: n is size - len(b)%size, so 1..size, and size is
	// aes.BlockSize. It cannot exceed a byte.
	return append(b, bytes.Repeat([]byte{byte(n)}, n)...)
}

// pkcs7Unpad removes it, refusing a length the padding cannot describe.
//
// Checked rather than trusted: the last byte comes from a third party, and using
// it as a length unverified is a slice bound an attacker picks.
func pkcs7Unpad(b []byte, size int) ([]byte, error) {
	if len(b) == 0 || len(b)%size != 0 {
		return nil, fmt.Errorf("padded length %d is not a multiple of %d", len(b), size)
	}
	n := int(b[len(b)-1])
	if n == 0 || n > size || n > len(b) {
		return nil, fmt.Errorf("padding byte %d is not a valid length", n)
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, errors.New("padding bytes disagree")
		}
	}
	return b[:len(b)-n], nil
}

// dotNetURLEncode matches System.Web.HttpUtility.UrlEncode, which is what
// ECPay's own SDK uses and therefore what their decoder expects.
//
// Two differences from Go's url.QueryEscape, and both matter: .NET lower-cases
// the hex digits, and it leaves `!`, `(`, `)` and `*` unescaped. A request that
// differs decrypts to a string their parser reads differently — which surfaces
// as an envelope rejection with no indication of why.
func dotNetURLEncode(s string) string {
	escaped := url.QueryEscape(s)
	var b strings.Builder
	b.Grow(len(escaped))
	for i := 0; i < len(escaped); i++ {
		c := escaped[i]
		if c == '%' && i+2 < len(escaped) {
			b.WriteByte('%')
			b.WriteString(strings.ToLower(escaped[i+1 : i+3]))
			i += 2
			continue
		}
		b.WriteByte(c)
	}
	out := b.String()
	for from, to := range map[string]string{
		"%21": "!", "%28": "(", "%29": ")", "%2a": "*",
	} {
		out = strings.ReplaceAll(out, from, to)
	}
	return out
}

// itemsFor turns goen's lines into ECPay's Items array.
func itemsFor(lines []Line) []item {
	out := make([]item, 0, len(lines))
	for i, l := range lines {
		out = append(out, item{
			ItemSeq: i + 1,
			// ItemName is bounded at 500 characters by ECPay. A product name
			// that long is not a real one, and truncating beats a rejection
			// nobody can act on — the invoice still names what was bought.
			ItemName:  truncate(l.Description, 500),
			ItemCount: l.Quantity,
			// i18n-exempt: ItemWord is the 單位 on a 統一發票, which the 財政部
			// platform records in Chinese whoever bought the thing.
			ItemWord:    "個",
			ItemPrice:   wholeDollars(l.UnitPriceCents),
			ItemAmount:  wholeDollars(l.AmountCents),
			ItemTaxType: "1", // 應稅
		})
	}
	return out
}

type item struct {
	ItemSeq     int    `json:"ItemSeq"`
	ItemName    string `json:"ItemName"`
	ItemCount   int32  `json:"ItemCount"`
	ItemWord    string `json:"ItemWord"`
	ItemPrice   int64  `json:"ItemPrice"`
	ItemTaxType string `json:"ItemTaxType"`
	ItemAmount  int64  `json:"ItemAmount"`
}

// wholeDollars converts goen's cents to the NEW TAIWAN DOLLARS ECPay expects.
//
// This is the direction the Stripe integration got wrong once, in reverse: for
// CHARGES, TWD passes through unscaled and dividing undercharges by 100 (mistake
// #18). An INVOICE is the opposite — 統一發票 amounts are whole dollars, because
// that is the unit the 財政部 platform records — so the division belongs here and
// nowhere near a payment.
//
// Integer, not float. A fractional SalesAmount is a rejection rather than a
// rounding, and goen's own prices are whole dollars anyway: every price column
// is cents and every one of them is a multiple of 100.
func wholeDollars(cents int64) int64 { return cents / 100 }

// truncate bounds a string by RUNES, not bytes.
//
// A byte bound would cut a Chinese product name mid-character and send ECPay
// invalid UTF-8, which they refuse — and the catalogue is Chinese.
func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}
