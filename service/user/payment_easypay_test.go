package user

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/paymentorder"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/payment"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// easypaySettingProvider returns payment settings with both channels off; each
// test flips on what it needs.
func newEasypayDep(t *testing.T, client *ent.Client, cfg *setting.PaymentSettings) dependency.Dep {
	t.Helper()
	dep, _ := newPaymentDep(t, client, false)
	sp := &paymentSettingProvider{payment: cfg}
	// Rebuild the dependency over the same plumbing but with our setting
	// provider, mirroring newPaymentDep's wiring.
	return dependency.NewDependency(
		dependency.WithDbClient(client),
		dependency.WithConfigProvider(dep.ConfigProvider()),
		dependency.WithLogger(dep.Logger()),
		dependency.WithKV(dep.KV()),
		dependency.WithSettingProvider(sp),
		dependency.WithHashIDEncoder(dep.HashIDEncoder()),
	)
}

func easypayCfg() *setting.PaymentSettings {
	return &setting.PaymentSettings{
		Currency:     "cny",
		CurrencyMark: "¥",
		CurrencyUnit: 100,
		Easypay: setting.EasypaySettings{
			Enabled:    true,
			GatewayURL: "https://pay.example.com",
			PID:        "1005",
			Key:        "testkey",
			PayType:    "alipay",
		},
	}
}

// easypayNotifyCtx builds a GET context carrying a signed notification.
func easypayNotifyCtx(t *testing.T, dep dependency.Dep, key string, fields map[string]string) *gin.Context {
	t.Helper()
	values := map[string]string{}
	for k, v := range fields {
		values[k] = v
	}
	values["sign"] = payment.EasypaySign(values, key)
	values["sign_type"] = "MD5"
	q := url.Values{}
	for k, v := range values {
		q.Set(k, v)
	}
	engine := gin.New()
	engine.ContextWithFallback = true
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), engine)
	req := httptest.NewRequest("GET", "/api/v4/callback/easypay/notify?"+q.Encode(), nil)
	c.Request = req
	util.WithValue(c, dependency.DepCtx{}, dep)
	return c
}

func TestEasypayCheckoutBuildsSignedForm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	dep := newEasypayDep(t, client, easypayCfg())
	enc := mustHasher(t)

	res, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(
		paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.Equal(t, "easypay", res.Provider)
	require.Equal(t, "POST", res.Method)
	require.Equal(t, "https://pay.example.com/submit.php", res.URL)
	require.Equal(t, "1005", res.Params["pid"])
	require.Equal(t, "4.99", res.Params["money"])
	require.Len(t, res.Params["sign"], 32)
	// The signature must verify over exactly what the browser will POST.
	flat := map[string]string{}
	for k, v := range res.Params {
		flat[k] = v
	}
	require.Equal(t, payment.EasypaySign(flat, "testkey"), res.Params["sign"])

	order, err := client.PaymentOrder.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "easypay", order.Provider)
	require.Equal(t, paymentorder.StatusPending, order.Status)
	// The out_trade_no doubles as the session id so the notification can find
	// the order.
	require.Equal(t, order.SessionID, res.Params["out_trade_no"])
	require.Equal(t, res.Params["notify_url"], "https://example.com/api/v4/callback/easypay/notify")
}

func TestEasypayCheckoutRequiresChoiceWhenBothEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	u, s := paymentFixtureSvc(t, client)
	cfg := easypayCfg()
	cfg.Enabled, cfg.SecretKey = true, "sk_test_x"
	dep := newEasypayDep(t, client, cfg)
	enc := mustHasher(t)

	// Ambiguous: two channels are live and none was named.
	_, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID)}).Create(paymentTestCtx(t, dep, u, nil))
	require.Error(t, err)

	// Naming an unconfigured channel is refused.
	cfg2 := easypayCfg()
	cfg2.Easypay = setting.EasypaySettings{}
	dep2 := newEasypayDep(t, client, cfg2)
	_, err = (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(paymentTestCtx(t, dep2, u, nil))
	require.Error(t, err)
}

func TestEasypayNotifyFulfillsOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	cfg := easypayCfg()
	dep := newEasypayDep(t, client, cfg)

	order, err := dep.VasClient().CreatePaymentOrder(ctx, u.ID, s.ID, "easypay", s.Price, "cny")
	require.NoError(t, err)
	require.NoError(t, dep.VasClient().BindPaymentSession(ctx, order.ID, "O-1"))

	fields := map[string]string{
		"pid":          "1005",
		"trade_no":     "E-1",
		"out_trade_no": "O-1",
		"type":         "alipay",
		"name":         "Stream",
		"money":        "4.99",
		"trade_status": payment.EasypayTradeSuccess,
	}
	svc := &EasypayNotifyService{}
	res, err := svc.Create(easypayNotifyCtx(t, dep, cfg.Easypay.Key, fields))
	require.NoError(t, err)
	require.Equal(t, "success", res)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)
	require.Equal(t, paymentorder.StatusPaid, client.PaymentOrder.GetX(ctx, order.ID).Status)

	// Gateway retries the same notification: acknowledged, no second grant.
	res, err = svc.Create(easypayNotifyCtx(t, dep, cfg.Easypay.Key, fields))
	require.NoError(t, err)
	require.Equal(t, "success", res)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)
}

