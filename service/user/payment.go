package user

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/paymentorder"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/payment"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
)

const (
	// stripeCustomerKVPrefix caches the Stripe customer id per user so
	// checkout sessions reuse one provider-side customer.
	stripeCustomerKVPrefix = "payment:stripe:customer:"
	stripeProvider         = "stripe"
	easypayProvider        = "easypay"
	// defaultOrderTTL is how long an unpaid cash order stays payable when the
	// setting is missing.
	defaultOrderTTL = 30 * time.Minute
	// paymentOrderMaxPageSize caps one page of the order history.
	paymentOrderMaxPageSize = 50
)

type (
	// CheckoutSessionService opens a cash checkout for a SKU. Provider picks
	// the channel ("stripe" or "easypay"); empty uses the only enabled one.
	CheckoutSessionService struct {
		Sku      string `json:"sku" form:"sku" binding:"required,max=64"`
		Provider string `json:"provider" form:"provider" binding:"max=32"`
	}
	CheckoutSessionParamCtx struct{}

	// PaymentOrderService reports one of the caller's payment orders. When
	// the order is still pending it also asks the provider for the session
	// state and settles the order locally — covers deployments where the
	// webhook cannot reach the server.
	PaymentOrderService struct {
		ID string `uri:"id" binding:"required,max=64"`
	}
	PaymentOrderParamCtx struct{}

	// PaymentOrderListService pages the caller's own payment orders so the
	// shop can show an order history with their settlement state.
	PaymentOrderListService struct {
		Page     int `form:"page" json:"page"`
		PageSize int `form:"page_size" json:"page_size"`
	}
	PaymentOrderListParamCtx struct{}

	// PaymentOrderResumeService re-opens a still-payable order: it re-asks the
	// gateway for a payment handle for the SAME merchant order reference, so
	// the buyer can pick up where they left off without a second order and
	// without any risk of paying twice for two orders.
	PaymentOrderResumeService struct {
		ID string `uri:"id" binding:"required,max=64"`
	}
	PaymentOrderResumeParamCtx struct{}

	// AdminPaymentOrderListService pages every user's payment orders for the
	// admin console, optionally filtered by buyer, status and creation time.
	AdminPaymentOrderListService struct {
		Page          int    `form:"page" json:"page"`
		PageSize      int    `form:"page_size" json:"page_size"`
		UserID        string `form:"user_id" json:"user_id"`
		Status        string `form:"status" json:"status"`
		CreatedAfter  string `form:"created_after" json:"created_after"`
		CreatedBefore string `form:"created_before" json:"created_before"`
	}
	AdminPaymentOrderListParamCtx struct{}

	// AdminPaymentOrderService returns one order with the audit detail the
	// console needs (gateway trade reference, manual note).
	AdminPaymentOrderService struct {
		ID string `uri:"id" binding:"required,max=64"`
	}
	AdminPaymentOrderParamCtx struct{}

	// AdminReconcilePaymentOrderService settles an order by hand. It queries
	// the channel first; the admin may force it through when no query can
	// confirm the payment, which then records who did it and why.
	AdminReconcilePaymentOrderService struct {
		ID     string `uri:"id" binding:"required,max=64"`
		Reason string `json:"reason"`
		Force  bool   `json:"force"`
	}
	AdminReconcilePaymentOrderParamCtx struct{}

	// AdminPaymentOrderResponse is one row of the admin order list.
	AdminPaymentOrderResponse struct {
		ID        string `json:"id"`
		UserID    string `json:"user_id"`
		UserNick  string `json:"user_nick"`
		UserEmail string `json:"user_email"`
		SkuID     string `json:"sku_id"`
		SkuName   string `json:"sku_name"`
		Provider  string `json:"provider"`
		Amount    int64  `json:"amount"`
		Currency  string `json:"currency"`
		Status    string `json:"status"`
		CreatedAt int64  `json:"created_at"`
		PaidAt    int64  `json:"paid_at,omitempty"`
		ExpiresAt int64  `json:"expires_at,omitempty"`
		// Manual marks an order settled by hand; ManualBy/ManualReason are the
		// audit trail of that decision.
		Manual       bool   `json:"manual"`
		ManualByNick string `json:"manual_by_nick,omitempty"`
		ManualReason string `json:"manual_reason,omitempty"`
	}

	AdminPaymentOrderListResponse struct {
		Orders   []*AdminPaymentOrderResponse `json:"orders"`
		Total    int                          `json:"total"`
		Page     int                          `json:"page"`
		PageSize int                          `json:"page_size"`
	}

	// AdminReconcileResponse reports what the reconciliation settled on.
	AdminReconcileResponse struct {
		Order *AdminPaymentOrderResponse `json:"order"`
		// Confirmed is true when the channel itself reported the order paid.
		Confirmed bool `json:"confirmed"`
		// Fulfilled is false when the order was already settled — nothing was
		// granted twice.
		Fulfilled bool   `json:"fulfilled"`
		Message   string `json:"message"`
	}

	// StripeWebhookService ingests Stripe webhook events. The payload is
	// only a trigger: the session is re-fetched from Stripe before any
	// fulfillment happens.
	StripeWebhookService  struct{}
	StripeWebhookParamCtx struct{}

	// EasypayNotifyService ingests the asynchronous GET notification of the
	// 易支付 gateway. The payload is signature-verified and its amount is
	// compared against the order before fulfillment.
	EasypayNotifyService struct{}

	// EasypayReturnService handles the buyer's browser coming back from the
	// gateway. It verifies the signature, settles the order when the payment
	// already succeeded, and redirects to the shop; nothing is granted at
	// return time — the notification or the status poll is authoritative.
	EasypayReturnService struct{}

	CheckoutResponse struct {
		OrderID string `json:"order_id"`
		URL     string `json:"url"`
		// Provider is the channel the order was created on.
		Provider string `json:"provider"`
		// Method/Params carry a form POST when the channel needs one: the
		// browser POSTs Params to URL (易支付 submit.php). Both are empty for
		// redirect-style channels, where the caller just navigates to URL.
		Method string            `json:"method,omitempty"`
		Params map[string]string `json:"params,omitempty"`
		// Payment carries what an in-page payment dialog needs (易支付): the QR
		// payload when the channel returns one, otherwise the cashier URL to
		// open. Absent for redirect-style channels.
		Payment *CheckoutPayment `json:"payment,omitempty"`
		// Amount/Currency describe what is being charged, for the dialog.
		Amount   int64  `json:"amount,omitempty"`
		Currency string `json:"currency,omitempty"`
		// ExpiresAt is the unix deadline of the order (0 when none).
		ExpiresAt int64 `json:"expires_at,omitempty"`
	}

	// CheckoutPayment is the payable handle shown in the payment dialog.
	// Qrcode is a real QR payload (wxpay); PayURL is a cashier page that has
	// no QR payload behind it (alipay/qqpay).
	CheckoutPayment struct {
		Qrcode string `json:"qrcode,omitempty"`
		PayURL string `json:"pay_url,omitempty"`
		// URLScheme is a mini-program deep link (urlscheme), when returned.
		URLScheme string `json:"url_scheme,omitempty"`
	}

	PaymentOrderResponse struct {
		ID        string `json:"id"`
		SkuID     string `json:"sku_id"`
		SkuName   string `json:"sku_name"`
		Provider  string `json:"provider"`
		Amount    int64  `json:"amount"`
		Currency  string `json:"currency"`
		Status    string `json:"status"`
		CreatedAt int64  `json:"created_at"`
		// PaidAt is when the order settled, 0 while it has not.
		PaidAt int64 `json:"paid_at,omitempty"`
		// ExpiresAt is when an unpaid order stops being payable (0 once
		// settled or for orders that carry no deadline).
		ExpiresAt int64 `json:"expires_at,omitempty"`
	}

	// PaymentOrderListResponse is one page of the caller's orders.
	PaymentOrderListResponse struct {
		Orders   []*PaymentOrderResponse `json:"orders"`
		Total    int                     `json:"total"`
		Page     int                     `json:"page"`
		PageSize int                     `json:"page_size"`
	}
)

