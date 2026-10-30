import { Box, Button, Chip, CircularProgress, Dialog, DialogContent, DialogTitle, Divider, Stack, Typography } from "@mui/material";
import { useSnackbar } from "notistack";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { QRCodeSVG } from "qrcode.react";
import { getPaymentOrder } from "../../../api/api.ts";
import { CheckoutSession, PaymentOrder } from "../../../api/user.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";

export type PaymentState = "waiting" | "paid" | "expired";

interface Props {
  session: CheckoutSession;
  skuName: string;
  price: string;
  /** Called once when the order settles as paid, to refresh balances. */
  onPaid: (order: PaymentOrder) => void;
  onClose: () => void;
}

// Poll interval for the order status. Two seconds keeps the dialog responsive
// without hammering the server, and the server side is what actually asks the
// gateway, so a missing asynchronous notification still settles here.
const POLL_INTERVAL_MS = 2000;
// Give up after this many polls (~5 minutes) and offer a manual retry.
const MAX_POLLS = 150;

/**
 * In-page payment dialog for the 易支付 channel.
 *
 * The gateway is asked for a payable handle when the order is created. When it
 * returns a real QR payload (wxpay) the dialog renders it; channels that only
 * return a cashier URL (alipay/qqpay) get a "open cashier" button instead,
 * because there is no QR content to show. Either way the dialog polls the
 * order and closes itself once the order settles.
 */
const CheckoutDialog = ({ session, skuName, price, onPaid, onClose }: Props) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const { enqueueSnackbar } = useSnackbar();
  const [state, setState] = useState<PaymentState>("waiting");
  const [polling, setPolling] = useState(true);
  const [attempts, setAttempts] = useState(0);
  // Guards against a poll firing after the dialog closed.
  const cancelled = useRef(false);

  const qrcode = session.payment?.qrcode;
  const payUrl = session.payment?.pay_url;

  const check = useCallback(() => {
    if (cancelled.current) {
      return;
    }
    dispatch(getPaymentOrder(session.order_id))
      .then((order) => {
        if (cancelled.current) {
          return;
        }
        if (order.status === "paid") {
          setState("paid");
          setPolling(false);
          onPaid(order);
          return;
        }
        if (order.status === "expired") {
          setState("expired");
          setPolling(false);
          return;
        }
        setAttempts((n) => {
          const next = n + 1;
          if (next >= MAX_POLLS) {
            setPolling(false);
            return next;
          }
          return next;
        });
      })
      .catch(() => undefined);
  }, [dispatch, session.order_id, onPaid]);

  // Poll while the dialog is open and the order is still pending; every exit
  // path (close, paid, expired, too many attempts) stops the timer.
  useEffect(() => {
    if (!polling) {
      return;
    }
    const timer = window.setTimeout(check, POLL_INTERVAL_MS);
    return () => window.clearTimeout(timer);
  }, [polling, attempts, check]);

  useEffect(() => {
    return () => {
      cancelled.current = true;
    };
  }, []);

  // A paid order closes the dialog on its own; the shop shows the result.
  useEffect(() => {
    if (state === "paid") {
      const timer = window.setTimeout(onClose, 1500);
      return () => window.clearTimeout(timer);
    }
  }, [state, onClose]);

  const statusLabel =
    state === "paid"
      ? t("shop.paymentStatusPaid")
      : state === "expired"
        ? t("shop.paymentStatusExpired")
        : t("shop.paymentStatusWaiting");

  return (
    <Dialog open onClose={onClose} maxWidth="xs" fullWidth>
      <DialogTitle>{t("shop.paymentDialogTitle")}</DialogTitle>
      <DialogContent>
        <Stack spacing={2} alignItems="center" sx={{ pb: 1 }}>
          <Typography variant="subtitle1">{skuName}</Typography>
          <Typography variant="h5" sx={{ fontWeight: 600 }}>
            {price}
          </Typography>

          {state !== "paid" && (
            <>
              {qrcode ? (
                <Box sx={{ p: 2, bgcolor: "#fff", borderRadius: 1, lineHeight: 0 }}>
                  <QRCodeSVG value={qrcode} size={208} level="M" />
                </Box>
              ) : payUrl ? (
                <Stack spacing={1} alignItems="center">
                  <Typography variant="body2" color="text.secondary" align="center">
                    {t("shop.paymentNoQrcode")}
                  </Typography>
                  <Button variant="contained" href={payUrl} target="_blank" rel="noopener noreferrer">
                    {t("shop.paymentOpenCashier")}
                  </Button>
                </Stack>
              ) : null}
              {qrcode && (
                <Typography variant="body2" color="text.secondary">
                  {t("shop.paymentScanHint")}
                </Typography>
              )}
            </>
          )}

          <Stack direction="row" spacing={1} alignItems="center">
            {state === "waiting" && polling && <CircularProgress size={16} />}
            <Chip
              size="small"
              variant={state === "waiting" ? "outlined" : "filled"}
              color={state === "paid" ? "success" : state === "expired" ? "default" : "default"}
              label={statusLabel}
            />
          </Stack>

          <Divider flexItem />
          <Typography variant="caption" color="text.secondary">
            {t("shop.paymentOrderNo", { order: session.order_id })}
          </Typography>

          {state === "waiting" && !polling && (
            <Button
              size="small"
              onClick={() => {
                setAttempts(0);
                setPolling(true);
                check();
              }}
            >
              {t("shop.paymentCheckAgain")}
            </Button>
          )}
          {state === "expired" && (
            <Button size="small" onClick={onClose}>
              {t("common:close")}
            </Button>
          )}
        </Stack>
      </DialogContent>
    </Dialog>
  );
};

export default CheckoutDialog;
