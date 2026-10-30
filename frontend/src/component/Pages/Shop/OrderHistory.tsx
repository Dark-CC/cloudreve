import { Button, Chip, Paper, Stack, Table, TableBody, TableCell, TableHead, TableRow, Typography } from "@mui/material";
import dayjs from "dayjs";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { listPaymentOrders } from "../../../api/api.ts";
import { PaymentOrder } from "../../../api/user.ts";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks.ts";
import Nothing from "../../Common/Nothing.tsx";
import FacebookCircularProgress from "../../Common/CircularProgress.tsx";

interface Props {
  /** Called when the buyer wants to finish an unpaid order. */
  onResume: (order: PaymentOrder) => void;
  /** Bumped by the shop after a payment settles, to reload the list. */
  refreshToken?: number;
}

/**
 * The caller's own payment orders. Pending orders show their remaining window
 * and can be resumed; expired ones are shown as lapsed with no way back in —
 * the buyer has to place a new order.
 */
const OrderHistory = ({ onResume, refreshToken }: Props) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const [orders, setOrders] = useState<PaymentOrder[] | undefined>(undefined);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const pageSize = 20;
  const currencyMark = useAppSelector((state) => state.siteConfig.basic.config.payment_currency_mark);
  const currencyUnit = useAppSelector((state) => state.siteConfig.basic.config.payment_currency_unit ?? 100);

  const load = useCallback(
    (p: number) => {
      dispatch(listPaymentOrders(p, pageSize)).then((res) => {
        setOrders(res.orders ?? []);
        setTotal(res.total ?? 0);
      });
    },
    [dispatch],
  );

  useEffect(() => {
    load(page);
  }, [page, load, refreshToken]);

  const formatAmount = (o: PaymentOrder) =>
    currencyMark
      ? `${currencyMark}${(o.amount / currencyUnit).toFixed(2)}`
      : `${(o.amount / currencyUnit).toFixed(2)} ${o.currency.toUpperCase()}`;

  const statusChip = (o: PaymentOrder) => {
    if (o.status === "paid") {
      return <Chip size="small" color="success" label={t("shop.orderStatusPaid")} />;
    }
    if (o.status === "expired") {
      return <Chip size="small" label={t("shop.orderStatusExpired")} />;
    }
    return <Chip size="small" color="warning" variant="outlined" label={t("shop.orderStatusPending")} />;
  };

  // Remaining minutes in the payment window, for a pending order.
  const remaining = (o: PaymentOrder) => {
    if (o.status !== "pending" || !o.expires_at) {
      return undefined;
    }
    const ms = o.expires_at * 1000 - Date.now();
    if (ms <= 0) {
      return 0;
    }
    return Math.ceil(ms / 60000);
  };

  if (orders === undefined) {
    return (
      <Stack sx={{ pt: 10, alignItems: "center" }}>
        <FacebookCircularProgress />
      </Stack>
    );
  }

  if (orders.length === 0) {
    return <Nothing primary={t("shop.noOrders")} secondary={t("shop.noOrdersDescription")} />;
  }

  const pages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <Paper variant="outlined" sx={{ mt: 2 }}>
      <Table size="small">
        <TableHead>
          <TableRow>
            <TableCell>{t("shop.orderNo")}</TableCell>
            <TableCell>{t("shop.orderItem")}</TableCell>
            <TableCell align="right">{t("shop.orderAmount")}</TableCell>
            <TableCell>{t("shop.orderStatus")}</TableCell>
            <TableCell>{t("shop.orderCreatedAt")}</TableCell>
            <TableCell>{t("shop.orderPaidAt")}</TableCell>
            <TableCell />
          </TableRow>
        </TableHead>
        <TableBody>
          {orders.map((o) => {
            const mins = remaining(o);
            return (
              <TableRow key={o.id} hover>
                <TableCell sx={{ fontFamily: "monospace", fontSize: 12 }}>{o.id}</TableCell>
                <TableCell>{o.sku_name}</TableCell>
                <TableCell align="right">{formatAmount(o)}</TableCell>
                <TableCell>
                  <Stack direction="row" spacing={1} alignItems="center">
                    {statusChip(o)}
                    {mins !== undefined && mins > 0 && (
                      <Typography variant="caption" color="text.secondary">
                        {t("shop.orderExpiresIn", { minutes: mins })}
                      </Typography>
                    )}
                  </Stack>
                </TableCell>
                <TableCell>{dayjs.unix(o.created_at).format("YYYY-MM-DD HH:mm")}</TableCell>
                <TableCell>{o.paid_at ? dayjs.unix(o.paid_at).format("YYYY-MM-DD HH:mm") : "-"}</TableCell>
                <TableCell align="right">
                  {o.status === "pending" && mins !== undefined && mins > 0 && (
                    <Button size="small" variant="outlined" onClick={() => onResume(o)}>
                      {t("shop.payNow")}
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      {pages > 1 && (
        <Stack direction="row" spacing={1} sx={{ p: 1, justifyContent: "flex-end", alignItems: "center" }}>
          <Button size="small" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>
            {t("shop.prevPage")}
          </Button>
          <Typography variant="caption">
            {page} / {pages}
          </Typography>
          <Button size="small" disabled={page >= pages} onClick={() => setPage((p) => p + 1)}>
            {t("shop.nextPage")}
          </Button>
        </Stack>
      )}
    </Paper>
  );
};

export default OrderHistory;
