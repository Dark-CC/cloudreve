import { Button, Stack } from "@mui/material";
import { useSnackbar } from "notistack";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { redeemGiftCode } from "../../../api/api.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";
import { DenseFilledTextField } from "../StyledComponents.tsx";

export interface RedeemCodeInputProps {
  onRedeemed?: () => void;
}

const RedeemCodeInput = ({ onRedeemed }: RedeemCodeInputProps) => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const { enqueueSnackbar } = useSnackbar();
  const [code, setCode] = useState("");
  const [redeeming, setRedeeming] = useState(false);

  const onRedeem = () => {
    if (!code.trim()) {
      return;
    }
    setRedeeming(true);
    dispatch(redeemGiftCode(code.trim()))
      .then(() => {
        enqueueSnackbar(t("setting.giftCodeRedeemed"), { variant: "success" });
        setCode("");
        onRedeemed?.();
      })
      .finally(() => setRedeeming(false));
  };

  return (
    // 兑换按钮放在输入框右侧（同一行），而不是嵌在输入框内。
    <Stack direction="row" spacing={1} alignItems="center">
      <DenseFilledTextField
        value={code}
        onChange={(e) => setCode(e.target.value)}
        placeholder={t("setting.giftCodePlaceholder")}
        sx={{ flexGrow: 1, minWidth: 0 }}
      />
      <Button variant="contained" onClick={onRedeem} disabled={redeeming || !code.trim()} sx={{ flexShrink: 0 }}>
        {t("setting.redeem")}
      </Button>
    </Stack>
  );
};

export default RedeemCodeInput;
