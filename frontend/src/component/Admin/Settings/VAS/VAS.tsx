import { Box, Chip, FormControl, FormControlLabel, Menu, MenuItem, Stack, Switch, Typography } from "@mui/material";
import { useContext, useState } from "react";
import { useTranslation } from "react-i18next";
import { isTrueVal } from "../../../../session/utils.ts";
import { DenseFilledTextField, SecondaryButton } from "../../../Common/StyledComponents.tsx";
import SettingForm from "../../../Pages/Setting/SettingForm.tsx";
import { NoMarginHelperText, SettingSection, SettingSectionContent } from "../Settings.tsx";
import { SettingContext } from "../SettingWrapper.tsx";
import GiftCodes from "./GiftCodes.tsx";
import SkuTable from "./SkuTable.tsx";

// Common currencies offered by the picker, mirroring the Pro admin panel.
const commonCurrencies = [
  { code: "USD", mark: "$", unit: 100, label: "US Dollar ($)" },
  { code: "EUR", mark: "€", unit: 100, label: "Euro (€)" },
  { code: "GBP", mark: "£", unit: 100, label: "British Pound (£)" },
  { code: "JPY", mark: "¥", unit: 1, label: "Japanese Yen (¥)" },
  { code: "CNY", mark: "¥", unit: 100, label: "Chinese Yuan (¥)" },
  { code: "HKD", mark: "$", unit: 100, label: "Hong Kong Dollar ($)" },
  { code: "SGD", mark: "$", unit: 100, label: "Singapore Dollar ($)" },
  { code: "KRW", mark: "₩", unit: 1, label: "Korean Won (₩)" },
  { code: "INR", mark: "₹", unit: 100, label: "Indian Rupee (₹)" },
  { code: "RUB", mark: "₽", unit: 100, label: "Russian Ruble (₽)" },
  { code: "BRL", mark: "R", unit: 100, label: "Brazilian Real (R)" },
  { code: "AUD", mark: "$", unit: 100, label: "Australian Dollar ($)" },
  { code: "CAD", mark: "$", unit: 100, label: "Canadian Dollar ($)" },
  { code: "CHF", mark: "F", unit: 100, label: "Swiss Franc (F)" },
];

// Providers the fork backend understands. Pro additionally offers WeChat,
// Alipay and custom channels; they are not shown until the backend grows
// support for them.

