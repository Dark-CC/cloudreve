package payment

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Easypay (易支付) is the widely deployed Chinese aggregation-payment
// protocol: the merchant redirects the browser to submit.php with an MD5
// signed parameter set, the gateway redirects back to return_url and posts an
// asynchronous GET notification to notify_url. Unlike Stripe there is no
// hosted-checkout API and no provider-side customer, so it cannot implement
// Processor; it gets its own small gateway surface instead.
//
// Protocol reference: https://pay.urldwz.com/doc.html (char set UTF-8, MD5
// signature, params sorted by name, sign/sign_type/empty values excluded,
// signature = md5(sorted "a=b&c=d" + key) in lowercase).

const (
	// EasypayTypeAlipay/EasypayTypeWxpay/EasypayTypeQqpay are the values the
	// gateway accepts in the "type" field.
	EasypayTypeAlipay = "alipay"
	EasypayTypeWxpay  = "wxpay"
	EasypayTypeQqpay  = "qqpay"
	// EasypayTypeCashier leaves the type empty so the gateway shows its own
	// cashier page where the buyer picks a channel.
	EasypayTypeCashier = ""

	// EasypayTradeSuccess is the only trade_status that counts as paid.
	EasypayTradeSuccess = "TRADE_SUCCESS"
)

// EasypayConfig carries what a single gateway call needs. PayType is the
// configured default channel (alipay/wxpay/qqpay), empty for the cashier.
type EasypayConfig struct {
	GatewayURL string
	PID        string
	Key        string
	PayType    string
}

// EasypayParams is the parameter set of one checkout, before signing.
type EasypayParams struct {
	PID        string
	PayType    string
	OutTradeNo string
	NotifyURL  string
	ReturnURL  string
	Name       string
	// Money is the amount in yuan with at most two decimals, e.g. "1.00".
	Money string
	// Param is echoed back by the gateway untouced; unused here but kept for
	// parity with the protocol.
	Param string
	// ClientIP is the buyer's address, which the gateway uses for its own risk
	// checks. Some merchants require it and reject the order without it.
	ClientIP string
}

// EasypayValues renders the parameter set as the gateway expects it: empty
// optional fields are omitted so they do not take part in the signature.
func (p EasypayParams) values() url.Values {
	v := url.Values{}
	v.Set("pid", p.PID)
	if p.PayType != "" {
		v.Set("type", p.PayType)
	}
	v.Set("out_trade_no", p.OutTradeNo)
	v.Set("notify_url", p.NotifyURL)
	v.Set("return_url", p.ReturnURL)
	v.Set("name", p.Name)
	v.Set("money", p.Money)
	if p.Param != "" {
		v.Set("param", p.Param)
	}
	if p.ClientIP != "" {
		v.Set("clientip", p.ClientIP)
	}
	return v
}

