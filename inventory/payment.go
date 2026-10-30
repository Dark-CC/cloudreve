package inventory

import (
	"context"
	"errors"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/paymentorder"
)

var ErrPaymentOrderNotFound = errors.New("payment order not found")

// CreatePaymentOrder implements VasClient.CreatePaymentOrder.
func (c *vasClient) CreatePaymentOrder(ctx context.Context, userID, skuID int, provider string, amount int64, currency string) (*ent.PaymentOrder, error) {
	return c.client.PaymentOrder.Create().
		SetUserID(userID).
		SetSkuID(skuID).
		SetProvider(provider).
		SetAmount(amount).
		SetCurrency(currency).
		Save(ctx)
}

var ErrPaymentSessionBound = errors.New("payment order already bound to a session")

// BindPaymentSession implements VasClient.BindPaymentSession.
func (c *vasClient) BindPaymentSession(ctx context.Context, orderID int, sessionID string) error {
	n, err := c.client.PaymentOrder.Update().
		Where(paymentorder.ID(orderID), paymentorder.SessionIDIsNil()).
		SetSessionID(sessionID).
		Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrPaymentSessionBound
	}
	return nil
}

// PaymentOrder implements VasClient.PaymentOrder.
func (c *vasClient) PaymentOrder(ctx context.Context, id int) (*ent.PaymentOrder, error) {
	o, err := c.client.PaymentOrder.Query().
		Where(paymentorder.ID(id)).
		WithSku().
		WithUser().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrPaymentOrderNotFound
		}
		return nil, err
	}
	return o, nil
}

// PaymentOrderBySession implements VasClient.PaymentOrderBySession.
func (c *vasClient) PaymentOrderBySession(ctx context.Context, sessionID string) (*ent.PaymentOrder, error) {
	o, err := c.client.PaymentOrder.Query().
		Where(paymentorder.SessionID(sessionID)).
		WithSku().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrPaymentOrderNotFound
		}
		return nil, err
	}
	return o, nil
}

// ListPaymentOrders implements VasClient.ListPaymentOrders.
func (c *vasClient) ListPaymentOrders(ctx context.Context, userID, page, pageSize int) ([]*ent.PaymentOrder, int, error) {
	q := c.client.PaymentOrder.Query().
		Where(paymentorder.UserID(userID)).
		WithSku()
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	orders, err := q.
		Order(ent.Desc(paymentorder.FieldCreatedAt)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		All(ctx)
	return orders, total, err
}

// ListAllPaymentOrders implements VasClient.ListAllPaymentOrders.
func (c *vasClient) ListAllPaymentOrders(ctx context.Context, opts ListPaymentOrdersOptions) ([]*ent.PaymentOrder, int, error) {
	q := c.client.PaymentOrder.Query().WithSku().WithUser()
	if opts.UserID > 0 {
		q = q.Where(paymentorder.UserID(opts.UserID))
	}
	if opts.Status != "" {
		q = q.Where(paymentorder.StatusEQ(paymentorder.Status(opts.Status)))
	}
	if !opts.CreatedAfter.IsZero() {
		q = q.Where(paymentorder.CreatedAtGTE(opts.CreatedAfter))
	}
	if !opts.CreatedBefore.IsZero() {
		q = q.Where(paymentorder.CreatedAtLTE(opts.CreatedBefore))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	page, pageSize := opts.Page, opts.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	orders, err := q.
		Order(ent.Desc(paymentorder.FieldCreatedAt)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		All(ctx)
	return orders, total, err
}

// MarkPaymentOrderManual implements VasClient.MarkPaymentOrderManual.
func (c *vasClient) MarkPaymentOrderManual(ctx context.Context, orderID, adminID int, reason string) (bool, error) {
	n, err := c.client.PaymentOrder.Update().
		Where(paymentorder.ID(orderID), paymentorder.ManualEQ(false)).
		SetManual(true).
		SetManualBy(adminID).
		SetManualReason(reason).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ExpirePaymentOrder implements VasClient.ExpirePaymentOrder.
func (c *vasClient) ExpirePaymentOrder(ctx context.Context, orderID int) (bool, error) {
	n, err := c.client.PaymentOrder.Update().
		Where(paymentorder.ID(orderID), paymentorder.StatusEQ(paymentorder.StatusPending)).
		SetStatus(paymentorder.StatusExpired).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// FulfillPaymentOrder implements VasClient.FulfillPaymentOrder.
func (c *vasClient) FulfillPaymentOrder(ctx context.Context, orderID int) (bool, error) {
	// Atomic claim in autocommit: only one fulfiller can flip pending→paid.
	// Same pattern as gift-code redemption — keeps webhook retries and the
	// user-facing status poll from double-granting.
	// An expired order is still claimable: if the buyer paid inside the
	// window and the payment only reached us afterwards, the money is real and
	// the order must be settled rather than swallowed. Claiming from both
	// pending and expired stays a single atomic transition, so the double
	// grant protection is unchanged.
	n, err := c.client.PaymentOrder.Update().
		Where(
			paymentorder.ID(orderID),
			paymentorder.StatusIn(paymentorder.StatusPending, paymentorder.StatusExpired),
		).
		SetStatus(paymentorder.StatusPaid).
		Save(ctx)
	if err != nil {
		return false, err
	}
	if n == 0 {
		if _, qErr := c.client.PaymentOrder.Get(ctx, orderID); qErr != nil {
			if ent.IsNotFound(qErr) {
				return false, ErrPaymentOrderNotFound
			}
			return false, qErr
		}
		return false, nil
	}

	o, err := c.PaymentOrder(ctx, orderID)
	if err != nil {
		return false, err
	}

	txVc, tx, txCtx, err := WithTx(ctx, c)
	if err != nil {
		return false, err
	}
	grantErr := txVc.applySkuGrant(txCtx, o.UserID, o.Edges.Sku)
	if grantErr == nil {
		grantErr = Commit(tx)
	} else {
		_ = Rollback(tx)
	}
	if grantErr != nil {
		// Compensate the paid flip so a failed grant does not strand the
		// order in a paid-but-unfulfilled state — the next webhook retry or
		// status poll can settle it again.
		_, _ = c.client.PaymentOrder.Update().
			Where(paymentorder.ID(orderID), paymentorder.StatusEQ(paymentorder.StatusPaid)).
			SetStatus(paymentorder.StatusPending).
			Save(ctx)
		return false, grantErr
	}
	return true, nil
}