// enabledProviders lists the cash channels that are fully configured.
func enabledProviders(cfg *setting.PaymentSettings) []string {
	var out []string
	if cfg.Enabled && cfg.SecretKey != "" {
		out = append(out, stripeProvider)
	}
	if cfg.Easypay.Ready() {
		out = append(out, easypayProvider)
	}
	return out
}

func (service *CheckoutSessionService) Create(c *gin.Context) (*CheckoutResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)

	cfg := dep.SettingProvider().Payment(c)
	available := enabledProviders(cfg)
	provider := strings.ToLower(strings.TrimSpace(service.Provider))
	if provider == "" {
		if len(available) != 1 {
			if len(available) == 0 {
				return nil, serializer.NewError(serializer.CodeNotFound, "Cash checkout is not enabled", nil)
			}
			return nil, serializer.NewError(serializer.CodeParamErr, "A payment provider must be chosen", nil)
		}
		provider = available[0]
	}
	if !contains(available, provider) {
		return nil, serializer.NewError(serializer.CodeNotFound, "Payment provider is not available", nil)
	}

	skuID, err := dep.HashIDEncoder().Decode(service.Sku, hashid.SkuID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "Invalid product", err)
	}
	s, err := dep.VasClient().GetSku(c, skuID)
	if err != nil || !s.Enabled {
		return nil, serializer.NewError(serializer.CodeNotFound, "Product not found", err)
	}
	if s.Price <= 0 {
		return nil, serializer.NewError(serializer.CodeParamErr, "Product is not purchasable with cash", nil)
	}

	if provider == easypayProvider {
		return createEasypayCheckout(c, dep, cfg, u, s)
	}
	return createStripeCheckout(c, dep, cfg, u, s)
}

func createStripeCheckout(c *gin.Context, dep dependency.Dep, cfg *setting.PaymentSettings, u *ent.User, s *ent.Sku) (*CheckoutResponse, error) {
	customerID, err := ensureStripeCustomer(c, dep, cfg, u)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to create payment customer", err)
	}

	order, err := dep.VasClient().CreatePaymentOrder(c, u.ID, s.ID, stripeProvider, s.Price, cfg.Currency)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to create payment order", err)
	}

	siteURL := dep.SettingProvider().SiteURL(c).String()
	orderHash := hashid.EncodePaymentID(dep.HashIDEncoder(), order.ID)
	session, err := payment.Stripe.CreateCheckoutSession(c, cfg.SecretKey, payment.CheckoutParams{
		OrderID:     order.ID,
		ProductName: s.Name,
		Amount:      s.Price,
		Currency:    cfg.Currency,
		SuccessURL:  fmt.Sprintf("%s/shop?checkout=%s", siteURL, orderHash),
		CancelURL:   fmt.Sprintf("%s/shop", siteURL),
		CustomerID:  customerID,
	})
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to create checkout session", err)
	}
	if err := dep.VasClient().BindPaymentSession(c, order.ID, session.SessionID); err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to bind checkout session", err)
	}

	return &CheckoutResponse{OrderID: orderHash, URL: session.URL, Provider: stripeProvider}, nil
}

