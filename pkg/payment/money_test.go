package payment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// OrderCents is the 易支付 charge amount derived from an order stored in the
// site currency's minor units. A cent-style currency is a pass-through, which
// is what keeps a 0.01 (one minor unit) price charging 0.01.
func TestOrderCents(t *testing.T) {
	for _, c := range []struct {
		amount int64
		unit   int
		want   int64
	}{
		{amount: 1, unit: 100, want: 1},     // 0.01 in cents stays one cent
		{amount: 499, unit: 100, want: 499}, // 4.99
		{amount: 100, unit: 100, want: 100}, // 1.00
		{amount: 100, unit: 0, want: 100},   // unset unit defaults to cents
		{amount: 100, unit: 1, want: 10000}, // zero-decimal currency: 100 yen = 10000 sen
		{amount: 15, unit: 1, want: 1500},   // 15 yen
		{amount: 0, unit: 100, want: 0},     //
	} {
		require.Equal(t, c.want, OrderCents(c.amount, c.unit), "amount=%d unit=%d", c.amount, c.unit)
	}
}

// The formatted charge for a one-cent product must be "0.01"; the decimal
// admin price is what makes this reachable now.
func TestOrderCentsFormatsSmallestPrice(t *testing.T) {
	require.Equal(t, "0.01", FormatMoneyCents(OrderCents(1, 100)))
	require.Equal(t, "1.00", FormatMoneyCents(OrderCents(100, 100)))
}

// ScaleToCents stays the verification side: an order in minor units and the
// gateway's cents reconcile in either orientation, and refuse otherwise.
func TestScaleToCents(t *testing.T) {
	for _, c := range []struct {
		amount, charged, want int64
		ok                    bool
	}{
		{amount: 1, charged: 1, want: 1, ok: true},
		{amount: 1, charged: 100, want: 100, ok: true}, // yen order vs cents
		{amount: 100, charged: 1, want: 100, ok: true}, // gateway reported minor units
		{amount: 1, charged: 2, want: 0, ok: false},
		{amount: 499, charged: 500, want: 0, ok: false},
	} {
		got, ok := ScaleToCents(c.amount, c.charged)
		require.Equal(t, c.ok, ok, "amount=%d charged=%d", c.amount, c.charged)
		require.Equal(t, c.want, got, "amount=%d charged=%d", c.amount, c.charged)
	}
}