// EasypaySign computes the MD5 signature of a parameter set. sign, sign_type
// and empty values never take part; values are used raw (already decoded, no
// URL encoding) exactly as the protocol requires.
func EasypaySign(values map[string]string, key string) string {
	keys := make([]string, 0, len(values))
	for k, v := range values {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(values[k])
	}
	b.WriteString(key)

	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// EasypayCheckout is a ready-to-submit payment request: the browser POSTs
// Params to URL (form fields) to reach the gateway cashier. url.Values keeps
// the form fields already encoded for the page.
type EasypayCheckout struct {
	URL    string
	Params map[string]string
}

// EasypayBuildCheckout signs a checkout request and returns it ready for the
// frontend to POST. The gateway accepts GET too, but POST is recommended by
// the protocol (less prone to hijacking or interception).
func EasypayBuildCheckout(cfg EasypayConfig, p EasypayParams) (*EasypayCheckout, error) {
	if cfg.GatewayURL == "" || cfg.PID == "" || cfg.Key == "" {
		return nil, fmt.Errorf("easypay gateway is not configured")
	}
	if p.OutTradeNo == "" || p.Money == "" {
		return nil, fmt.Errorf("easypay checkout requires out_trade_no and money")
	}
	if p.PayType == "" {
		p.PayType = cfg.PayType
	}
	p.PID = cfg.PID

	values := p.values()
	flat := make(map[string]string, len(values))
	for k := range values {
		flat[k] = values.Get(k)
	}
	flat["sign"] = EasypaySign(flat, cfg.Key)
	flat["sign_type"] = "MD5"

	return &EasypayCheckout{
		URL:    strings.TrimRight(cfg.GatewayURL, "/") + "/submit.php",
		Params: flat,
	}, nil
}

// EasypayNotify is a verified notification (or return) payload.
type EasypayNotify struct {
	PID         string
	TradeNo     string
	OutTradeNo  string
	Type        string
	Name        string
	Money       string
	TradeStatus string
	Param       string
}

// EasypayPaid reports whether the notification says the trade succeeded.
func (n *EasypayNotify) Paid() bool {
	return n.TradeStatus == EasypayTradeSuccess
}

// EasypayParseNotify verifies the signature of an incoming notify/return
// payload and returns it. The signature covers every supplied parameter
// except sign, sign_type and empty values, so extra parameters cannot ride
// along unauthenticated.
func EasypayParseNotify(values map[string]string, key string) (*EasypayNotify, error) {
	if key == "" {
		return nil, fmt.Errorf("easypay key is not configured")
	}
	sign := values["sign"]
	if sign == "" {
		return nil, fmt.Errorf("notification carried no signature")
	}
	if st := values["sign_type"]; st != "" && !strings.EqualFold(st, "MD5") {
		return nil, fmt.Errorf("unsupported sign_type %q", st)
	}
	if expected := EasypaySign(values, key); !strings.EqualFold(expected, sign) {
		return nil, fmt.Errorf("notification signature mismatch")
	}
	if values["out_trade_no"] == "" {
		return nil, fmt.Errorf("notification carried no out_trade_no")
	}
	return &EasypayNotify{
		PID:         values["pid"],
		TradeNo:     values["trade_no"],
		OutTradeNo:  values["out_trade_no"],
		Type:        values["type"],
		Name:        values["name"],
		Money:       values["money"],
		TradeStatus: values["trade_status"],
		Param:       values["param"],
	}, nil
}

// EasypayOrder is what the mapi.php call hands back to the storefront so it
// can render an in-page payment dialog instead of redirecting to the cashier.
//
// The gateway returns at most one of qrcode/payurl/urlscheme:
//   - qrcode is a real payload to render as a QR code (a weixin:// link);
//   - payurl is a cashier page that has to be opened (or framed) instead;
//   - urlscheme is a mini-program deep link.
//
// Which one arrives depends on the channel: wxpay answers with qrcode, while
// alipay and qqpay only ever return payurl.
type EasypayOrder struct {
	TradeNo string
	// Qrcode is the payload to encode into a QR code, empty when the gateway
	// did not return one for this channel.
	Qrcode string
	// PayURL is the cashier page to open when there is no QR payload.
	PayURL string
	// URLScheme is the mini-program deep link, when the gateway chose it.
	URLScheme string
}

// EasypayGateway is the seam the service layer talks to. It is an interface so
// tests can stub the gateway without network access.
type EasypayGateway interface {
	// CreateOrder signs a checkout and asks mapi.php for a payable handle
	// (QR payload or cashier URL) that the storefront can show in a dialog.
	CreateOrder(ctx context.Context, cfg EasypayConfig, p EasypayParams) (*EasypayOrder, error)
	// BuildCheckout signs a checkout and returns the form the browser POSTs to
	// submit.php. Kept as a fallback for gateways whose mapi.php is disabled.
	BuildCheckout(cfg EasypayConfig, p EasypayParams) (*EasypayCheckout, error)
	// ParseNotify verifies an incoming notification's signature.
	ParseNotify(values map[string]string, key string) (*EasypayNotify, error)
	// OrderPaid asks the gateway whether a merchant order reference is paid.
	// Used as the authoritative settlement path for the status poll when the
	// asynchronous notification cannot reach the server.
	OrderPaid(ctx context.Context, cfg EasypayConfig, outTradeNo string) (paid bool, money string, err error)
}

type easypayGateway struct {
	client *http.Client
}

// Easypay is the easypay-backed EasypayGateway implementation.
var Easypay EasypayGateway = &easypayGateway{
	client: &http.Client{Timeout: 10 * time.Second},
}

func (g *easypayGateway) BuildCheckout(cfg EasypayConfig, p EasypayParams) (*EasypayCheckout, error) {
	return EasypayBuildCheckout(cfg, p)
}

// easypayMapiResponse is the mapi.php reply carrying the payment handle.
type easypayMapiResponse struct {
	Code      EasypayInt `json:"code"`
	Msg       string     `json:"msg"`
	TradeNo   string     `json:"trade_no"`
	PayURL    string     `json:"payurl"`
	Qrcode    string     `json:"qrcode"`
	URLScheme string     `json:"urlscheme"`
}

// EasypayCreateOrder posts the signed parameter set to mapi.php and returns
// the payable handle. The request is a form POST, the reply is JSON.
func EasypayCreateOrder(ctx context.Context, client *http.Client, cfg EasypayConfig, p EasypayParams) (*EasypayOrder, error) {
	if cfg.GatewayURL == "" || cfg.PID == "" || cfg.Key == "" {
		return nil, fmt.Errorf("easypay gateway is not configured")
	}
	if p.OutTradeNo == "" || p.Money == "" {
		return nil, fmt.Errorf("easypay checkout requires out_trade_no and money")
	}
	if p.PayType == "" {
		p.PayType = cfg.PayType
	}
	p.PID = cfg.PID

	values := p.values()
	flat := make(map[string]string, len(values))
	for k := range values {
		flat[k] = values.Get(k)
	}
	flat["sign"] = EasypaySign(flat, cfg.Key)
	flat["sign_type"] = "MD5"

	// Sign the flat set and post exactly that: url.Values would drop the
	// optional empty fields, but sign/sign_type have to travel with the body.
	form := url.Values{}
	for k, v := range flat {
		form.Set(k, v)
	}

	endpoint := strings.TrimRight(cfg.GatewayURL, "/") + "/mapi.php"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}

	var r easypayMapiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("unexpected easypay reply: %w", err)
	}
	if r.Code != 1 {
		return nil, fmt.Errorf("easypay order creation failed: code=%d msg=%s", int(r.Code), r.Msg)
	}
	if r.Qrcode == "" && r.PayURL == "" && r.URLScheme == "" {
		return nil, fmt.Errorf("easypay order creation returned no payment handle")
	}
	return &EasypayOrder{TradeNo: r.TradeNo, Qrcode: r.Qrcode, PayURL: r.PayURL, URLScheme: r.URLScheme}, nil
}

