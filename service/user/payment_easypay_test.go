package user

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/paymentorder"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
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

func TestEasypayCheckoutReturnsPaymentDialog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	dep := newEasypayDep(t, client, easypayCfg())
	enc := mustHasher(t)

	fake := &stubGateway{qrcode: "weixin://wxpay/bizpayurl?pr=abc"}
	swapEasypayGateway(t, fake)

	res, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(
		paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.Equal(t, "easypay", res.Provider)
	// No form POST and no redirect: the dialog gets the QR payload instead.
	require.Empty(t, res.Method)
	require.Nil(t, res.Params)
	require.NotNil(t, res.Payment)
	require.Equal(t, "weixin://wxpay/bizpayurl?pr=abc", res.Payment.Qrcode)
	require.Empty(t, res.Payment.PayURL)
	require.Equal(t, int64(499), res.Amount)

	// The gateway must have been asked to create the order with the amount in
	// yuan and the order reference signed in.
	require.Equal(t, "4.99", fake.lastParams.Money)
	require.Equal(t, "https://example.com/api/v4/callback/easypay/notify", fake.lastParams.NotifyURL)
	// The merchant id is filled in by the gateway from the settings, not by
	// the caller; the params carry only what the caller decides.
	require.Empty(t, fake.lastParams.PID)

	order, err := client.PaymentOrder.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "easypay", order.Provider)
	require.Equal(t, paymentorder.StatusPending, order.Status)
	// The out_trade_no doubles as the session id so both the notification and
	// the status poll can find the order.
	require.Equal(t, order.SessionID, fake.lastParams.OutTradeNo)
	require.Equal(t, res.OrderID, fake.lastParams.Param)
}

// Channels without a QR payload (alipay/qqpay) surface only a cashier URL.
func TestEasypayCheckoutCashierFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	u, s := paymentFixtureSvc(t, client)
	dep := newEasypayDep(t, client, easypayCfg())
	enc := mustHasher(t)

	swapEasypayGateway(t, &stubGateway{payurl: "https://pay.example.com/pay/submit/1/"})

	res, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(
		paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.NotNil(t, res.Payment)
	require.Empty(t, res.Payment.Qrcode)
	require.Equal(t, "https://pay.example.com/pay/submit/1/", res.Payment.PayURL)
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
	// qrcode/payurl mirror what mapi.php hands back for the channel.
	qrcode string
	payurl string
	// lastParams records what the service asked the gateway to charge.
	lastParams payment.EasypayParams
}

func (s *stubGateway) CreateOrder(_ context.Context, _ payment.EasypayConfig, p payment.EasypayParams) (*payment.EasypayOrder, error) {
	s.lastParams = p
	if s.err != nil {
		return nil, s.err
	}
	return &payment.EasypayOrder{Qrcode: s.qrcode, PayURL: s.payurl}, nil
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

// Resuming an order must reuse the merchant reference so a buyer who left and
// came back is still paying the SAME order — never a second charge.
func TestEasypayResumeReusesOrderReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	dep := newEasypayDep(t, client, easypayCfg())
	enc := mustHasher(t)

	fake := &stubGateway{qrcode: "weixin://wxpay/bizpayurl?pr=first"}
	swapEasypayGateway(t, fake)

	first, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(
		paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	firstRef := fake.lastParams.OutTradeNo
	require.NotEmpty(t, firstRef)

	order, err := client.PaymentOrder.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, paymentorder.StatusPending, order.Status)

	// "Refresh and come back": resume through the order history.
	fake.qrcode = "weixin://wxpay/bizpayurl?pr=second"
	resumed, err := (&PaymentOrderResumeService{ID: first.OrderID}).Resume(paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.Equal(t, first.OrderID, resumed.OrderID)
	require.Equal(t, "weixin://wxpay/bizpayurl?pr=second", resumed.Payment.Qrcode)
	// The critical assertion: the same merchant order reference, so the
	// gateway treats it as one order.
	require.Equal(t, firstRef, fake.lastParams.OutTradeNo)
	require.Equal(t, order.SessionID, fake.lastParams.OutTradeNo)

	// No second order row was created.
	count, err := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

// Past its deadline the order can no longer be resumed, and it is persisted as
// expired rather than staying pending forever.
func TestEasypayResumeRejectsExpired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	cfg := easypayCfg()
	cfg.OrderTTLMinutes = 30
	dep := newEasypayDep(t, client, cfg)
	enc := mustHasher(t)

	swapEasypayGateway(t, &stubGateway{qrcode: "weixin://wxpay/bizpayurl?pr=x"})
	first, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(
		paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)

	// Age the order past the 30 minute window. created_at is maintained by the
	// ent mixin and cannot be Set through the generated builder, so backdate it
	// with raw SQL.
	order, err := client.PaymentOrder.Query().Only(ctx)
	require.NoError(t, err)
	backdateOrder(t, client, order.ID, 31*time.Minute)

	_, err = (&PaymentOrderResumeService{ID: first.OrderID}).Resume(paymentTestCtx(t, dep, u, nil))
	require.Error(t, err)

	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, paymentorder.StatusExpired, stored.Status)
}

