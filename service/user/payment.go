package user

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

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

	// StripeWebhookService ingests Stripe webhook events. The payload is
	// only a trigger: the session is re-fetched from Stripe before any
	// fulfillment happens.
	StripeWebhookService  struct{}
	StripeWebhookParamCtx struct{}

	// EasypayNotifyService ingests the asynchronous GET notification of the
	// 易支付 gateway. The payload is signature-verified and its amount is
	// compared against the order before fulfillment.
	EasypayNotifyService  struct{}
	EasypayNotifyParamCtx struct{}

	// EasypayReturnService handles the buyer's browser coming back from the
	// gateway. It verifies the signature, settles the order when the payment
	// already succeeded, and redirects to the shop; nothing is granted at
	// return time — the notification or the status poll is authoritative.
	EasypayReturnService  struct{}
	EasypayReturnParamCtx struct{}

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

// createEasypayCheckout records the order, signs the gateway request and
// returns the form the browser POSTs. 易支付 settles only in CNY, so the
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
	checkout, err := payment.Easypay.BuildCheckout(easypayConfig(cfg), payment.EasypayParams{
		PayType:    cfg.Easypay.PayType,
		OutTradeNo: outTradeNo,
		NotifyURL:  fmt.Sprintf("%s/api/v4/callback/easypay/notify", siteURL),
		ReturnURL:  fmt.Sprintf("%s/api/v4/callback/easypay/return", siteURL),
		Name:       s.Name,
		Money:      payment.FormatMoneyCents(order.Amount),
		Param:      orderHash,
	})
	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "Failed to create checkout session", err)
	}

	return &CheckoutResponse{
		OrderID:  orderHash,
		URL:      checkout.URL,
		Provider: easypayProvider,
		Method:   "POST",
		Params:   checkout.Params,
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

	if err := settlePaymentOrder(c, dep, order); err != nil {
		return nil, err
	}
	return paymentOrderResponse(dep, order), nil
}

// settlePaymentOrder pushes a pending, session-bound order through
// fulfillment when the provider confirms payment. Safe to call from both the
// status poll and the webhook — FulfillPaymentOrder claims the order
// atomically.
func settlePaymentOrder(c *gin.Context, dep dependency.Dep, order *ent.PaymentOrder) error {
	if order.Status != paymentorder.StatusPending || order.SessionID == "" {
		return nil
	}
	cfg := dep.SettingProvider().Payment(c)
	switch order.Provider {
	case stripeProvider:
		if !cfg.Enabled || cfg.SecretKey == "" {
			return nil
		}
		paid, err := payment.Stripe.SessionPaid(c, cfg.SecretKey, order.SessionID)
		if err != nil {
			// Transient provider failures should not fail the status endpoint —
			// the order stays pending and the next poll settles it.
			dep.Logger().Warning("Failed to retrieve checkout session %s: %s", order.SessionID, err)
			return nil
		}
		if !paid {
			return nil
		}
	case easypayProvider:
		if !cfg.Easypay.Ready() {
			return nil
		}
		paid, money, err := payment.Easypay.OrderPaid(c, easypayConfig(cfg), order.SessionID)
		if err != nil {
			dep.Logger().Warning("Failed to query easypay order %s: %s", order.SessionID, err)
			return nil
		}
		if !paid {
			return nil
		}
		// The gateway's own amount must match what was charged; a mismatch
		// leaves the order pending for manual review instead of granting.
		if err := easypayAmountMatches(order, money); err != nil {
			dep.Logger().Warning("Easypay order %s amount mismatch: %s", order.SessionID, err)
			return nil
		}
	default:
		return nil
	}

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

func paymentOrderResponse(dep dependency.Dep, o *ent.PaymentOrder) *PaymentOrderResponse {
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
	return res
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
