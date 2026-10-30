package payment

// Money holds the shared amount arithmetic of the cash checkout: a SKU price
// is stored in the site currency's minor units, while 易支付 always charges
// major units rounded to cents. Both conversions live here so the checkout and
// the callback verification cannot drift apart.

// ScaleToCents maps an amount in the order's minor units onto cents, the unit
// 易支付 charges in. Minor units equal cents for cent-style currencies
// (unit 100); zero-decimal currencies (JPY/KRW, unit 1) are multiplied by 100
// so the comparison with the gateway stays honest. A second path accepts the
// reverse orientation for gateways reporting in minor units. It refuses when
// neither reconciliation holds.
func ScaleToCents(amount int64, chargedCents int64) (int64, bool) {
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

// OrderCents converts an order amount stored in the site currency's minor
// units into cents for the 易支付 gateway. CurrencyUnit is the number of minor
// units per major unit and defaults to 100 (cents) when unset.
func OrderCents(amount int64, currencyUnit int) int64 {
	if currencyUnit <= 0 || currencyUnit == 100 {
		return amount
	}
	return amount * 100 / int64(currencyUnit)
}