// createEasypayCheckout records the order and asks the gateway for a payable
// handle the storefront shows in an in-page dialog (QR code, or a cashier URL
// for channels that have no QR payload). 易支付 settles only in CNY, so the
// order amount is converted from the site currency unit into yuan.
func createEasypayCheckout(c *gin.Context, dep dependency.Dep, cfg *setting.PaymentSettings, u *ent.User, s *ent.Sku) (*CheckoutResponse, error) {
	order, err := dep.VasClient().CreatePaymentOrder(c, u.ID, s.ID, easypayProvider, s.Price, cfg.Currency)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to create payment order", err)
	}

	// out_trade_no must be unique per gateway account; the order id is unique
	// and the timestamp keeps a re-issued checkout from colliding with an
	// earlier attempt's reference.
	outTradeNo := fmt.Sprintf("%d-%d", order.ID, order.CreatedAt.Unix())
	if err := dep.VasClient().BindPaymentSession(c, order.ID, outTradeNo); err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to bind checkout session", err)
	}

	siteURL := strings.TrimRight(dep.SettingProvider().SiteURL(c).String(), "/")
	orderHash := hashid.EncodePaymentID(dep.HashIDEncoder(), order.ID)

	// mapi.php returns a payable handle the storefront renders in a dialog
	// (a QR payload for wxpay, a cashier URL for alipay/qqpay) instead of
	// sending the browser to the gateway. The notification is only one of two
	// settlement paths now; the order status poll queries the gateway directly.
	remote, err := payment.Easypay.CreateOrder(c, easypayConfig(cfg), payment.EasypayParams{
		PayType:    cfg.Easypay.PayType,
		OutTradeNo: outTradeNo,
		NotifyURL:  fmt.Sprintf("%s/api/v4/callback/easypay/notify", siteURL),
		ReturnURL:  fmt.Sprintf("%s/api/v4/callback/easypay/return", siteURL),
		Name:       s.Name,
		Money:      payment.FormatMoneyCents(order.Amount),
		Param:      orderHash,
		ClientIP:   c.ClientIP(),
	})
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to create checkout session", err)
	}

	return &CheckoutResponse{
		OrderID:  orderHash,
		Provider: easypayProvider,
		Payment: &CheckoutPayment{
			Qrcode:    remote.Qrcode,
			PayURL:    remote.PayURL,
			URLScheme: remote.URLScheme,
		},
		Amount:    order.Amount,
		Currency:  order.Currency,
		ExpiresAt: orderExpiresAt(cfg, order).Unix(),
	}, nil
}

// easypayConfig narrows the settings to what a gateway call needs.
func easypayConfig(cfg *setting.PaymentSettings) payment.EasypayConfig {
	return payment.EasypayConfig{
		GatewayURL: cfg.Easypay.GatewayURL,
		PID:        cfg.Easypay.PID,
		Key:        cfg.Easypay.Key,
		PayType:    cfg.Easypay.PayType,
	}
}

func (service *PaymentOrderService) Get(c *gin.Context) (*PaymentOrderResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)

	orderID, err := dep.HashIDEncoder().Decode(service.ID, hashid.PaymentID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "Invalid order id", err)
	}
	order, err := dep.VasClient().PaymentOrder(c, int(orderID))
	if err != nil {
		if errors.Is(err, inventory.ErrPaymentOrderNotFound) {
			return nil, serializer.NewError(serializer.CodeNotFound, "Order not found", err)
		}
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to get order", err)
	}
	if order.UserID != u.ID {
		return nil, serializer.NewError(serializer.CodeNotFound, "Order not found", nil)
	}

	// Lazy expiry: an unpaid order past its deadline reports (and is persisted
	// as) expired. Settlement is still attempted first, because a payment made
	// inside the window must not be swallowed just because the notification
	// never arrived.
	if err := settlePaymentOrder(c, dep, order); err != nil {
		return nil, err
	}
	if err := expireIfLapsed(c, dep, order); err != nil {
		return nil, err
	}
	return paymentOrderResponse(dep, dep.SettingProvider().Payment(c), order), nil
}

// orderTTL returns how long an unpaid cash order stays payable.
func orderTTL(cfg *setting.PaymentSettings) time.Duration {
	if cfg.OrderTTLMinutes <= 0 {
		return defaultOrderTTL
	}
	return time.Duration(cfg.OrderTTLMinutes) * time.Minute
}

// orderExpiresAt is the deadline of a pending order, zero for settled ones.
func orderExpiresAt(cfg *setting.PaymentSettings, order *ent.PaymentOrder) time.Time {
	if order.Status != paymentorder.StatusPending {
		return time.Time{}
	}
	return order.CreatedAt.Add(orderTTL(cfg))
}

// expireIfLapsed lazily marks a pending order past its deadline as expired.
// There is no background job: the deadline only matters when someone looks at
// the order (history list, status poll, resume attempt), so it is evaluated
// there. Fulfillment stays authoritative — an expired order that the gateway
// reports as paid is still settled by settlePaymentOrder.
func expireIfLapsed(c *gin.Context, dep dependency.Dep, order *ent.PaymentOrder) error {
	if order.Status != paymentorder.StatusPending {
		return nil
	}
	cfg := dep.SettingProvider().Payment(c)
	deadline := orderExpiresAt(cfg, order)
	if deadline.IsZero() || time.Now().Before(deadline) {
		return nil
	}
	// A provider-side payment that already landed wins over the deadline, so
	// check before closing the order. settlePaymentOrder has just run; if the
	// order is still pending here the gateway has nothing for us.
	expired, err := dep.VasClient().ExpirePaymentOrder(c, order.ID)
	if err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to expire order", err)
	}
	if !expired {
		// Lost the race against a concurrent settlement: reload so the caller
		// reports the state that actually won.
		if latest, qErr := dep.VasClient().PaymentOrder(c, order.ID); qErr == nil {
			*order = *latest
		}
		return nil
	}
	order.Status = paymentorder.StatusExpired
	return nil
}

