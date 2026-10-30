import { SyncProblem } from "@mui/icons-material";
import {
  Alert,
  Box,
  Button,
  Chip,
  Container,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  Stack,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Typography,
} from "@mui/material";
import dayjs from "dayjs";
import { enqueueSnackbar } from "notistack";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { adminGetPaymentOrder, adminListPaymentOrders, adminReconcilePaymentOrder } from "../../../api/api";
import { AdminPaymentOrder } from "../../../api/user";
import { useAppDispatch } from "../../../redux/hooks";
import { DefaultCloseAction } from "../../Common/Snackbar/snackbar";
import { DenseSelect, NoWrapTableCell, SecondaryButton, StyledTableContainerPaper } from "../../Common/StyledComponents";
import { SquareMenuItem } from "../../FileManager/ContextMenu/ContextMenu";
import PageContainer from "../../Pages/PageContainer";
import PageHeader from "../../Pages/PageHeader";
import TablePagination from "../Common/TablePagination";

/**
 * Column widths are pinned so the table keeps the same rhythm as /admin/task:
 * fixed layout, no reflow as rows load, amounts and dates never shift column.
 */
const COL = {
  id: 110,
  buyer: "20%",
  item: "18%",
  amount: 90,
  provider: 100,
  status: 150,
  createdAt: 150,
  paidAt: 150,
  actions: 120,
} as const;

const statusColor = (s: AdminPaymentOrder["status"]): "success" | "warning" | "default" =>
  s === "paid" ? "success" : s === "pending" ? "warning" : "default";

/**
 * ReconcileDialog settles one order by hand. The admin must always give a
 * reason; forcing it through (when the channel cannot confirm the payment) is a
 * second, explicitly switched-on decision, because it grants goods with no
 * evidence but the operator's word.
 */
const ReconcileDialog = ({
  order,
  onClose,
  onDone,
}: {
  order?: AdminPaymentOrder;
  onClose: () => void;
  onDone: () => void;
}) => {
  const { t } = useTranslation("dashboard");
  const dispatch = useAppDispatch();
  const [reason, setReason] = useState("");
  const [force, setForce] = useState(false);
  const [loading, setLoading] = useState(false);
  const [confirmedStep, setConfirmedStep] = useState(false);

  useEffect(() => {
    setReason("");
    setForce(false);
    setConfirmedStep(false);
  }, [order?.id]);

  const submit = () => {
    if (!order) return;
    // Forcing is destructive (grants without evidence) — require a second click.
    if (force && !confirmedStep) {
      setConfirmedStep(true);
      return;
    }
    setLoading(true);
    dispatch(adminReconcilePaymentOrder(order.id, { reason, force }))
      .then((res) => {
        enqueueSnackbar({
          message: res.fulfilled ? t("paymentOrders.reconcileSuccess") : t("paymentOrders.alreadyPaid"),
          variant: res.fulfilled ? "success" : "info",
          action: DefaultCloseAction,
        });
        onDone();
        onClose();
      })
      .catch((e) => {
        enqueueSnackbar({
          message: e.message,
          variant: "warning",
          action: DefaultCloseAction,
        });
      })
      .finally(() => {
        setLoading(false);
        setConfirmedStep(false);
      });
  };

  return (
    <Dialog open={!!order} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{t("paymentOrders.reconcileTitle")}</DialogTitle>
      <DialogContent>
        {order && (
          <Stack spacing={1} sx={{ mb: 1 }}>
            <Typography variant="body2">
              {t("paymentOrders.orderNo")}: <code>{order.id}</code>
            </Typography>
            <Typography variant="body2">
              {t("paymentOrders.buyer")}: {order.user_nick || order.user_email}
            </Typography>
            <Typography variant="body2">
              {t("paymentOrders.item")}: {order.sku_name} · {(order.amount / 100).toFixed(2)} {order.currency}
            </Typography>
          </Stack>
        )}
        <Alert severity="info" sx={{ mb: 1 }}>
          {t("paymentOrders.reconcileHint")}
        </Alert>
        <TextField
          fullWidth
          required
          multiline
          minRows={2}
          label={t("paymentOrders.reason")}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          inputProps={{ maxLength: 500 }}
          helperText={t("paymentOrders.reasonHint")}
        />
        <FormControlLabel
          sx={{ mt: 1 }}
          control={
            <Switch
              checked={force}
              onChange={(e) => {
                setForce(e.target.checked);
                setConfirmedStep(false);
              }}
            />
          }
          label={t("paymentOrders.force")}
        />
        {force && (
          <Alert severity="warning">{t("paymentOrders.forceHint")}</Alert>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t("cancel")}</Button>
        <Button
          onClick={submit}
          variant="contained"
          disabled={loading || !reason.trim()}
          color={force ? "warning" : "primary"}
        >
          {force && confirmedStep ? t("paymentOrders.forceConfirm") : t("paymentOrders.reconcile")}
        </Button>
      </DialogActions>
    </Dialog>
  );
};

const DetailDialog = ({ order, onClose }: { order?: AdminPaymentOrder; onClose: () => void }) => {
  const { t } = useTranslation("dashboard");
  if (!order) return null;
  const row = (label: string, value: React.ReactNode) => (
    <Stack direction="row" spacing={2} sx={{ py: 0.5 }}>
      <Typography variant="body2" sx={{ minWidth: 120 }} color="text.secondary">
        {label}
      </Typography>
      <Typography variant="body2" sx={{ wordBreak: "break-all" }}>
        {value}
      </Typography>
    </Stack>
  );
  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{t("paymentOrders.detail")}</DialogTitle>
      <DialogContent>
        {row(t("paymentOrders.orderNo"), <code>{order.id}</code>)}
        {row(t("paymentOrders.buyer"), `${order.user_nick || "-"} (${order.user_email || "-"})`)}
        {row(t("paymentOrders.item"), order.sku_name)}
        {row(t("paymentOrders.amount"), `${(order.amount / 100).toFixed(2)} ${order.currency.toUpperCase()}`)}
        {row(t("paymentOrders.provider"), order.provider)}
        {row(t("paymentOrders.status"), <Chip size="small" label={order.status} color={statusColor(order.status)} />)}
        {row(t("paymentOrders.createdAt"), dayjs.unix(order.created_at).format("YYYY-MM-DD HH:mm:ss"))}
        {row(
          t("paymentOrders.paidAt"),
          order.paid_at ? dayjs.unix(order.paid_at).format("YYYY-MM-DD HH:mm:ss") : "-",
        )}
        {row(
          t("paymentOrders.manual"),
          order.manual ? `${t("paymentOrders.manualYes")} · ${order.manual_by_nick || "-"}` : t("paymentOrders.no"),
        )}
        {order.manual_reason && row(t("paymentOrders.reason"), order.manual_reason)}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t("close")}</Button>
      </DialogActions>
    </Dialog>
  );
};

