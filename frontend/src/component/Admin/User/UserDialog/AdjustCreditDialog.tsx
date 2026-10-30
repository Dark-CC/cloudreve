import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Stack,
  Typography,
} from "@mui/material";
import { useSnackbar } from "notistack";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { adjustUserCredit } from "../../../../api/api";
import { UserEnt } from "../../../../api/user";
import { useAppDispatch } from "../../../../redux/hooks";
import { DenseFilledTextField, SecondaryButton } from "../../../Common/StyledComponents";

interface AdjustCreditDialogProps {
  open: boolean;
  onClose: () => void;
  user: UserEnt;
  onAdjusted: (credits: number) => void;
}

// AdjustCreditDialog applies a signed points delta to one user. Deliberately a
// delta rather than an absolute value: the server records it in the points
// ledger, so the balance stays reconstructable from the transaction history
// instead of being silently overwritten.
const AdjustCreditDialog = ({ open, onClose, user, onAdjusted }: AdjustCreditDialogProps) => {
  const { t } = useTranslation("dashboard");
  const dispatch = useAppDispatch();
  const { enqueueSnackbar } = useSnackbar();
  const [delta, setDelta] = useState("");
  const [reason, setReason] = useState("");
  const [loading, setLoading] = useState(false);

  const onConfirm = useCallback(async () => {
    const parsed = parseInt(delta, 10);
    if (isNaN(parsed) || parsed === 0) {
      enqueueSnackbar(t("user.creditDeltaInvalid"), { variant: "warning" });
      return;
    }
    setLoading(true);
    try {
      const res = await dispatch(adjustUserCredit(user.id, parsed, reason));
      const updated = res as unknown as UserEnt;
      enqueueSnackbar(t("user.creditAdjusted"), { variant: "success" });
      onAdjusted(updated?.credits ?? (user.credits ?? 0) + parsed);
      setDelta("");
      setReason("");
      onClose();
    } catch {
      // send() surfaces the server's message through its own snackbar.
    } finally {
      setLoading(false);
    }
  }, [delta, reason, user, dispatch, enqueueSnackbar, t, onAdjusted, onClose]);

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="xs">
      <DialogTitle>{t("user.adjustPoints")}</DialogTitle>
      <DialogContent>
        <Stack spacing={2} sx={{ mt: 1 }}>
          <Typography variant="body2" color="text.secondary">
            {t("user.currentCredits", { credits: user.credits ?? 0 })}
          </Typography>
          <DenseFilledTextField
            label={t("user.creditDelta")}
            helperText={t("user.creditDeltaDes")}
            fullWidth
            type="number"
            value={delta}
            onChange={(e) => setDelta(e.target.value)}
            slotProps={{ htmlInput: { step: 1 } }}
          />
          <DenseFilledTextField
            label={t("user.creditReason")}
            helperText={t("user.creditReasonDes")}
            fullWidth
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            slotProps={{ htmlInput: { maxLength: 200 } }}
          />
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={loading}>
          {t("common:cancel")}
        </Button>
        <SecondaryButton variant="contained" onClick={onConfirm} disabled={loading}>
          {t("common:save")}
        </SecondaryButton>
      </DialogActions>
    </Dialog>
  );
};

export default AdjustCreditDialog;