const VAS = () => {
  const { t } = useTranslation("dashboard");
  const { formRef, setSettings, values } = useContext(SettingContext);
  const [currencyAnchor, setCurrencyAnchor] = useState<HTMLElement | null>(null);
  const [providerAnchor, setProviderAnchor] = useState<HTMLElement | null>(null);

  const stripeEnabled = isTrueVal(values.payment_stripe_enabled ?? "0");
  const easypayEnabled = isTrueVal(values.payment_easypay_enabled ?? "0");

  return (
    <Box component={"form"} ref={formRef}>
      <Stack spacing={5}>
        <SettingSection>
          <Typography variant="h6" gutterBottom sx={{ display: "flex", alignItems: "center" }}>
            {t("settings.creditAndVAS")}
          </Typography>
          <SettingSectionContent>
            <SettingForm lgWidth={5}>
              <FormControl fullWidth>
                <FormControlLabel
                  control={
                    <Switch
                      checked={isTrueVal(values.score_enabled ?? "1")}
                      onChange={(e) => setSettings({ score_enabled: e.target.checked ? "1" : "0" })}
                    />
                  }
                  label={t("settings.enableCredit")}
                />
                <NoMarginHelperText>{t("settings.enableCreditDes")}</NoMarginHelperText>
              </FormControl>
            </SettingForm>

            <SettingForm title={t("settings.shareScoreRate")} lgWidth={5}>
              <FormControl fullWidth>
                <DenseFilledTextField
                  type="number"
                  value={values.share_score_rate ?? "100"}
                  slotProps={{ htmlInput: { min: 0, max: 100 } }}
                  onChange={(e) => {
                    const v = Math.max(0, Math.min(100, Math.floor(Number(e.target.value) || 0)));
                    setSettings({ share_score_rate: v.toString() });
                  }}
                />
                <NoMarginHelperText>{t("settings.shareScoreRateDes")}</NoMarginHelperText>
              </FormControl>
            </SettingForm>

            <SettingForm lgWidth={5}>
              <FormControl fullWidth>
                <FormControlLabel
                  control={
                    <Switch
                      checked={isTrueVal(values.shop_nav ?? "1")}
                      onChange={(e) => setSettings({ shop_nav: e.target.checked ? "1" : "0" })}
                    />
                  }
                  label={t("settings.shopNavEnabled")}
                />
                <NoMarginHelperText>{t("settings.shopNavEnabledDes")}</NoMarginHelperText>
              </FormControl>
            </SettingForm>
          </SettingSectionContent>
        </SettingSection>

        <SettingSection>
          <Typography variant="h6" gutterBottom sx={{ display: "flex", alignItems: "center" }}>
            {t("settings.paymentSettings")}
          </Typography>
          <SettingSectionContent>
            <SettingForm
              title={
                <Stack direction="row" spacing={1} alignItems="center">
                  <span>{t("settings.currencyCode")}</span>
                  <SecondaryButton size="small" sx={{ ml: 1 }} onClick={(e) => setCurrencyAnchor(e.currentTarget)}>
                    {t("settings.selectCurrency")}
                  </SecondaryButton>
                </Stack>
              }
              lgWidth={5}
            >
              <FormControl fullWidth>
                <DenseFilledTextField
                  value={values.payment_currency_code ?? "usd"}
                  slotProps={{ htmlInput: { maxLength: 3 } }}
                  onChange={(e) => setSettings({ payment_currency_code: e.target.value.trim().toUpperCase() })}
                />
                <NoMarginHelperText>{t("settings.currencyCodeDes")}</NoMarginHelperText>
              </FormControl>
            </SettingForm>

            <SettingForm title={t("settings.currencySymbol")} lgWidth={5}>
              <FormControl fullWidth>
                <DenseFilledTextField
                  value={values.payment_currency_mark ?? "$"}
                  onChange={(e) => setSettings({ payment_currency_mark: e.target.value })}
                />
                <NoMarginHelperText>{t("settings.currencySymbolDes")}</NoMarginHelperText>
              </FormControl>
            </SettingForm>

            <SettingForm title={t("settings.currencyUnit")} lgWidth={5}>
              <FormControl fullWidth>
                <DenseFilledTextField
                  type="number"
                  value={values.payment_currency_unit ?? "100"}
                  slotProps={{ htmlInput: { min: 1 } }}
                  onChange={(e) => {
                    const v = Math.max(1, Math.floor(Number(e.target.value) || 1));
                    setSettings({ payment_currency_unit: v.toString() });
                  }}
                />
                <NoMarginHelperText>{t("settings.currencyUnitDes")}</NoMarginHelperText>
              </FormControl>
            </SettingForm>
          </SettingSectionContent>
        </SettingSection>

        <SettingSection>
          <Typography variant="h6" gutterBottom sx={{ display: "flex", alignItems: "center" }}>
            {t("settings.paymentProviders")}
          </Typography>
          <SettingSectionContent>
            <SettingForm lgWidth={12}>
              <SecondaryButton variant="contained" onClick={(e) => setProviderAnchor(e.currentTarget)}>
                {t("settings.addPaymentProvider")}
              </SecondaryButton>

              {stripeEnabled && (
                <Stack
                  spacing={2}
                  sx={{
                    mt: 2,
                    p: 2,
                    border: (theme) => `1px solid ${theme.palette.divider}`,
                    borderRadius: 1,
                  }}
                >
                  <Stack direction="row" spacing={1} alignItems="center" sx={{ width: "100%" }}>
                    <Chip size="small" label={t("settings.stripeProvider")} />
                    <Typography variant="body2" color="text.secondary" sx={{ flexGrow: 1 }}>
                      {t("settings.paymentStripeEnabledDes")}
                    </Typography>
                    <Switch
                      size="small"
                      checked={stripeEnabled}
                      onChange={(e) => setSettings({ payment_stripe_enabled: e.target.checked ? "1" : "0" })}
                    />
                  </Stack>
                  <SettingForm title={t("settings.paymentStripePublishableKey")} lgWidth={5}>
                    <FormControl fullWidth>
                      <DenseFilledTextField
                        value={values.payment_stripe_publishable_key ?? ""}
                        onChange={(e) => setSettings({ payment_stripe_publishable_key: e.target.value })}
                      />
                      <NoMarginHelperText>{t("settings.paymentStripePublishableKeyDes")}</NoMarginHelperText>
                    </FormControl>
                  </SettingForm>
                  <SettingForm title={t("settings.paymentStripeSecretKey")} lgWidth={5}>
                    <FormControl fullWidth>
                      <DenseFilledTextField
                        type="password"
                        value={values.payment_stripe_secret_key ?? ""}
                        placeholder={t("settings.secretKeepHint")}
                        onChange={(e) => setSettings({ payment_stripe_secret_key: e.target.value })}
                      />
                      <NoMarginHelperText>{t("settings.paymentStripeSecretKeyDes")}</NoMarginHelperText>
                    </FormControl>
                  </SettingForm>
                  <SettingForm title={t("settings.paymentStripeWebhookSecret")} lgWidth={5}>
                    <FormControl fullWidth>
                      <DenseFilledTextField
                        type="password"
                        value={values.payment_stripe_webhook_secret ?? ""}
                        placeholder={t("settings.secretKeepHint")}
                        onChange={(e) => setSettings({ payment_stripe_webhook_secret: e.target.value })}
                      />
                      <NoMarginHelperText>
                        {t("settings.paymentStripeWebhookSecretDes", {
                          url: `${window.location.origin}/api/v4/callback/stripe`,
                        })}
                      </NoMarginHelperText>
                    </FormControl>
                  </SettingForm>
                </Stack>
              )}

              {easypayEnabled && (
                <Stack
                  spacing={2}
                  sx={{
                    mt: 2,
                    p: 2,
                    border: (theme) => `1px solid ${theme.palette.divider}`,
                    borderRadius: 1,
                  }}
                >
                  <Stack direction="row" spacing={1} alignItems="center" sx={{ width: "100%" }}>
                    <Chip size="small" label={t("settings.easypayProvider")} />
                    <Typography variant="body2" color="text.secondary" sx={{ flexGrow: 1 }}>
                      {t("settings.paymentEasypayEnabledDes")}
                    </Typography>
                    <Switch
                      size="small"
                      checked={easypayEnabled}
                      onChange={(e) => setSettings({ payment_easypay_enabled: e.target.checked ? "1" : "0" })}
                    />
                  </Stack>
                  <SettingForm title={t("settings.paymentEasypayGateway")} lgWidth={5}>
                    <FormControl fullWidth>
                      <DenseFilledTextField
                        value={values.payment_easypay_gateway ?? ""}
                        placeholder="https://pay.example.com"
                        onChange={(e) => setSettings({ payment_easypay_gateway: e.target.value.trim() })}
                      />
                      <NoMarginHelperText>{t("settings.paymentEasypayGatewayDes")}</NoMarginHelperText>
                    </FormControl>
                  </SettingForm>
                  <SettingForm title={t("settings.paymentEasypayPid")} lgWidth={5}>
                    <FormControl fullWidth>
                      <DenseFilledTextField
                        value={values.payment_easypay_pid ?? ""}
                        onChange={(e) => setSettings({ payment_easypay_pid: e.target.value.trim() })}
                      />
                      <NoMarginHelperText>{t("settings.paymentEasypayPidDes")}</NoMarginHelperText>
                    </FormControl>
                  </SettingForm>
                  <SettingForm title={t("settings.paymentEasypayKey")} lgWidth={5}>
                    <FormControl fullWidth>
                      <DenseFilledTextField
                        type="password"
                        value={values.payment_easypay_key ?? ""}
                        placeholder={t("settings.secretKeepHint")}
                        onChange={(e) => setSettings({ payment_easypay_key: e.target.value })}
                      />
                      <NoMarginHelperText>
                        {t("settings.paymentEasypayKeyDes", {
                          notify: `${window.location.origin}/api/v4/callback/easypay/notify`,
                          ret: `${window.location.origin}/api/v4/callback/easypay/return`,
                        })}
                      </NoMarginHelperText>
                    </FormControl>
                  </SettingForm>
                  <SettingForm title={t("settings.paymentEasypayType")} lgWidth={5}>
                    <FormControl fullWidth>
                      <DenseFilledTextField
                        value={values.payment_easypay_type ?? ""}
                        placeholder="alipay"
                        onChange={(e) => setSettings({ payment_easypay_type: e.target.value.trim() })}
                      />
                      <NoMarginHelperText>{t("settings.paymentEasypayTypeDes")}</NoMarginHelperText>
                    </FormControl>
                  </SettingForm>
                </Stack>
              )}
            </SettingForm>
          </SettingSectionContent>
        </SettingSection>

        <SettingSection>
          <Typography variant="h6" gutterBottom sx={{ display: "flex", alignItems: "center" }}>
            {t("settings.storageProductSettings")}
          </Typography>
          <SettingSectionContent>
            <SettingForm lgWidth={12}>
              <FormControl fullWidth>
                <SkuTable type="storage" />
                <NoMarginHelperText>{t("settings.storageProductsDes")}</NoMarginHelperText>
              </FormControl>
            </SettingForm>
          </SettingSectionContent>
        </SettingSection>

        <SettingSection>
          <Typography variant="h6" gutterBottom sx={{ display: "flex", alignItems: "center" }}>
            {t("settings.groupProductSettings")}
          </Typography>
          <SettingSectionContent>
            <SettingForm lgWidth={12}>
              <FormControl fullWidth>
                <SkuTable type="group" />
                <NoMarginHelperText>{t("settings.groupProductsDes")}</NoMarginHelperText>
              </FormControl>
            </SettingForm>
          </SettingSectionContent>
        </SettingSection>

        <SettingSection>
          <Typography variant="h6" gutterBottom sx={{ display: "flex", alignItems: "center" }}>
            {t("settings.trafficProductSettings")}
          </Typography>
          <SettingSectionContent>
            <SettingForm lgWidth={12}>
              <FormControl fullWidth>
                <SkuTable type="traffic" />
                <NoMarginHelperText>{t("settings.trafficProductsDes")}</NoMarginHelperText>
              </FormControl>
            </SettingForm>
          </SettingSectionContent>
        </SettingSection>

        <SettingSection>
          <Typography variant="h6" gutterBottom sx={{ display: "flex", alignItems: "center" }}>
            {t("settings.streamTrafficProductSettings")}
          </Typography>
          <SettingSectionContent>
            <SettingForm lgWidth={12}>
              <FormControl fullWidth>
                <SkuTable type="stream_traffic" />
                <NoMarginHelperText>{t("settings.streamTrafficProductsDes")}</NoMarginHelperText>
              </FormControl>
            </SettingForm>
          </SettingSectionContent>
        </SettingSection>

        <SettingSection>
          <Typography variant="h6" gutterBottom sx={{ display: "flex", alignItems: "center" }}>
            {t("giftCodes.giftCodesSettings")}
          </Typography>
          <SettingSectionContent>
            <GiftCodes />
          </SettingSectionContent>
        </SettingSection>
      </Stack>

      <Menu
        anchorEl={currencyAnchor}
        open={Boolean(currencyAnchor)}
        onClose={() => setCurrencyAnchor(null)}
        slotProps={{ paper: { sx: { maxHeight: 360 } } }}
      >
        {commonCurrencies.map((c) => (
          <MenuItem
            key={c.code}
            selected={(values.payment_currency_code ?? "usd").toLowerCase() === c.code.toLowerCase()}
            onClick={() => {
              setSettings({
                payment_currency_code: c.code,
                payment_currency_mark: c.mark,
                payment_currency_unit: c.unit.toString(),
              });
              setCurrencyAnchor(null);
            }}
          >
            {c.label}
          </MenuItem>
        ))}
      </Menu>

      <Menu anchorEl={providerAnchor} open={Boolean(providerAnchor)} onClose={() => setProviderAnchor(null)}>
        <MenuItem
          disabled={stripeEnabled}
          onClick={() => {
            setSettings({ payment_stripe_enabled: "1" });
            setProviderAnchor(null);
          }}
        >
          {t("settings.stripeProvider")}
        </MenuItem>
        <MenuItem
          disabled={easypayEnabled}
          onClick={() => {
            setSettings({ payment_easypay_enabled: "1" });
            setProviderAnchor(null);
          }}
        >
          {t("settings.easypayProvider")}
        </MenuItem>
        {/* WeChat Pay / Alipay direct / custom channels: backend support
            pending, hidden from the picker until implemented. */}
      </Menu>
    </Box>
  );
};

export default VAS;