const PaymentOrderList = () => {
  const { t } = useTranslation("dashboard");
  const dispatch = useAppDispatch();
  const [orders, setOrders] = useState<AdminPaymentOrder[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [status, setStatus] = useState("");
  const [userId, setUserId] = useState("");
  const [after, setAfter] = useState("");
  const [before, setBefore] = useState("");
  const [loading, setLoading] = useState(false);
  const [reconcile, setReconcile] = useState<AdminPaymentOrder | undefined>();
  const [detail, setDetail] = useState<AdminPaymentOrder | undefined>();

  const load = useCallback(() => {
    setLoading(true);
    dispatch(
      adminListPaymentOrders({
        page,
        page_size: pageSize,
        status: status || undefined,
        user_id: userId.trim() || undefined,
        created_after: after || undefined,
        created_before: before || undefined,
      }),
    )
      .then((res) => {
        setOrders(res.orders ?? []);
        setTotal(res.total ?? 0);
      })
      .finally(() => setLoading(false));
  }, [dispatch, page, pageSize, status, userId, after, before]);

  useEffect(() => {
    load();
  }, [load]);

  const openDetail = (o: AdminPaymentOrder) => {
    // Re-read the single order so the dialog shows stored state, not a cached row.
    dispatch(adminGetPaymentOrder(o.id))
      .then((fresh) => setDetail(fresh))
      .catch(() => setDetail(o));
  };

  const applyFilter = () => {
    setPage(1);
    load();
  };

  return (
    <PageContainer>
      <Container maxWidth="xl">
        <PageHeader title={t("dashboard:nav.paymentOrders")} loading={loading} onRefresh={load} />
        <Stack direction="row" spacing={1} sx={{ mb: 2, flexWrap: "wrap", rowGap: 1 }}>
          <DenseSelect
            value={status}
            onChange={(e) => {
              setStatus(e.target.value as string);
              setPage(1);
            }}
            displayEmpty
            size="small"
          >
            <SquareMenuItem value="">{t("paymentOrders.allStatus")}</SquareMenuItem>
            <SquareMenuItem value="pending">{t("paymentOrders.statusPending")}</SquareMenuItem>
            <SquareMenuItem value="paid">{t("paymentOrders.statusPaid")}</SquareMenuItem>
            <SquareMenuItem value="expired">{t("paymentOrders.statusExpired")}</SquareMenuItem>
          </DenseSelect>
          <TextField
            size="small"
            label={t("paymentOrders.userId")}
            value={userId}
            onChange={(e) => setUserId(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") applyFilter();
            }}
            sx={{ width: 170 }}
          />
          <TextField
            size="small"
            type="date"
            label={t("paymentOrders.createdAfter")}
            value={after}
            onChange={(e) => setAfter(e.target.value)}
            InputLabelProps={{ shrink: true }}
            sx={{ width: 170 }}
          />
          <TextField
            size="small"
            type="date"
            label={t("paymentOrders.createdBefore")}
            value={before}
            onChange={(e) => setBefore(e.target.value)}
            InputLabelProps={{ shrink: true }}
            sx={{ width: 170 }}
          />
          <SecondaryButton variant="contained" onClick={applyFilter} disabled={loading}>
            {t("paymentOrders.applyFilter")}
          </SecondaryButton>
        </Stack>
        <TableContainer component={StyledTableContainerPaper} sx={{ mt: 2 }}>
          <Table size="small" stickyHeader sx={{ width: "100%", tableLayout: "fixed" }}>
            <TableHead>
              <TableRow>
                <NoWrapTableCell width={COL.id}>{t("paymentOrders.orderNo")}</NoWrapTableCell>
                <NoWrapTableCell width={COL.buyer}>{t("paymentOrders.buyer")}</NoWrapTableCell>
                <NoWrapTableCell width={COL.item}>{t("paymentOrders.item")}</NoWrapTableCell>
                <NoWrapTableCell width={COL.amount} align="right">
                  {t("paymentOrders.amount")}
                </NoWrapTableCell>
                <NoWrapTableCell width={COL.provider}>{t("paymentOrders.provider")}</NoWrapTableCell>
                <NoWrapTableCell width={COL.status}>{t("paymentOrders.status")}</NoWrapTableCell>
                <NoWrapTableCell width={COL.createdAt}>{t("paymentOrders.createdAt")}</NoWrapTableCell>
                <NoWrapTableCell width={COL.paidAt}>{t("paymentOrders.paidAt")}</NoWrapTableCell>
                <NoWrapTableCell width={COL.actions} align="right">
                  {t("paymentOrders.actions")}
                </NoWrapTableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {orders.map((o) => (
                <TableRow key={o.id} hover>
                  <NoWrapTableCell sx={{ fontFamily: "monospace", fontSize: 12 }}>
                    <Button size="small" onClick={() => openDetail(o)} sx={{ textTransform: "none", p: 0, minWidth: 0 }}>
                      {o.id}
                    </Button>
                  </NoWrapTableCell>
                  <NoWrapTableCell>{o.user_nick || o.user_email || "-"}</NoWrapTableCell>
                  <NoWrapTableCell>{o.sku_name}</NoWrapTableCell>
                  <NoWrapTableCell align="right">{(o.amount / 100).toFixed(2)}</NoWrapTableCell>
                  <NoWrapTableCell>{o.provider}</NoWrapTableCell>
                  <NoWrapTableCell>
                    <Stack direction="row" spacing={0.5}>
                      <Chip size="small" label={o.status} color={statusColor(o.status)} />
                      {o.manual && <Chip size="small" variant="outlined" label={t("paymentOrders.manualYes")} />}
                    </Stack>
                  </NoWrapTableCell>
                  <NoWrapTableCell>{dayjs.unix(o.created_at).format("MM-DD HH:mm")}</NoWrapTableCell>
                  <NoWrapTableCell>{o.paid_at ? dayjs.unix(o.paid_at).format("MM-DD HH:mm") : "-"}</NoWrapTableCell>
                  <NoWrapTableCell align="right">
                    {o.status !== "paid" && (
                      <Button size="small" startIcon={<SyncProblem />} onClick={() => setReconcile(o)} color="warning">
                        {t("paymentOrders.reconcile")}
                      </Button>
                    )}
                  </NoWrapTableCell>
                </TableRow>
              ))}
              {orders.length === 0 && !loading && (
                <TableRow>
                  <TableCell colSpan={9} align="center">
                    <Typography variant="body2" color="text.secondary" sx={{ py: 3 }}>
                      {t("paymentOrders.empty")}
                    </Typography>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </TableContainer>
        {total > 0 && (
          <Box sx={{ mt: 1 }}>
            <TablePagination
              page={page}
              totalItems={total}
              rowsPerPage={pageSize}
              rowsPerPageOptions={[10, 25, 50]}
              onRowsPerPageChange={(v) => {
                setPageSize(v);
                setPage(1);
              }}
              onChange={(_, v) => setPage(v)}
            />
          </Box>
        )}
      </Container>
      <ReconcileDialog order={reconcile} onClose={() => setReconcile(undefined)} onDone={load} />
      <DetailDialog order={detail} onClose={() => setDetail(undefined)} />
    </PageContainer>
  );
};

export default PaymentOrderList;
