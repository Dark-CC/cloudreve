// Cash price helpers shared by the admin panel and the shop. A SKU's `price`
// is stored in the currency's minor units (the site's payment_currency_unit
// per major unit), so everything the admin types in major units has to be
// converted at the edge and formatted back the same way.

// Round to a whole minor unit — 0.01 with unit 100, 1 with unit 1. A positive
// price never rounds down to zero: the backend reads price 0 as "not
// purchasable with cash", so a mistyped 0.005 would silently disable the
// product. It becomes the smallest chargeable amount instead.
export const toMinorUnits = (major: number, unit: number): number => {
  const scale = unit > 0 ? unit : 1;
  if (!Number.isFinite(major) || major <= 0) {
    return 0;
  }
  return Math.max(1, Math.round(major * scale));
};

// fromMinorUnits is the inverse, rounded to two decimals for display.
export const fromMinorUnits = (minor: number | undefined, unit: number): number => {
  const scale = unit > 0 ? unit : 1;
  if (!minor || minor <= 0) {
    return 0;
  }
  return Number((minor / scale).toFixed(2));
};

// smallestPriceStep is the smallest amount the admin may enter: one minor
// unit in major units (0.01 for a cent-style currency).
export const smallestPriceStep = (unit: number): number => {
  const scale = unit > 0 ? unit : 1;
  return Number((1 / scale).toFixed(4));
};