// settlePaymentOrder pushes a pending, session-bound order through
// fulfillment when the provider confirms payment. Safe to call from both the
// status poll and the webhook — FulfillPaymentOrder claims the order
// atomically.
func settlePaymentOrder(c *gin.Context, dep dependency.Dep, order *ent.PaymentOrder) error {
	// Expired orders are queried too: a payment made inside the window whose
	// notification never arrived must still settle. The claim in
	// FulfillPaymentOrder only succeeds once, so a late settlement can never
	// grant twice.
	paid, err := providerConfirmsPaid(c, dep, order, true)
	if err != nil {
		return err
	}
	if !paid {
		return nil
	}
	return applyFulfillment(c, dep, order)
}

// providerConfirmsPaid asks the channel whether the order was really paid.
// It returns a plain error only when fulfillment itself should fail; a
// transient provider failure is logged and reported as "not paid" so callers
// can retry later. When logFailures is false (the admin path, which surfaces
// the reason to a human) provider errors are returned to the caller instead.
func providerConfirmsPaid(c *gin.Context, dep dependency.Dep, order *ent.PaymentOrder, quiet bool) (bool, error) {
	if (order.Status != paymentorder.StatusPending && order.Status != paymentorder.StatusExpired) || order.SessionID == "" {
		return false, nil
	}
	cfg := dep.SettingProvider().Payment(c)
	fail := func(format string, args ...any) (bool, error) {
		if quiet {
			dep.Logger().Warning(format, args...)
			return false, nil
		}
		dep.Logger().Warning(format, args...)
		return false, serializer.NewError(serializer.CodeParamErr, "Failed to query the payment channel", nil)
	}
	switch order.Provider {
	case stripeProvider:
		if !cfg.Enabled || cfg.SecretKey == "" {
			return false, nil
		}
		paid, err := payment.Stripe.SessionPaid(c, cfg.SecretKey, order.SessionID)
		if err != nil {
			// Transient provider failures should not fail the status endpoint —
			// the order stays pending and the next poll settles it.
			return fail("Failed to retrieve checkout session %s: %s", order.SessionID, err)
		}
		return paid, nil
	case easypayProvider:
		if !cfg.Easypay.Ready() {
			return false, nil
		}
		paid, money, err := payment.Easypay.OrderPaid(c, easypayConfig(cfg), order.SessionID)
		if err != nil {
			return fail("Failed to query easypay order %s: %s", order.SessionID, err)
		}
		if !paid {
			return false, nil
		}
		// The gateway's own amount must match what was charged; a mismatch
		// leaves the order unsettled for manual review instead of granting.
		if err := easypayAmountMatches(order, money); err != nil {
			return fail("Easypay order %s amount mismatch: %s", order.SessionID, err)
		}
		return true, nil
	default:
		return false, nil
	}
}

// applyFulfillment claims the order exactly once and grants what it bought.
// The single pending|expired→paid transition is what makes every path
// (notification, status poll, admin reconciliation) idempotent.
func applyFulfillment(c *gin.Context, dep dependency.Dep, order *ent.PaymentOrder) error {
	fulfilled, err := dep.VasClient().FulfillPaymentOrder(c, order.ID)
	if err != nil {
		return serializer.NewError(serializer.CodeDBError, "Failed to fulfill order", err)
	}
	if fulfilled {
		order.Status = paymentorder.StatusPaid
		activity.Record(c, dep.SettingProvider(), dep.ActivityClient(), types.EventPaymentFulfilled,
			activity.Extra(map[string]any{"order": order.ID, "amount": order.Amount, "currency": order.Currency}))
	}
	return nil
}

// easypayAmountMatches compares the gateway's "money" (yuan) against the
// order's charged amount expressed in major units.
func easypayAmountMatches(order *ent.PaymentOrder, money string) error {
	if money == "" {
		// The query API may omit money; the notification always carries it,
		// so an absent value is not itself evidence of tampering.
		return nil
	}
	cents, err := payment.ParseMoneyCents(money)
	if err != nil {
		return err
	}
	// order.Amount is in the currency's minor units; the gateway charges
	// major units, so compare against the amount scaled to cents.
	want, ok := scaleToCents(order.Amount, cents)
	if !ok {
		return fmt.Errorf("gateway charged %d cents, order is %d minor units", cents, order.Amount)
	}
	if cents != want {
		return fmt.Errorf("gateway charged %d cents, order is %d cents", cents, want)
	}
	return nil
}

// scaleToCents maps an order amount in minor units onto cents. Orders are
// stored in the site currency's minor units, which for CNY is already cents;
// Japanese-won-style zero-decimal currencies are multiplied by 100 so the
// comparison stays honest. It refuses when the two cannot be reconciled.
func scaleToCents(amount int64, chargedCents int64) (int64, bool) {
	if amount == chargedCents {
		return amount, true
	}
	if amount*100 == chargedCents {
		return amount * 100, true
	}
	if amount == chargedCents*100 {
		return amount, true
	}
	return 0, false
}