// A payment that lands after the window closed must still be honoured — the
// money is real, so the order settles instead of being swallowed.
func TestEasypayLatePaymentSettlesExpiredOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	dep := newEasypayDep(t, client, easypayCfg())

	order, err := dep.VasClient().CreatePaymentOrder(ctx, u.ID, s.ID, "easypay", s.Price, "cny")
	require.NoError(t, err)
	require.NoError(t, dep.VasClient().BindPaymentSession(ctx, order.ID, "O-LATE"))
	backdateOrder(t, client, order.ID, 2*time.Hour)

	// The lazy expiry path closes it first...
	expired, err := dep.VasClient().ExpirePaymentOrder(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, expired)

	// ...and then the gateway notification arrives for that expired order.
	swapEasypayGateway(t, &stubGateway{paid: true, money: "4.99"})
	notifyCtx := easypayNotifyCtx(t, dep, "testkey", map[string]string{
		"pid": "1005", "trade_no": "T-LATE", "out_trade_no": "O-LATE",
		"type": "wxpay", "name": "Traffic", "money": "4.99",
		"trade_status": payment.EasypayTradeSuccess,
	})
	// The notify handler needs a response writer to read; the service itself
	// returns the body string.
	body, err := (&EasypayNotifyService{}).Create(notifyCtx)
	require.NoError(t, err)
	require.Equal(t, "success", body)

	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, paymentorder.StatusPaid, stored.Status)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)

	// Replaying the same notification does not grant twice.
	body, err = (&EasypayNotifyService{}).Create(easypayNotifyCtx(t, dep, "testkey", map[string]string{
		"pid": "1005", "trade_no": "T-LATE", "out_trade_no": "O-LATE",
		"type": "wxpay", "name": "Traffic", "money": "4.99",
		"trade_status": payment.EasypayTradeSuccess,
	}))
	require.NoError(t, err)
	require.Equal(t, "success", body)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)
}

// The history lists only the caller's own orders.
func TestPaymentOrderListIsScopedToUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	// A second user on the same group, to prove the list is scoped.
	g := client.Group.Query().FirstX(ctx)
	other := client.User.Create().SetEmail("other@example.com").SetNick("other").SetStatus("active").
		SetGroup(g).SetSettings(&types.UserSetting{}).SetStreamTraffic(0).SaveX(ctx)
	dep := newEasypayDep(t, client, easypayCfg())

	mine, err := dep.VasClient().CreatePaymentOrder(ctx, u.ID, s.ID, "easypay", s.Price, "cny")
	require.NoError(t, err)
	require.NoError(t, dep.VasClient().BindPaymentSession(ctx, mine.ID, "O-MINE"))
	theirs, err := dep.VasClient().CreatePaymentOrder(ctx, other.ID, s.ID, "easypay", s.Price, "cny")
	require.NoError(t, err)
	require.NoError(t, dep.VasClient().BindPaymentSession(ctx, theirs.ID, "O-THEIRS"))

	res, err := (&PaymentOrderListService{}).List(paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.Len(t, res.Orders, 1)
	require.Equal(t, "pending", res.Orders[0].Status)
	require.NotZero(t, res.Orders[0].ExpiresAt)
}

