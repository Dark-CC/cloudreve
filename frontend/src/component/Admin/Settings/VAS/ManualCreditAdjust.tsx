import { Grid2 } from "@mui/material";
import { useSnackbar } from "notistack";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAppDispatch } from "../../../../redux/hooks.ts";
import { adminAdjustCredit } from "../../../../api/api.ts";
import { DenseFilledTextField, SecondaryButton } from "../../../Common/StyledComponents.tsx";
import SettingForm from "../../../Pages/Setting/SettingForm.tsx";
import { NoMarginHelperText } from "../Settings.tsx";

const ManualCreditAdjust = () => {
  const { t } = useTranslation("dashboard");
  const dispatch = useAppDispatch();
  const { enqueueSnackbar } = useSnackbar();
  const [email, setEmail] = useState("");
  const [delta, setDelta] = useState(0);
  const [des, setDes] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const onSubmit = () => {
    setSubmitting(true);
    dispatch(adminAdjustCredit({ email: email.trim(), delta, des: des || undefined }))
      .then(() => {
        enqueueSnackbar(t("vas.adjustSuccess"), { variant: "success" });
        setDelta(0);
        setDes("");
      })
      .catch(() => enqueueSnackbar(t("vas.adjustFailed"), { variant: "error" }))
      .finally(() => setSubmitting(false));
  };

  return (
    <Grid2 container spacing={1} sx={{ width: "100%", alignItems: "flex-start" }}>
      <SettingForm noContainer title={t("vas.adjustEmail")} lgWidth={4}>
        <DenseFilledTextField fullWidth value={email} onChange={(e) => setEmail(e.target.value)} />
      </SettingForm>
      <SettingForm noContainer title={t("vas.adjustDelta")} lgWidth={3}>
        <DenseFilledTextField
          fullWidth
          type="number"
          value={delta}
          onChange={(e) => setDelta(parseInt(e.target.value) || 0)}
        />
        <NoMarginHelperText>{t("vas.adjustDeltaDes")}</NoMarginHelperText>
      </SettingForm>
      <SettingForm noContainer title={t("vas.adjustNote")} lgWidth={4}>
        <DenseFilledTextField fullWidth value={des} onChange={(e) => setDes(e.target.value)} />
      </SettingForm>
      {/* alignSelf center: 与输入框行垂直居中（顶对齐后字段块因 helper 文案高度不一，按钮在行内居中恰好对齐输入框） */}
      <Grid2 size={{ xs: 12, lg: 1 }} sx={{ display: "flex", justifyContent: "flex-end", alignSelf: { lg: "center" } }}>
        <SecondaryButton
          variant="contained"
          onClick={onSubmit}
          disabled={submitting || !email.trim() || delta === 0}
          sx={{ whiteSpace: "nowrap", flexShrink: 0 }}
        >
          {t("vas.adjustSubmit")}
        </SecondaryButton>
      </Grid2>
    </Grid2>
  );
};

export default ManualCreditAdjust;