func TestEasypayNotifyRejections(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	cfg := easypayCfg()
	dep := newEasypayDep(t, client, cfg)

	order, err := dep.VasClient().CreatePaymentOrder(ctx, u.ID, s.ID, "easypay", s.Price, "cny")
	require.NoError(t, err)
	require.NoError(t, dep.VasClient().BindPaymentSession(ctx, order.ID, "O-2"))
	svc := &EasypayNotifyService{}

	base := map[string]string{
		"pid":          "1005",
		"out_trade_no": "O-2",
		"name":         "Stream",
		"money":        "4.99",
		"trade_status": payment.EasypayTradeSuccess,
	}

	// Wrong signing key (forged notification).
	_, err = svc.Create(easypayNotifyCtx(t, dep, "wrongkey", base))
	require.Error(t, err)
	// Signed correctly but for a lower amount than the order.
	cheap := map[string]string{}
	for k, v := range base {
		cheap[k] = v
	}
	cheap["money"] = "0.01"
	_, err = svc.Create(easypayNotifyCtx(t, dep, cfg.Easypay.Key, cheap))
	require.Error(t, err)
	// Signed correctly but from another merchant account.
	foreign := map[string]string{}
	for k, v := range base {
		foreign[k] = v
	}
	foreign["pid"] = "9999"
	_, err = svc.Create(easypayNotifyCtx(t, dep, cfg.Easypay.Key, foreign))
	require.Error(t, err)
	// Nothing above granted anything.
	require.Equal(t, int64(0), client.User.GetX(ctx, u.ID).StreamTraffic)
	require.Equal(t, paymentorder.StatusPending, client.PaymentOrder.GetX(ctx, order.ID).Status)

	// A genuine but non-success status is acknowledged without granting.
	waiting := map[string]string{}
	for k, v := range base {
		waiting[k] = v
	}
	waiting["trade_status"] = "WAIT_BUYER_PAY"
	res, err := svc.Create(easypayNotifyCtx(t, dep, cfg.Easypay.Key, waiting))
	require.NoError(t, err)
	require.Equal(t, "success", res)
	require.Equal(t, int64(0), client.User.GetX(ctx, u.ID).StreamTraffic)

	// Unknown order reference is refused so the gateway retries / alerts.
	unknown := map[string]string{}
	for k, v := range base {
		unknown[k] = v
	}
	unknown["out_trade_no"] = "nope"
	_, err = svc.Create(easypayNotifyCtx(t, dep, cfg.Easypay.Key, unknown))
	require.Error(t, err)
}

// The status poll must settle a pending 易支付 order even when the
// asynchronous notification never arrives, and must not settle on an amount
// that does not match what was charged.
func TestEasypayOrderPollSettles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	cfg := easypayCfg()
	dep := newEasypayDep(t, client, cfg)
	enc := mustHasher(t)

	fake := &stubGateway{paid: true, money: "4.99"}
	swapEasypayGateway(t, fake)

	order, err := dep.VasClient().CreatePaymentOrder(ctx, u.ID, s.ID, "easypay", s.Price, "cny")
	require.NoError(t, err)
	require.NoError(t, dep.VasClient().BindPaymentSession(ctx, order.ID, "O-3"))
	orderHash := hashid.EncodePaymentID(enc, order.ID)

	res, err := (&PaymentOrderService{ID: orderHash}).Get(paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.Equal(t, "paid", res.Status)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)

	// A gateway reporting an amount that does not match leaves the order
	// pending instead of granting.
	order2, err := dep.VasClient().CreatePaymentOrder(ctx, u.ID, s.ID, "easypay", s.Price, "cny")
	require.NoError(t, err)
	require.NoError(t, dep.VasClient().BindPaymentSession(ctx, order2.ID, "O-4"))
	fake.money = "0.01"
	res, err = (&PaymentOrderService{ID: hashid.EncodePaymentID(enc, order2.ID)}).Get(paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.Equal(t, "pending", res.Status)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)

	// An unpaid order stays pending.
	order3, err := dep.VasClient().CreatePaymentOrder(ctx, u.ID, s.ID, "easypay", s.Price, "cny")
	require.NoError(t, err)
	require.NoError(t, dep.VasClient().BindPaymentSession(ctx, order3.ID, "O-5"))
	fake.paid, fake.money = false, "4.99"
	_, err = (&PaymentOrderService{ID: hashid.EncodePaymentID(enc, order3.ID)}).Get(paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.Equal(t, paymentorder.StatusPending, client.PaymentOrder.GetX(ctx, order3.ID).Status)
}