// backdateOrder moves an order's created_at into the past so the TTL path can
// be exercised without waiting.
func backdateOrder(t *testing.T, client *ent.Client, orderID int, age time.Duration) {
	t.Helper()
	_, err := client.PaymentOrder.Update().
		Where(paymentorder.ID(orderID)).
		SetUpdatedAt(time.Now().Add(-age)).
		Save(context.Background())
	require.NoError(t, err)
	// ent exposes no setter for created_at; use the underlying SQL handle.
	_, err = client.ExecContext(context.Background(),
		"UPDATE payment_orders SET created_at = ? WHERE id = ?", time.Now().Add(-age), orderID)
	require.NoError(t, err)
}

// reconcileFixture builds an order plus an admin to act on it.
func reconcileFixture(t *testing.T) (*ent.Client, dependency.Dep, *ent.User, *ent.User, *ent.PaymentOrder, string) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	u, s := paymentFixtureSvc(t, client)
	cfg := easypayCfg()
	cfg.OrderTTLMinutes = 30
	dep := newEasypayDep(t, client, cfg)
	enc := mustHasher(t)

	g := client.Group.Query().FirstX(ctx)
	admin := client.User.Create().SetEmail("admin@example.com").SetNick("admin").SetStatus("active").
		SetGroup(g).SetSettings(&types.UserSetting{}).SaveX(ctx)

	swapEasypayGateway(t, &stubGateway{qrcode: "weixin://wxpay/bizpayurl?pr=r"})
	res, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(
		paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	order, err := client.PaymentOrder.Query().Only(ctx)
	require.NoError(t, err)
	return client, dep, u, admin, order, res.OrderID
}

// A channel-confirmed reconciliation settles the order through the normal
// path and does NOT mark it manual — no audit exception is needed when the
// gateway vouches for the payment.
func TestAdminReconcileConfirmedByGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client, dep, u, admin, order, orderHash := reconcileFixture(t)
	ctx := context.Background()

	swapEasypayGateway(t, &stubGateway{paid: true, money: "4.99"})
	res, err := (&AdminReconcilePaymentOrderService{ID: orderHash, Reason: "buyer sent a receipt"}).
		Reconcile(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.True(t, res.Confirmed)
	require.True(t, res.Fulfilled)
	require.Equal(t, "paid", res.Order.Status)
	require.False(t, res.Order.Manual)

	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, paymentorder.StatusPaid, stored.Status)
	require.False(t, stored.Manual)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)
}

// Without channel confirmation, nothing is granted unless the admin forces it,
// and a forced settlement carries the operator and reason.
func TestAdminReconcileForceRecordsAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client, dep, u, admin, order, orderHash := reconcileFixture(t)
	ctx := context.Background()

	// Gateway says unpaid: a plain reconcile is refused and grants nothing.
	swapEasypayGateway(t, &stubGateway{paid: false})
	res, err := (&AdminReconcilePaymentOrderService{ID: orderHash, Reason: "call with the buyer"}).
		Reconcile(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.False(t, res.Confirmed)
	require.False(t, res.Fulfilled)
	require.Equal(t, "pending", res.Order.Status)
	require.Equal(t, int64(0), client.User.GetX(ctx, u.ID).StreamTraffic)

	// Forced: settles, flags the order, records who and why.
	res, err = (&AdminReconcilePaymentOrderService{ID: orderHash, Reason: "call with the buyer", Force: true}).
		Reconcile(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.True(t, res.Fulfilled)
	require.Equal(t, "paid", res.Order.Status)
	require.True(t, res.Order.Manual)
	require.Equal(t, "call with the buyer", res.Order.ManualReason)
	require.NotEmpty(t, res.Order.ManualByNick)

	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, stored.Manual)
	require.Equal(t, admin.ID, *stored.ManualBy)
	require.Equal(t, "call with the buyer", *stored.ManualReason)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)

	// Repeat: refused, and the balance is unchanged — no double grant.
	res, err = (&AdminReconcilePaymentOrderService{ID: orderHash, Reason: "again", Force: true}).
		Reconcile(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.False(t, res.Fulfilled)
	require.Equal(t, "Order already paid", res.Message)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)
}

// An expired order whose payment really did arrive is the main reason this
// feature exists, so reconciliation must settle it.
func TestAdminReconcileExpiredOrderAfterPayment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client, dep, u, admin, order, orderHash := reconcileFixture(t)
	ctx := context.Background()

	// Past the 30 minute window, and the lazy expiry has already closed it.
	backdateOrder(t, client, order.ID, 2*time.Hour)
	expired, err := dep.VasClient().ExpirePaymentOrder(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, expired)

	swapEasypayGateway(t, &stubGateway{paid: true, money: "4.99"})
	res, err := (&AdminReconcilePaymentOrderService{ID: orderHash, Reason: "late settlement"}).
		Reconcile(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.True(t, res.Confirmed)
	require.True(t, res.Fulfilled)
	require.Equal(t, "paid", res.Order.Status)
	require.Equal(t, int64(2048), client.User.GetX(ctx, u.ID).StreamTraffic)
}