func (service *StripeWebhookService) Create(c *gin.Context) (string, error) {
	dep := dependency.FromContext(c)
	cfg := dep.SettingProvider().Payment(c)
	if !cfg.Enabled || cfg.WebhookSecret == "" || cfg.SecretKey == "" {
		return "", serializer.NewError(serializer.CodeParamErr, "Webhook is not configured", nil)
	}

	payload, err := c.GetRawData()
	if err != nil {
		return "", serializer.NewError(serializer.CodeParamErr, "Failed to read payload", err)
	}
	sessionID, completed, err := payment.Stripe.VerifyWebhook(payload, c.GetHeader("Stripe-Signature"), cfg.WebhookSecret)
	if err != nil {
		return "", serializer.NewError(serializer.CodeParamErr, "Invalid webhook signature", err)
	}
	if !completed {
		return "ignored", nil
	}

	// Authoritative check: never trust the event payload for money.
	paid, err := payment.Stripe.SessionPaid(c, cfg.SecretKey, sessionID)
	if err != nil {
		return "", serializer.NewError(serializer.CodeDBError, "Failed to retrieve session", err)
	}
	if !paid {
		return "ignored", nil
	}

	order, err := dep.VasClient().PaymentOrderBySession(c, sessionID)
	if err != nil {
		if errors.Is(err, inventory.ErrPaymentOrderNotFound) {
			return "ignored", nil
		}
		return "", serializer.NewError(serializer.CodeDBError, "Failed to load order", err)
	}
	if order.Provider != stripeProvider {
		return "ignored", nil
	}

	fulfilled, err := dep.VasClient().FulfillPaymentOrder(c, order.ID)
	if err != nil {
		activity.Record(c, dep.SettingProvider(), dep.ActivityClient(), types.EventPaymentFulfillFailed,
			activity.Extra(map[string]any{"order": order.ID, "error": err.Error()}))
		return "", serializer.NewError(serializer.CodeDBError, "Failed to fulfill order", err)
	}
	if fulfilled {
		dep.Logger().Info("Payment order %d fulfilled via webhook (session %s)", order.ID, sessionID)
		activity.Record(c, dep.SettingProvider(), dep.ActivityClient(), types.EventPaymentFulfilled,
			activity.Extra(map[string]any{"order": order.ID, "session": sessionID}))
	}
	return "ok", nil
}

// Create handles the gateway's asynchronous notification. The reply must be
// the literal string "success" — anything else makes the gateway retry — so
// every error path is surfaced through the caller's HTTP status and the body
// only carries "success" on a settled or already-settled order.
func (service *EasypayNotifyService) Create(c *gin.Context) (string, error) {
	dep := dependency.FromContext(c)
	cfg := dep.SettingProvider().Payment(c)
	if !cfg.Easypay.Ready() {
		return "", serializer.NewError(serializer.CodeParamErr, "Easypay is not configured", nil)
	}

	notify, err := payment.Easypay.ParseNotify(queryValues(c), cfg.Easypay.Key)
	if err != nil {
		dep.Logger().Warning("Rejected easypay notification: %s", err)
		return "", serializer.NewError(serializer.CodeParamErr, "Invalid notification signature", err)
	}
	// A notification for another merchant account is not ours to settle.
	if notify.PID != "" && notify.PID != cfg.Easypay.PID {
		return "", serializer.NewError(serializer.CodeParamErr, "Notification is for another merchant", nil)
	}

	order, err := dep.VasClient().PaymentOrderBySession(c, notify.OutTradeNo)
	if err != nil {
		if errors.Is(err, inventory.ErrPaymentOrderNotFound) {
			dep.Logger().Warning("Easypay notification for unknown order %s", notify.OutTradeNo)
			return "", serializer.NewError(serializer.CodeNotFound, "Order not found", nil)
		}
		return "", serializer.NewError(serializer.CodeDBError, "Failed to load order", err)
	}
	if order.Provider != easypayProvider {
		return "", serializer.NewError(serializer.CodeParamErr, "Order belongs to another provider", nil)
	}

	if !notify.Paid() {
		// Not a success status: acknowledge so the gateway stops retrying, but
		// grant nothing.
		return "success", nil
	}
	if err := easypayAmountMatches(order, notify.Money); err != nil {
		dep.Logger().Warning("Easypay notification amount mismatch for order %d: %s", order.ID, err)
		return "", serializer.NewError(serializer.CodeParamErr, "Notification amount mismatch", err)
	}

	fulfilled, err := dep.VasClient().FulfillPaymentOrder(c, order.ID)
	if err != nil {
		activity.Record(c, dep.SettingProvider(), dep.ActivityClient(), types.EventPaymentFulfillFailed,
			activity.Extra(map[string]any{"order": order.ID, "error": err.Error()}))
		return "", serializer.NewError(serializer.CodeDBError, "Failed to fulfill order", err)
	}
	if fulfilled {
		dep.Logger().Info("Payment order %d fulfilled via easypay (trade %s)", order.ID, notify.TradeNo)
		activity.Record(c, dep.SettingProvider(), dep.ActivityClient(), types.EventPaymentFulfilled,
			activity.Extra(map[string]any{"order": order.ID, "trade": notify.TradeNo}))
	}
	return "success", nil
}

// Return handles the buyer's browser. It never grants on its own: a paid
// notice still goes through FulfillPaymentOrder's atomic claim, and an
// unverified or unpaid return just sends the buyer back to the shop where the
// status poll takes over.
func (service *EasypayReturnService) Return(c *gin.Context) string {
	dep := dependency.FromContext(c)
	cfg := dep.SettingProvider().Payment(c)

	shopURL := strings.TrimRight(dep.SettingProvider().SiteURL(c).String(), "/") + "/shop"
	if !cfg.Easypay.Ready() {
		return shopURL
	}
	notify, err := payment.Easypay.ParseNotify(queryValues(c), cfg.Easypay.Key)
	if err != nil {
		dep.Logger().Warning("Invalid easypay return: %s", err)
		return shopURL
	}
	order, err := dep.VasClient().PaymentOrderBySession(c, notify.OutTradeNo)
	if err != nil || order.Provider != easypayProvider {
		return shopURL
	}
	orderHash := hashid.EncodePaymentID(dep.HashIDEncoder(), order.ID)
	target := fmt.Sprintf("%s/shop?checkout=%s", strings.TrimRight(dep.SettingProvider().SiteURL(c).String(), "/"), orderHash)

	if notify.Paid() && easypayAmountMatches(order, notify.Money) == nil {
		if fulfilled, err := dep.VasClient().FulfillPaymentOrder(c, order.ID); err == nil && fulfilled {
			activity.Record(c, dep.SettingProvider(), dep.ActivityClient(), types.EventPaymentFulfilled,
				activity.Extra(map[string]any{"order": order.ID, "trade": notify.TradeNo}))
		}
	}
	return target
}

