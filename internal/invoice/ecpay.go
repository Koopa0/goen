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

// ECPay's B2C e-invoice endpoints; staging is the default.
const (
	StagingBaseURL    = "https://einvoice-stage.ecpay.com.tw"
	ProductionBaseURL = "https://einvoice.ecpay.com.tw"
)

const requestTimeout = 20 * time.Second

// Gateway talks to ECPay. The zero value is DISABLED and answers ErrDisabled to
// everything.
type Gateway struct {
	merchantID string
	hashKey    []byte
	hashIV     []byte
	baseURL    string
	http       *http.Client
}

// NewGateway returns a Gateway for the given credentials: all three or none.
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
	// AES-128: ECPay's keys are exactly 16 bytes.
	if len(hashKey) != 16 || len(hashIV) != 16 {
		return nil, fmt.Errorf("invoice: the hash key and IV must be 16 bytes "+
			"(AES-128); got %d and %d", len(hashKey), len(hashIV))
	}
	if baseURL == "" {
		baseURL = StagingBaseURL
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invoice: base URL %q is not usable: %w", baseURL, err)
	}
	if (parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") ||
		parsedBaseURL.Host == "" || parsedBaseURL.User != nil {
		return nil, fmt.Errorf("invoice: base URL %q must be an absolute HTTP(S) URL without userinfo", baseURL)
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
	// Timestamp is Unix SECONDS. ECPay rejects one far from its own clock,
	// which is a replay bound rather than a formality.
	Timestamp int64 `json:"Timestamp"`
}

// response is the outer reply; TransCode is about the ENVELOPE and the
// operation's own result is inside the encrypted Data.
type response struct {
	TransCode int    `json:"TransCode"`
	TransMsg  string `json:"TransMsg"`
	Data      string `json:"Data"`
}

type resultStatus struct {
	RtnCode int    `json:"RtnCode"`
	RtnMsg  string `json:"RtnMsg"`
}

// providerError preserves the machine-readable ECPay verdict. Callers use the
// code for documented not-found/duplicate outcomes; operator evidence stores
// only that code and a local category, never this message or a provider payload.
type providerError struct {
	Code    int
	Message string
}

func (e *providerError) Error() string {
	return fmt.Sprintf("%s (RtnCode %d)", e.Message, e.Code)
}

func (e *providerError) Unwrap() error { return ErrRejected }

// call posts one request and returns the decrypted result. Three failure
// surfaces stay apart — transport, envelope, document — and only the last, which
// is data a staff member can fix, is ErrRejected.
func (g *Gateway) call[T any](ctx context.Context, path string, data any) (T, error) {
	var zero T
	if !g.Enabled() {
		return zero, ErrDisabled
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return zero, fmt.Errorf("encode %s request: %w", path, err)
	}
	sealed, err := g.seal(payload)
	if err != nil {
		return zero, fmt.Errorf("encrypt %s request: %w", path, err)
	}

	body, err := json.Marshal(envelope{
		MerchantID: g.merchantID,
		RqHeader:   rqHeader{Timestamp: time.Now().Unix()},
		Data:       sealed,
	})
	if err != nil {
		return zero, fmt.Errorf("encode %s envelope: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return zero, fmt.Errorf("build %s request: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return zero, fmt.Errorf("call %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded: an unbounded read from a third party is memory anybody can spend.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return zero, fmt.Errorf("read %s reply: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("%s answered %d", path, resp.StatusCode)
	}

	var outer response
	if unmarshalErr := json.Unmarshal(raw, &outer); unmarshalErr != nil {
		return zero, fmt.Errorf("decode %s reply: %w", path, unmarshalErr)
	}
	// TransCode is the ENVELOPE's verdict: 1 means ECPay could read the request.
	if outer.TransCode != 1 {
		return zero, fmt.Errorf("%s refused the envelope: %s (TransCode %d)",
			path, outer.TransMsg, outer.TransCode)
	}

	opened, err := g.open(outer.Data)
	if err != nil {
		return zero, fmt.Errorf("decrypt %s reply: %w", path, err)
	}
	var status resultStatus
	if decodeErr := json.Unmarshal(opened, &status); decodeErr != nil {
		return zero, fmt.Errorf("decode %s status: %w", path, decodeErr)
	}
	if status.RtnCode != 1 {
		return zero, &providerError{Code: status.RtnCode, Message: status.RtnMsg}
	}
	var res T
	if decodeErr := json.Unmarshal(opened, &res); decodeErr != nil {
		return zero, fmt.Errorf("decode %s result: %w", path, decodeErr)
	}
	return res, nil
}

// seal is ECPay's parameter encryption: URL-encode, AES-128-CBC with PKCS#7,
// then base64. The URL-encode comes FIRST, and their encoder is .NET's.
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

// pkcs7Unpad removes it. The last byte comes from a third party, so using it as
// a length unverified is a slice bound an attacker picks.
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
// ECPay's decoder expects. It differs from Go's url.QueryEscape twice: .NET
// lower-cases the hex digits and leaves `!`, `(`, `)` and `*` unescaped.
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
	return dotNetLiterals.Replace(b.String())
}

// dotNetLiterals restores the four escapes .NET leaves as literals. They are
// disjoint and produce no new %XX, so one pass is the whole substitution.
var dotNetLiterals = strings.NewReplacer(
	"%21", "!",
	"%28", "(",
	"%29", ")",
	"%2a", "*",
)

// itemsFor turns goen's lines into ECPay's Items array.
func itemsFor(lines []Line) []item {
	out := make([]item, 0, len(lines))
	for i, l := range lines {
		out = append(out, item{
			ItemSeq: i + 1,
			// ECPay Issue bounds ItemName at 100 characters.
			// https://developers.ecpay.com.tw/53662/
			ItemName:  truncate(l.Description, 100),
			ItemCount: l.Quantity,
			// i18n-exempt: ItemWord is the 單位 on a 統一發票, which the 財政部
			// platform records in Chinese whoever bought the thing.
			ItemWord:    "個",
			ItemPrice:   wholeDollars(l.UnitPriceCents),
			ItemAmount:  wholeDollars(l.AmountCents),
			ItemTaxType: "1", // taxable
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

// wholeDollars converts goen's cents to the whole New Taiwan dollars an invoice
// records. A Stripe CHARGE is the opposite: TWD goes there unscaled, and
// dividing it undercharges by 100x.
func wholeDollars(cents int64) int64 { return cents / 100 }

// truncate bounds a string by RUNES: a byte bound would cut a Chinese product
// name mid-character and send ECPay invalid UTF-8.
func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}