func (g *easypayGateway) CreateOrder(ctx context.Context, cfg EasypayConfig, p EasypayParams) (*EasypayOrder, error) {
	return EasypayCreateOrder(ctx, g.client, cfg, p)
}

func (g *easypayGateway) ParseNotify(values map[string]string, key string) (*EasypayNotify, error) {
	return EasypayParseNotify(values, key)
}

// easypayAPIResponse is the [API]查询单个订单 reply.
//
// code and status arrive as either numbers or numeric strings depending on the
// gateway build (this one answers {"status":"0"}), so they are decoded into
// EasypayInt rather than a plain int.
type easypayAPIResponse struct {
	Code       EasypayInt `json:"code"`
	Msg        string     `json:"msg"`
	TradeNo    string     `json:"trade_no"`
	OutTradeNo string     `json:"out_trade_no"`
	Money      string     `json:"money"`
	// Status is 1 for paid, 0 for unpaid.
	Status EasypayInt `json:"status"`
}

// EasypayInt is an integer that accepts both a JSON number and a JSON string
// holding one, because gateway replies are inconsistent about it.
type EasypayInt int

func (i *EasypayInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*i = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		// A value that is neither a number nor a numeric string is treated as
		// not-yet-paid rather than failing the whole query.
		*i = 0
		return nil //nolint:nilerr // lenient by design, see above
	}
	*i = EasypayInt(n)
	return nil
}

func (g *easypayGateway) OrderPaid(ctx context.Context, cfg EasypayConfig, outTradeNo string) (bool, string, error) {
	if cfg.GatewayURL == "" || cfg.PID == "" || cfg.Key == "" {
		return false, "", fmt.Errorf("easypay gateway is not configured")
	}
	q := url.Values{}
	q.Set("act", "order")
	q.Set("pid", cfg.PID)
	q.Set("key", cfg.Key)
	q.Set("out_trade_no", outTradeNo)
	endpoint := strings.TrimRight(cfg.GatewayURL, "/") + "/api.php?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, "", err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return false, "", err
	}
	var r easypayAPIResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return false, "", fmt.Errorf("unexpected easypay reply: %w", err)
	}
	if int(r.Code) != 1 {
		return false, "", fmt.Errorf("easypay order query failed: code=%d msg=%s", int(r.Code), r.Msg)
	}
	// The query API identifies the order by out_trade_no; guard against a
	// gateway that echoes a different order.
	if r.OutTradeNo != "" && r.OutTradeNo != outTradeNo {
		return false, "", fmt.Errorf("easypay replied for order %q, expected %q", r.OutTradeNo, outTradeNo)
	}
	return int(r.Status) == 1, r.Money, nil
}

// ParseMoneyCents converts an easypay "money" string (yuan, at most two
// decimals) into cents. Returns an error on malformed input rather than
// silently rounding, because callers compare it against a charged amount.
func ParseMoneyCents(money string) (int64, error) {
	money = strings.TrimSpace(money)
	if money == "" {
		return 0, fmt.Errorf("empty amount")
	}
	neg := false
	if strings.HasPrefix(money, "-") {
		neg = true
		money = money[1:]
	}
	intPart := money
	fracPart := ""
	if i := strings.IndexByte(money, '.'); i >= 0 {
		intPart, fracPart = money[:i], money[i+1:]
	}
	if len(fracPart) > 2 {
		fracPart = fracPart[:2]
	}
	for len(fracPart) < 2 {
		fracPart += "0"
	}
	if intPart == "" {
		intPart = "0"
	}
	whole, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("malformed amount %q: %w", money, err)
	}
	frac, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("malformed amount %q: %w", money, err)
	}
	total := whole*100 + frac
	if neg {
		total = -total
	}
	return total, nil
}

// FormatMoneyCents renders minor units as yuan with two decimals, the format
// easypay expects in "money".
func FormatMoneyCents(cents int64) string {
	neg := ""
	if cents < 0 {
		neg = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", neg, cents/100, cents%100)
}