// queryValues flattens the query string to a plain map; a repeated parameter
// keeps its last value, which is what the signature check sees.
func queryValues(c *gin.Context) map[string]string {
	q, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(q))
	for k, v := range q {
		if len(v) > 0 {
			out[k] = v[len(v)-1]
		}
	}
	return out
}

// ensureStripeCustomer returns the cached Stripe customer id for the user,
// creating the customer on first use.
func ensureStripeCustomer(c *gin.Context, dep dependency.Dep, cfg *setting.PaymentSettings, u *ent.User) (string, error) {
	key := stripeCustomerKVPrefix + strconv.Itoa(u.ID)
	if raw, ok := dep.KV().Get(key); ok {
		if id, ok := raw.(string); ok && id != "" {
			return id, nil
		}
	}
	id, err := payment.Stripe.EnsureCustomer(c, cfg.SecretKey, u.ID, u.Email)
	if err != nil {
		return "", err
	}
	if err := dep.KV().Set(key, id, 0); err != nil {
		util.Log().Warning("Failed to cache Stripe customer id: %s", err)
	}
	return id, nil
}

func paymentOrderResponse(dep dependency.Dep, cfg *setting.PaymentSettings, o *ent.PaymentOrder) *PaymentOrderResponse {
	res := &PaymentOrderResponse{
		ID:        hashid.EncodePaymentID(dep.HashIDEncoder(), o.ID),
		SkuID:     hashid.EncodeSkuID(dep.HashIDEncoder(), o.SkuID),
		Provider:  o.Provider,
		Amount:    o.Amount,
		Currency:  o.Currency,
		Status:    string(o.Status),
		CreatedAt: o.CreatedAt.Unix(),
	}
	if o.Edges.Sku != nil {
		res.SkuName = o.Edges.Sku.Name
	}
	if o.Status == paymentorder.StatusPaid {
		// The row carries no paid_at column; updated_at is bumped by the
		// pending→paid claim, which is the closest thing to a settle time.
		res.PaidAt = o.UpdatedAt.Unix()
	}
	if deadline := orderExpiresAt(cfg, o); !deadline.IsZero() {
		res.ExpiresAt = deadline.Unix()
	}
	return res
}

// List pages the caller's own payment orders, newest first, with each order's
// current state. Expiry is applied lazily per row, so the history never shows
// a pending order that is already past its deadline.
func (service *PaymentOrderListService) List(c *gin.Context) (*PaymentOrderListResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)

	page := service.Page
	if page < 1 {
		page = 1
	}
	pageSize := service.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > paymentOrderMaxPageSize {
		pageSize = paymentOrderMaxPageSize
	}

	// Scoped to the caller: there is no way to list another user's orders.
	orders, total, err := dep.VasClient().ListPaymentOrders(c, u.ID, page, pageSize)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to list orders", err)
	}

	cfg := dep.SettingProvider().Payment(c)
	res := &PaymentOrderListResponse{
		Orders:   make([]*PaymentOrderResponse, 0, len(orders)),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}
	for _, o := range orders {
		// Settle first (a paid-but-unnotified order must show as paid), then
		// close the ones whose window lapsed.
		if o.Status == paymentorder.StatusPending {
			if err := settlePaymentOrder(c, dep, o); err != nil {
				return nil, err
			}
			if err := expireIfLapsed(c, dep, o); err != nil {
				return nil, err
			}
		}
		res.Orders = append(res.Orders, paymentOrderResponse(dep, cfg, o))
	}
	return res, nil
}

