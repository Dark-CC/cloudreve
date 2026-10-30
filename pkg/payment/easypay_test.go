package payment

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The signature is defined by the gateway protocol: params sorted by name,
// sign/sign_type/empty values excluded, values raw, md5(sorted + key) in
// lowercase.
func TestEasypaySign(t *testing.T) {
	key := "testkey"
	values := map[string]string{
		"pid":          "1005",
		"out_trade_no": "20260806151343349",
		"name":         "VIP",
		"money":        "1.00",
		"type":         "alipay",
		"notify_url":   "https://example.com/notify",
		"return_url":   "https://example.com/return",
		"sign":         "ignored",
		"sign_type":    "MD5",
		"param":        "",
	}
	got := EasypaySign(values, key)
	// Recomputing with the same inputs is stable and ignores sign/sign_type.
	require.Equal(t, got, EasypaySign(values, key))
	require.Len(t, got, 32)
	// A different key yields a different signature, and the trailing key is
	// part of the hash (no separator).
	require.NotEqual(t, got, EasypaySign(values, "otherkey"))
	// Empty values are excluded: dropping an empty param changes nothing.
	without := map[string]string{}
	for k, v := range values {
		if v != "" {
			without[k] = v
		}
	}
	require.Equal(t, got, EasypaySign(without, key))
	// Changing a value changes the signature.
	values["money"] = "2.00"
	require.NotEqual(t, got, EasypaySign(values, key))
}

// Known-answer test so a refactor cannot silently change what the gateway
// receives: md5 of "money=1.00&name=VIP&out_trade_no=O1&pid=1005testkey".
func TestEasypaySignKnownAnswer(t *testing.T) {
	values := map[string]string{
		"pid":          "1005",
		"money":        "1.00",
		"name":         "VIP",
		"out_trade_no": "O1",
	}
	require.Equal(t,
		"5a0b903849384d19b147bf6f2e475694",
		EasypaySign(values, "testkey"))
}

func TestEasypayBuildCheckout(t *testing.T) {
	cfg := EasypayConfig{
		GatewayURL: "https://pay.example.com/", // trailing slash tolerated
		PID:        "1005",
		Key:        "testkey",
		PayType:    "alipay",
	}
	out, err := EasypayBuildCheckout(cfg, EasypayParams{
		OutTradeNo: "O1",
		NotifyURL:  "https://example.com/api/v4/callback/easypay/notify",
		ReturnURL:  "https://example.com/api/v4/callback/easypay/return",
		Name:       "VIP",
		Money:      "1.00",
	})
	require.NoError(t, err)
	require.Equal(t, "https://pay.example.com/submit.php", out.URL)
	require.Equal(t, "1005", out.Params["pid"])
	require.Equal(t, "alipay", out.Params["type"])
	require.Equal(t, "MD5", out.Params["sign_type"])
	require.Len(t, out.Params["sign"], 32)
	require.Equal(t, EasypaySign(withoutSign(out.Params), "testkey"), out.Params["sign"])

	// An explicit per-checkout type wins over the configured default.
	out2, err := EasypayBuildCheckout(cfg, EasypayParams{OutTradeNo: "O1", Name: "VIP", Money: "1.00", PayType: EasypayTypeWxpay})
	require.NoError(t, err)
	require.Equal(t, "wxpay", out2.Params["type"])

	// Cashier mode omits type entirely.
	cashier := EasypayConfig{GatewayURL: cfg.GatewayURL, PID: cfg.PID, Key: cfg.Key, PayType: EasypayTypeCashier}
	out3, err := EasypayBuildCheckout(cashier, EasypayParams{OutTradeNo: "O1", Name: "VIP", Money: "1.00"})
	require.NoError(t, err)
	_, hasType := out3.Params["type"]
	require.False(t, hasType)

	// Missing configuration is refused instead of producing an unsigned form.
	_, err = EasypayBuildCheckout(EasypayConfig{}, EasypayParams{OutTradeNo: "O1", Money: "1.00"})
	require.Error(t, err)
	_, err = EasypayBuildCheckout(cfg, EasypayParams{Money: "1.00"})
	require.Error(t, err)
}

