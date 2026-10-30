import { ChipProps } from "@mui/material";

/**
 * Order statuses, in the order the console lists them. The wire values stay
 * the backend's English enum; only the label the operator reads is translated.
 */
export const PaymentOrderStatuses = ["pending", "paid", "expired"] as const;

export type PaymentOrderStatus = (typeof PaymentOrderStatuses)[number];

/**
 * getPaymentOrderStatusText maps a stored status to its label. Unknown values
 * (a status added by a newer backend) fall back to the raw value rather than
 * showing a translation key.
 */
export const getPaymentOrderStatusText = (
  status: string,
  t: (key: string) => string,
): string => {
  switch (status) {
    case "pending":
      return t("paymentOrders.statusPending");
    case "paid":
      return t("paymentOrders.statusPaid");
    case "expired":
      return t("paymentOrders.statusExpired");
    case "refunded":
      return t("paymentOrders.statusRefunded");
    case "failed":
      return t("paymentOrders.statusFailed");
    default:
      return status || "-";
  }
};

/**
 * statusChipColor picks the semantic MUI colour for a status, so the palette
 * comes from the theme instead of a hand-written hex value.
 */
export const statusChipColor = (status: string): ChipProps["color"] => {
  switch (status) {
    case "paid":
      return "success";
    case "pending":
      return "warning";
    case "expired":
      return "default";
    case "refunded":
      return "info";
    case "failed":
      return "error";
    default:
      return "default";
  }
};
