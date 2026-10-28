import { Trans, useTranslation } from "react-i18next";
import { FormControl, Link, Stack } from "@mui/material";
import SettingForm from "../../../Pages/Setting/SettingForm.tsx";
import { DenseFilledTextField } from "../../../Common/StyledComponents.tsx";
import * as React from "react";
import { NoMarginHelperText } from "../Settings.tsx";

export interface GeeTestCaptchaProps {
  values: {
    [key: string]: string;
  };
  setSettings: (settings: { [key: string]: string }) => void;
}

const GeeTest = ({ values, setSettings }: GeeTestCaptchaProps) => {
  const { t } = useTranslation("dashboard");
  return (
    <Stack spacing={3}>
      <SettingForm title={t("settings.geetestCaptchaId")} lgWidth={5}>
        <FormControl fullWidth>
          <DenseFilledTextField
            value={values.captcha_geetest_captcha_id}
            onChange={(e) =>
              setSettings({
                captcha_geetest_captcha_id: e.target.value,
              })
            }
            required
          />
          <NoMarginHelperText>
            <Trans
              i18nKey="settings.geetestCaptchaIdDes"
              ns={"dashboard"}
              components={[<Link key={0} href={"https://dashboard.geetest.com/"} target={"_blank"} />]}
            />
          </NoMarginHelperText>
        </FormControl>
      </SettingForm>
      <SettingForm title={t("settings.geetestCaptchaKey")} lgWidth={5}>
        <FormControl fullWidth>
          <DenseFilledTextField
            value={values.captcha_geetest_captcha_key}
            onChange={(e) =>
              setSettings({
                captcha_geetest_captcha_key: e.target.value,
              })
            }
            required
          />
          <NoMarginHelperText>
            <Trans
              i18nKey="settings.geetestCaptchaKeyDes"
              ns={"dashboard"}
              components={[<Link key={0} href={"https://dashboard.geetest.com/"} target={"_blank"} />]}
            />
          </NoMarginHelperText>
        </FormControl>
      </SettingForm>
    </Stack>
  );
};

export default GeeTest;