// A reason is mandatory: nobody may settle an order without saying why.
func TestAdminReconcileRequiresReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, dep, _, admin, _, orderHash := reconcileFixture(t)
	swapEasypayGateway(t, &stubGateway{paid: true, money: "4.99"})

	_, err := (&AdminReconcilePaymentOrderService{ID: orderHash}).
		Reconcile(paymentTestCtx(t, dep, admin, nil))
	require.Error(t, err)
}

// The admin list honours the filters and is not scoped to one user.
func TestAdminListPaymentOrdersFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client, dep, u, admin, order, _ := reconcileFixture(t)
	ctx := context.Background()

	res, err := (&AdminPaymentOrderListService{}).List(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.Equal(t, u.Email, res.Orders[0].UserEmail)

	// Filter by status.
	res, err = (&AdminPaymentOrderListService{Status: "paid"}).List(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, 0, res.Total)

	res, err = (&AdminPaymentOrderListService{Status: "pending"}).List(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)

	// Filter by user.
	enc := mustHasher(t)
	res, err = (&AdminPaymentOrderListService{UserID: hashid.EncodeUserID(enc, admin.ID)}).
		List(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, 0, res.Total)

	// The same box also accepts the buyer's email, which is the half an
	// operator usually has to hand. A partial address narrows the list; the
	// match is case-insensitive.
	res, err = (&AdminPaymentOrderListService{UserID: u.Email}).List(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.Equal(t, u.Email, res.Orders[0].UserEmail)

	res, err = (&AdminPaymentOrderListService{UserID: strings.ToUpper(u.Email)}).
		List(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)

	res, err = (&AdminPaymentOrderListService{UserID: u.Email[:3]}).List(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)

	// A bare numeric ID is accepted too, so the operator can paste one.
	res, err = (&AdminPaymentOrderListService{UserID: strconv.Itoa(u.ID)}).List(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)

	// An unmatched search is an empty list, not an error: the operator may
	// have typed an address that simply has no orders.
	res, err = (&AdminPaymentOrderListService{UserID: "nobody@example.invalid"}).
		List(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, 0, res.Total)

	// Filter by creation time.
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	res, err = (&AdminPaymentOrderListService{CreatedAfter: future}).List(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, 0, res.Total)

	// Reading the admin list must not settle anything: stored state is shown.
	stored, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, paymentorder.StatusPending, stored.Status)
}

// The admin detail view has to identify the buyer: an order's audit value
// collapses if the operator cannot see who it belongs to.
func TestAdminPaymentOrderDetailShowsBuyer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, dep, u, admin, _, orderHash := reconcileFixture(t)

	res, err := (&AdminPaymentOrderService{ID: orderHash}).Get(paymentTestCtx(t, dep, admin, nil))
	require.NoError(t, err)
	require.Equal(t, u.Email, res.UserEmail)
	require.Equal(t, u.Nick, res.UserNick)
	require.NotEmpty(t, res.UserID)
	require.NotEmpty(t, res.SkuName)
}

// The smallest convertible price must survive the whole cash path: one minor
// unit in a cent-style currency reaches 易支付 as "0.01" and the notification
// carrying that amount fulfils the order.
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

	fake := &stubGateway{qrcode: "weixin://wxpay/bizpayurl?pr=cheap"}
	swapEasypayGateway(t, fake)

	_, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(
		paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	// The gateway is charged the smallest price, not a rounded-up one.
	require.Equal(t, "0.01", fake.lastParams.Money)

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

	fake := &stubGateway{qrcode: "weixin://wxpay/bizpayurl?pr=unit"}
	swapEasypayGateway(t, fake)

	_, err := (&CheckoutSessionService{Sku: hashid.EncodeSkuID(enc, s.ID), Provider: "easypay"}).Create(
		paymentTestCtx(t, dep, u, nil))
	require.NoError(t, err)
	require.Equal(t, "15.00", fake.lastParams.Money)
}