// The browser return redirects into the shop with the order attached, and
// settles a paid order on the way.
func TestEasypayReturnRedirects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	cfg := easypayCfg()
	dep := newEasypayDep(t, client, cfg)
	enc := mustHasher(t)

	order, err := dep.VasClient().CreatePaymentOrder(ctx, u.ID, s.ID, "easypay", s.Price, "cny")
	require.NoError(t, err)
	require.NoError(t, dep.VasClient().BindPaymentSession(ctx, order.ID, "O-6"))

	c := easypayNotifyCtx(t, dep, cfg.Easypay.Key, map[string]string{
		"pid": "1005", "out_trade_no": "O-6", "money": "4.99",
		"trade_status": payment.EasypayTradeSuccess,
	})
	target := (&EasypayReturnService{}).Return(c)
	want := "https://example.com/shop?checkout=" + hashid.EncodePaymentID(enc, order.ID)
	require.Equal(t, want, target)
	require.Equal(t, paymentorder.StatusPaid, client.PaymentOrder.GetX(ctx, order.ID).Status)

	// An unsigned (or forged) return lands on the plain shop page instead of
	// leaking the order reference.
	bare := easypayNotifyCtx(t, dep, "wrongkey", map[string]string{"out_trade_no": "O-6", "money": "4.99"})
	require.Equal(t, "https://example.com/shop", (&EasypayReturnService{}).Return(bare))
}

// stubGateway is a local EasypayGateway stub for settlement tests.
type stubGateway struct {
	paid  bool
	money string
	err   error
}

func (s *stubGateway) BuildCheckout(cfg payment.EasypayConfig, p payment.EasypayParams) (*payment.EasypayCheckout, error) {
	return payment.EasypayBuildCheckout(cfg, p)
}
func (s *stubGateway) ParseNotify(values map[string]string, key string) (*payment.EasypayNotify, error) {
	return payment.EasypayParseNotify(values, key)
}
func (s *stubGateway) OrderPaid(context.Context, payment.EasypayConfig, string) (bool, string, error) {
	return s.paid, s.money, s.err
}

func swapEasypayGateway(t *testing.T, g payment.EasypayGateway) {
	orig := payment.Easypay
	payment.Easypay = g
	t.Cleanup(func() { payment.Easypay = orig })
}

// A product priced 0.01 has to survive the whole cash path: the order stores
// one minor unit, 易支付 is charged "0.01", and the notification carrying that
// same amount fulfils the order. Before the price input accepted decimals the
// smallest reachable price was a whole currency unit, so this path had no test.
func TestEasypaySmallestPriceCheckoutAndNotify(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	// One minor unit in a cent-style currency = 0.01.
	s = client.Sku.UpdateOne(s).SetPrice(1).SaveX(ctx)
	cfg := easypayCfg()
	dep := newEasypayDep(t, client, cfg)
	enc := mustHasher(t)

	res, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(
		paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.Equal(t, "0.01", res.Params["money"])

	order, err := client.PaymentOrder.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), order.Amount)

	fields := map[string]string{
		"pid":          "1005",
		"trade_no":     "E-cheap",
		"out_trade_no": order.SessionID,
		"type":         "alipay",
		"name":         "Stream",
		"money":        "0.01",
		"trade_status": payment.EasypayTradeSuccess,
	}
	svc := &EasypayNotifyService{}
	out, err := svc.Create(easypayNotifyCtx(t, dep, cfg.Easypay.Key, fields))
	require.NoError(t, err)
	require.Equal(t, "success", out)
	require.Equal(t, paymentorder.StatusPaid, client.PaymentOrder.GetX(ctx, order.ID).Status)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)
}

// The charge handed to the gateway follows the site currency's minor unit, so
// a zero-decimal currency (unit 1) is not charged 100x too little.
func TestEasypayCheckoutHonoursCurrencyUnit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	s = client.Sku.UpdateOne(s).SetPrice(15).SaveX(ctx) // 15 of the smallest unit
	cfg := easypayCfg()
	cfg.CurrencyUnit = 1
	dep := newEasypayDep(t, client, cfg)
	enc := mustHasher(t)

	res, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(
		paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.Equal(t, "15.00", res.Params["money"])
}