func withoutSign(params map[string]string) map[string]string {
	out := make(map[string]string, len(params))
	for k, v := range params {
		out[k] = v
	}
	delete(out, "sign")
	return out
}

func signedNotify(key string, fields map[string]string) map[string]string {
	values := make(map[string]string, len(fields)+2)
	for k, v := range fields {
		values[k] = v
	}
	values["sign"] = EasypaySign(values, key)
	values["sign_type"] = "MD5"
	return values
}

func TestEasypayParseNotify(t *testing.T) {
	key := "testkey"
	fields := map[string]string{
		"pid":          "1005",
		"trade_no":     "E202608060001",
		"out_trade_no": "O1",
		"type":         "alipay",
		"name":         "VIP",
		"money":        "1.00",
		"trade_status": EasypayTradeSuccess,
	}
	values := signedNotify(key, fields)

	n, err := EasypayParseNotify(values, key)
	require.NoError(t, err)
	require.True(t, n.Paid())
	require.Equal(t, "O1", n.OutTradeNo)
	require.Equal(t, "E202608060001", n.TradeNo)

	// A tampered amount breaks the signature.
	tampered := signedNotify(key, fields)
	tampered["money"] = "0.01"
	_, err = EasypayParseNotify(tampered, key)
	require.Error(t, err)

	// An extra parameter injected without re-signing breaks it too.
	injected := signedNotify(key, fields)
	injected["extra"] = "x"
	_, err = EasypayParseNotify(injected, key)
	require.Error(t, err)

	// Wrong key and missing signature are rejected.
	_, err = EasypayParseNotify(values, "otherkey")
	require.Error(t, err)
	_, err = EasypayParseNotify(map[string]string{"out_trade_no": "O1"}, key)
	require.Error(t, err)
	// A non-MD5 signature type is refused rather than trusted.
	rsa := signedNotify(key, fields)
	rsa["sign_type"] = "RSA"
	_, err = EasypayParseNotify(rsa, key)
	require.Error(t, err)

	// A verified but unsuccessful status is not "paid".
	waiting := signedNotify(key, map[string]string{
		"out_trade_no": "O1", "trade_status": "WAIT_BUYER_PAY", "money": "1.00",
	})
	n, err = EasypayParseNotify(waiting, key)
	require.NoError(t, err)
	require.False(t, n.Paid())
}

func TestParseAndFormatMoney(t *testing.T) {
	cases := map[string]int64{
		"1.00":    100,
		"0.01":    1,
		"12.5":    1250,
		"99.99":   9999,
		"100":     10000,
		"0":       0,
		"1.005":   100, // third decimal truncates, matching the gateway's 2-dp cap
		" 2.00 ":  200,
	}
	for in, want := range cases {
		got, err := ParseMoneyCents(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "abc", "1.2.3", "x.y"} {
		_, err := ParseMoneyCents(bad)
		require.Error(t, err, bad)
	}
	require.Equal(t, "1.00", FormatMoneyCents(100))
	require.Equal(t, "0.01", FormatMoneyCents(1))
	require.Equal(t, "12.34", FormatMoneyCents(1234))
	require.Equal(t, "0.00", FormatMoneyCents(0))
}

// fakeGateway stubs the query API so settlement tests need no network.
type fakeGateway struct {
	paid   bool
	money  string
	err    error
	checks int
}

func (f *fakeGateway) BuildCheckout(cfg EasypayConfig, p EasypayParams) (*EasypayCheckout, error) {
	return EasypayBuildCheckout(cfg, p)
}
func (f *fakeGateway) ParseNotify(values map[string]string, key string) (*EasypayNotify, error) {
	return EasypayParseNotify(values, key)
}
func (f *fakeGateway) OrderPaid(context.Context, EasypayConfig, string) (bool, string, error) {
	f.checks++
	return f.paid, f.money, f.err
}

func swapEasypayGateway(t *testing.T, g EasypayGateway) {
	orig := Easypay
	Easypay = g
	t.Cleanup(func() { Easypay = orig })
}