// Resume re-opens a still-payable order. It re-asks the gateway for a payment
// handle using the order's existing merchant reference, so no second gateway
// order is created: the buyer either scans again in the same window or the
// order stays expired and a fresh checkout is required.
func (service *PaymentOrderResumeService) Resume(c *gin.Context) (*CheckoutResponse, error) {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)

	orderID, err := dep.HashIDEncoder().Decode(service.ID, hashid.PaymentID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "Invalid order id", err)
	}
	order, err := dep.VasClient().PaymentOrder(c, int(orderID))
	if err != nil {
		if errors.Is(err, inventory.ErrPaymentOrderNotFound) {
			return nil, serializer.NewError(serializer.CodeNotFound, "Order not found", err)
		}
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to get order", err)
	}
	if order.UserID != u.ID {
		return nil, serializer.NewError(serializer.CodeNotFound, "Order not found", nil)
	}

	// A payment made just before the deadline settles here even if the
	// notification never arrived; the caller then sees "paid" instead of a
	// QR code.
	if err := settlePaymentOrder(c, dep, order); err != nil {
		return nil, err
	}
	switch order.Status {
	case paymentorder.StatusPaid:
		return nil, serializer.NewError(serializer.CodeParamErr, "Order is already paid", nil)
	case paymentorder.StatusExpired:
		return nil, serializer.NewError(serializer.CodeParamErr, "Order has expired, please place a new one", nil)
	}
	if err := expireIfLapsed(c, dep, order); err != nil {
		return nil, err
	}
	if order.Status != paymentorder.StatusPending {
		return nil, serializer.NewError(serializer.CodeParamErr, "Order has expired, please place a new one", nil)
	}

	cfg := dep.SettingProvider().Payment(c)
	if order.Provider != easypayProvider || !cfg.Easypay.Ready() {
		return nil, serializer.NewError(serializer.CodeParamErr, "Order cannot be resumed", nil)
	}

	orderHash := hashid.EncodePaymentID(dep.HashIDEncoder(), order.ID)
	remote, err := payment.Easypay.CreateOrder(c, easypayConfig(cfg), payment.EasypayParams{
		PayType: cfg.Easypay.PayType,
		// Same reference as the original checkout: the gateway treats a
		// resubmission as the same order and returns the same trade_no, so a
		// resumed payment can never be a second charge.
		OutTradeNo: order.SessionID,
		NotifyURL:  fmt.Sprintf("%s/api/v4/callback/easypay/notify", strings.TrimRight(dep.SettingProvider().SiteURL(c).String(), "/")),
		ReturnURL:  fmt.Sprintf("%s/api/v4/callback/easypay/return", strings.TrimRight(dep.SettingProvider().SiteURL(c).String(), "/")),
		Name:       order.Edges.Sku.Name,
		Money:      payment.FormatMoneyCents(order.Amount),
		Param:      orderHash,
		ClientIP:   c.ClientIP(),
	})
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to resume checkout session", err)
	}

	return &CheckoutResponse{
		OrderID:  orderHash,
		Provider: easypayProvider,
		Payment: &CheckoutPayment{
			Qrcode:    remote.Qrcode,
			PayURL:    remote.PayURL,
			URLScheme: remote.URLScheme,
		},
		Amount:    order.Amount,
		Currency:  order.Currency,
		ExpiresAt: orderExpiresAt(cfg, order).Unix(),
	}, nil
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// adminOrderResponse builds the console view of one order. Manual settlements
// keep their operator and reason, so a reconciled order stays identifiable in
// the list long after the fact.
func adminOrderResponse(dep dependency.Dep, cfg *setting.PaymentSettings, o *ent.PaymentOrder) *AdminPaymentOrderResponse {
	res := &AdminPaymentOrderResponse{
		ID:        hashid.EncodePaymentID(dep.HashIDEncoder(), o.ID),
		Provider:  o.Provider,
		Amount:    o.Amount,
		Currency:  o.Currency,
		Status:    string(o.Status),
		CreatedAt: o.CreatedAt.Unix(),
		Manual:    o.Manual,
	}
	if o.Edges.User != nil {
		res.UserID = hashid.EncodeUserID(dep.HashIDEncoder(), o.Edges.User.ID)
		res.UserNick = o.Edges.User.Nick
		res.UserEmail = o.Edges.User.Email
	}
	if o.Edges.Sku != nil {
		res.SkuID = hashid.EncodeSkuID(dep.HashIDEncoder(), o.Edges.Sku.ID)
		res.SkuName = o.Edges.Sku.Name
	}
	if o.Status == paymentorder.StatusPaid {
		res.PaidAt = o.UpdatedAt.Unix()
	}
	if deadline := orderExpiresAt(cfg, o); !deadline.IsZero() {
		res.ExpiresAt = deadline.Unix()
	}
	if o.ManualBy != nil {
		// The operator's nickname is resolved lazily by the console; the id is
		// the durable part. Keep the raw id in the reason-adjacent field only
		// when it is the acting admin, otherwise leave the nick empty.
		res.ManualByNick = "admin#" + strconv.Itoa(*o.ManualBy)
	}
	if o.ManualReason != nil {
		res.ManualReason = *o.ManualReason
	}
	return res
}

// List pages every user's orders. Unlike the buyer-facing list this does not
// settle or expire rows on read: an administrator looking at an order must see
// what is stored, and a read must never change billing state behind their back.
func (service *AdminPaymentOrderListService) List(c *gin.Context) (*AdminPaymentOrderListResponse, error) {
	dep := dependency.FromContext(c)

	page, pageSize := service.Page, service.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > paymentOrderMaxPageSize {
		pageSize = paymentOrderMaxPageSize
	}

	opts := inventory.ListPaymentOrdersOptions{Page: page, PageSize: pageSize}
	if q := strings.TrimSpace(service.UserID); q != "" {
		resolveAdminUserFilter(dep, q, &opts)
	}
	switch service.Status {
	case "", "pending", "paid", "expired":
		opts.Status = service.Status
	default:
		return nil, serializer.NewError(serializer.CodeParamErr, "Invalid order status", nil)
	}
	var err error
	if opts.CreatedAfter, err = parseAdminTime(service.CreatedAfter); err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "Invalid created_after", err)
	}
	if opts.CreatedBefore, err = parseAdminTime(service.CreatedBefore); err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "Invalid created_before", err)
	}

	orders, total, err := dep.VasClient().ListAllPaymentOrders(c, opts)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to list orders", err)
	}

	cfg := dep.SettingProvider().Payment(c)
	res := &AdminPaymentOrderListResponse{
		Orders:   make([]*AdminPaymentOrderResponse, 0, len(orders)),
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}
	for _, o := range orders {
		res.Orders = append(res.Orders, adminOrderResponse(dep, cfg, o))
	}
	return res, nil
}

