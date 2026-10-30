import { Box, Button, ListItemText, Popover, PopoverProps, Stack } from "@mui/material";
import { DatePicker, LocalizationProvider } from "@mui/x-date-pickers";
import { AdapterDayjs } from "@mui/x-date-pickers/AdapterDayjs";
import dayjs, { Dayjs } from "dayjs";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { DenseFilledTextField, DenseSelect } from "../../Common/StyledComponents";
import { SquareMenuItem } from "../../FileManager/ContextMenu/ContextMenu";
import SettingForm from "../../Pages/Setting/SettingForm";
import { PaymentOrderStatuses, getPaymentOrderStatusText } from "./PaymentOrderStatus";

export interface PaymentOrderFilterValues {
  status: string;
  user: string;
  createdAfter: string;
  createdBefore: string;
}

export interface PaymentOrderFilterPopoverProps extends PopoverProps, PaymentOrderFilterValues {
  setFilters: (values: PaymentOrderFilterValues) => void;
  clearFilters: () => void;
}

/**
 * Every control here is a stock MUI one: DenseSelect for the status and the
 * date pickers from @mui/x-date-pickers (the same pair the task cleanup dialog
 * uses), so the popover inherits the theme instead of carrying its own styling.
 *
 * Dates are held as Dayjs while the popover is open and handed back as plain
 * "YYYY-MM-DD" strings, which is what the admin API accepts.
 */
const PaymentOrderFilterPopover = ({
  status,
  user,
  createdAfter,
  createdBefore,
  setFilters,
  clearFilters,
  onClose,
  open,
  ...rest
}: PaymentOrderFilterPopoverProps) => {
  const { t } = useTranslation("dashboard");

  const [localStatus, setLocalStatus] = useState(status);
  const [localUser, setLocalUser] = useState(user);
  const [localAfter, setLocalAfter] = useState<Dayjs | null>(createdAfter ? dayjs(createdAfter) : null);
  const [localBefore, setLocalBefore] = useState<Dayjs | null>(createdBefore ? dayjs(createdBefore) : null);

  // Re-seed the local copy whenever the popover opens, so an applied filter is
  // what the operator sees next time.
  useEffect(() => {
    if (open) {
      setLocalStatus(status);
      setLocalUser(user);
      setLocalAfter(createdAfter ? dayjs(createdAfter) : null);
      setLocalBefore(createdBefore ? dayjs(createdBefore) : null);
    }
  }, [open]);

  const handleApply = () => {
    setFilters({
      status: localStatus,
      user: localUser,
      createdAfter: localAfter?.isValid() ? localAfter.format("YYYY-MM-DD") : "",
      createdBefore: localBefore?.isValid() ? localBefore.format("YYYY-MM-DD") : "",
    });
    onClose?.({}, "backdropClick");
  };

  const handleReset = () => {
    setLocalStatus("");
    setLocalUser("");
    setLocalAfter(null);
    setLocalBefore(null);
    clearFilters();
    onClose?.({}, "backdropClick");
  };

  return (
    <Popover
      anchorOrigin={{ vertical: "bottom", horizontal: "left" }}
      transformOrigin={{ vertical: "top", horizontal: "left" }}
      slotProps={{
        paper: {
          sx: {
            p: 2,
            width: 320,
            maxWidth: "100%",
          },
        },
      }}
      onClose={onClose}
      open={open}
      {...rest}
    >
      <Stack spacing={2}>
        <SettingForm title={t("paymentOrders.userSearch")} noContainer lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            value={localUser}
            onChange={(e) => setLocalUser(e.target.value)}
            placeholder={t("user.emptyNoFilter")}
            size="small"
          />
        </SettingForm>

        <SettingForm title={t("paymentOrders.status")} noContainer lgWidth={12}>
          <DenseSelect
            fullWidth
            displayEmpty
            value={localStatus == "" ? " " : localStatus}
            onChange={(e) => setLocalStatus(e.target.value === " " ? "" : (e.target.value as string))}
          >
            {PaymentOrderStatuses.map((s) => (
              <SquareMenuItem key={s} value={s}>
                <ListItemText
                  primary={getPaymentOrderStatusText(s, t)}
                  slotProps={{ primary: { variant: "body2" } }}
                />
              </SquareMenuItem>
            ))}
            <SquareMenuItem value={" "}>
              <ListItemText primary={<em>{t("user.all")}</em>} slotProps={{ primary: { variant: "body2" } }} />
            </SquareMenuItem>
          </DenseSelect>
        </SettingForm>

        <SettingForm title={t("paymentOrders.createdAfter")} noContainer lgWidth={12}>
          <LocalizationProvider dateAdapter={AdapterDayjs}>
            <DatePicker
              value={localAfter}
              onChange={(v) => setLocalAfter(v)}
              format="YYYY-MM-DD"
              slotProps={{
                textField: { fullWidth: true, size: "small" },
                field: { clearable: true },
              }}
            />
          </LocalizationProvider>
        </SettingForm>

        <SettingForm title={t("paymentOrders.createdBefore")} noContainer lgWidth={12}>
          <LocalizationProvider dateAdapter={AdapterDayjs}>
            <DatePicker
              value={localBefore}
              onChange={(v) => setLocalBefore(v)}
              format="YYYY-MM-DD"
              slotProps={{
                textField: { fullWidth: true, size: "small" },
                field: { clearable: true },
              }}
            />
          </LocalizationProvider>
        </SettingForm>

        <Box display="flex" justifyContent="space-between">
          <Button variant="outlined" size="small" onClick={handleReset}>
            {t("user.reset")}
          </Button>
          <Button variant="contained" size="small" onClick={handleApply}>
            {t("user.apply")}
          </Button>
        </Box>
      </Stack>
    </Popover>
  );
};

export default PaymentOrderFilterPopover;