// resolveAdminUserFilter turns the console's single "user" search box into a
// query filter. The box accepts either a user ID or an email address:
//
//   - something that decodes as a user hash ID (or a bare number) is matched
//     on the buyer's ID, exactly;
//   - anything else is treated as an email and matched as a
//     case-insensitive substring, so a partial address still narrows the list.
//
// A query that is neither keeps the email interpretation: it filters to a
// substring that will simply match nothing, which is the same outcome as
// the user typing a nonexistent address and is friendlier than an error.
func resolveAdminUserFilter(dep dependency.Dep, query string, opts *inventory.ListPaymentOrdersOptions) {
	if id, err := dep.HashIDEncoder().Decode(query, hashid.UserID); err == nil {
		opts.UserID = id
		return
	}
	if id, err := strconv.Atoi(query); err == nil && id > 0 {
		opts.UserID = id
		return
	}
	opts.UserEmail = query
}

// parseAdminTime accepts a unix timestamp (seconds) or an RFC3339 date; an
// empty string means "no bound".
func parseAdminTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if sec, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(sec, 0), nil
	}
	return time.Parse(time.RFC3339, raw)
}

// Get returns one order for the console, read-only.
func (service *AdminPaymentOrderService) Get(c *gin.Context) (*AdminPaymentOrderResponse, error) {
	dep := dependency.FromContext(c)
	orderID, err := dep.HashIDEncoder().Decode(service.ID, hashid.PaymentID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "Invalid order id", err)
	}
	order, err := dep.VasClient().PaymentOrder(c, orderID)
	if err != nil {
		if errors.Is(err, inventory.ErrPaymentOrderNotFound) {
			return nil, serializer.NewError(serializer.CodeNotFound, "Order not found", err)
		}
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to get order", err)
	}
	return adminOrderResponse(dep, dep.SettingProvider().Payment(c), order), nil
}

// Reconcile settles an order by hand for the case the notification was lost
// AND a query cannot confirm the payment.
//
// Order of operations, and why:
//   - an already paid order is refused outright — that is the idempotency
//     guarantee at the front door, so a double click never grants twice;
//   - the channel is queried first. A confirmed channel report settles the
//     order through exactly the same path as a notification or a status poll,
//     so nothing is marked manual and no audit exception is needed;
//   - only when the query cannot confirm it may the admin force the
//     settlement, which flags the order and records operator, time and reason.
//
// An expired order is deliberately allowed through both routes: an order past
// its 30 minute window whose payment did arrive is precisely what manual
// reconciliation is for, and FulfillPaymentOrder claims from expired too.
func (service *AdminReconcilePaymentOrderService) Reconcile(c *gin.Context) (*AdminReconcileResponse, error) {
	dep := dependency.FromContext(c)
	admin := inventory.UserFromContext(c)

	orderID, err := dep.HashIDEncoder().Decode(service.ID, hashid.PaymentID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "Invalid order id", err)
	}
	reason := strings.TrimSpace(service.Reason)
	if reason == "" {
		return nil, serializer.NewError(serializer.CodeParamErr, "A reason is required to settle an order by hand", nil)
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}

	order, err := dep.VasClient().PaymentOrder(c, orderID)
	if err != nil {
		if errors.Is(err, inventory.ErrPaymentOrderNotFound) {
			return nil, serializer.NewError(serializer.CodeNotFound, "Order not found", err)
		}
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to get order", err)
	}

	cfg := dep.SettingProvider().Payment(c)
	if order.Status == paymentorder.StatusPaid {
		return &AdminReconcileResponse{
			Order:     adminOrderResponse(dep, cfg, order),
			Confirmed: false,
			Fulfilled: false,
			Message:   "Order already paid",
		}, nil
	}

	// Ask the channel first: it is the authority whenever it can answer.
	confirmed, err := providerConfirmsPaid(c, dep, order, false)
	if err != nil && !service.Force {
		return nil, serializer.NewError(serializer.CodeParamErr,
			"Could not confirm the payment with the channel; retry or force the settlement", err)
	}
	if !confirmed && !service.Force {
		return &AdminReconcileResponse{
			Order:     adminOrderResponse(dep, cfg, order),
			Confirmed: false,
			Fulfilled: false,
			Message:   "The channel does not report this order as paid",
		}, nil
	}

	// Forced settlements carry the audit trail; a channel-confirmed one is a
	// normal settlement and leaves the order unflagged.
	if !confirmed {
		if _, err := dep.VasClient().MarkPaymentOrderManual(c, order.ID, admin.ID, reason); err != nil {
			return nil, serializer.NewError(serializer.CodeDBError, "Failed to flag the order", err)
		}
		dep.Logger().Warning("Admin %d reconciled payment order %d by hand: %s", admin.ID, order.ID, reason)
		activity.Record(c, dep.SettingProvider(), dep.ActivityClient(), types.EventPaymentManualReconcile,
			activity.Extra(map[string]any{"order": order.ID, "admin": admin.ID, "reason": reason}))
	}

	fulfilled, err := dep.VasClient().FulfillPaymentOrder(c, order.ID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to fulfill order", err)
	}

	// Re-read: the claim above only succeeds once, so a concurrent notifier may
	// have won the race. Either way the stored state is what the admin sees.
	updated, err := dep.VasClient().PaymentOrder(c, order.ID)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to reload order", err)
	}

	msg := "Order settled"
	if !fulfilled {
		msg = "Order was already settled by another path; nothing was granted again"
	}
	if fulfilled {
		activity.Record(c, dep.SettingProvider(), dep.ActivityClient(), types.EventPaymentFulfilled,
			activity.Extra(map[string]any{
				"order":  order.ID,
				"amount": order.Amount,
				"manual": !confirmed,
				"admin":  admin.ID,
				"reason": reason,
			}))
	}
	return &AdminReconcileResponse{
		Order:     adminOrderResponse(dep, cfg, updated),
		Confirmed: confirmed,
		Fulfilled: fulfilled,
		Message:   msg,
	}, nil
}
