import { Alert, Button, Link, Stack, Typography } from "@mui/material";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { StoragePolicy } from "../../../../../api/dashboard";
import { PolicyType } from "../../../../../api/explorer";
import { DenseFilledTextField } from "../../../../Common/StyledComponents";
import SettingForm from "../../../../Pages/Setting/SettingForm";
import { Code } from "../../../../Common/Code.tsx";
import { NoMarginHelperText } from "../../../Settings/Settings";
import { AddWizardProps } from "../../AddWizardDialog";

/**
 * OpenListWizard collects the address and owner account of a self-hosted
 * OpenList instance.
 *
 * OpenList is what the site owner runs; Cloudreve only calls its HTTP API with
 * a short-lived token and hands the browser OpenList's own download URL, so no
 * file content ever passes through this site. The instance address is a normal
 * URL, unlike the 139 cloud drive which discovers its host from the credential.
 */
const OpenListWizard = ({ onSubmit }: AddWizardProps) => {
  const { t } = useTranslation("dashboard");
  const formRef = useRef<HTMLFormElement>(null);
  const [policy, setPolicy] = useState<StoragePolicy>({
    id: 0,
    node_id: 0,
    name: "",
    type: PolicyType.openlist,
    is_private: true,
    dir_name_rule: "uploads/{uid}/{path}",
    settings: {},
    file_name_rule: "{originname}",
    edges: {},
  });

  const handleSubmit = () => {
    if (!formRef.current?.checkValidity()) {
      formRef.current?.reportValidity();
      return;
    }
    onSubmit(policy);
  };

  return (
    <form ref={formRef} onSubmit={handleSubmit}>
      <Stack spacing={2}>
        <SettingForm title={t("policy.name")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            required
            value={policy.name}
            onChange={(e) => setPolicy({ ...policy, name: e.target.value })}
          />
          <NoMarginHelperText>{t("policy.policyName")}</NoMarginHelperText>
        </SettingForm>

        <SettingForm title={t("policy.openlistServer")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            required
            placeholder="https://openlist.example.com"
            value={policy.server}
            onChange={(e) => setPolicy({ ...policy, server: e.target.value })}
          />
          <NoMarginHelperText>{t("policy.openlistServerDes")}</NoMarginHelperText>
        </SettingForm>

        <SettingForm title={t("policy.openlistMountPath")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            placeholder="/"
            value={policy.settings?.mount_path}
            onChange={(e) => setPolicy({ ...policy, settings: { ...policy.settings, mount_path: e.target.value } })}
          />
          <NoMarginHelperText>{t("policy.openlistMountPathDes")}</NoMarginHelperText>
        </SettingForm>

        <SettingForm title={t("policy.openlistUsername")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            required
            value={policy.access_key}
            onChange={(e) => setPolicy({ ...policy, access_key: e.target.value })}
          />
          <NoMarginHelperText>{t("policy.openlistUsernameDes")}</NoMarginHelperText>
        </SettingForm>

        <SettingForm title={t("policy.openlistPassword")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            required
            type="password"
            value={policy.secret_key}
            onChange={(e) => setPolicy({ ...policy, secret_key: e.target.value })}
          />
          <NoMarginHelperText>{t("policy.openlistPasswordDes")}</NoMarginHelperText>
        </SettingForm>

        <Alert severity="info">
          <Typography variant="body2" component="div" sx={{ whiteSpace: "pre-line" }}>
            {t("policy.openlistRisk")}
          </Typography>
          <Typography variant="body2" sx={{ mt: 1 }}>
            {t("policy.openlistHelp")}{" "}
            <Link href="https://openlist.team/" target="_blank" rel="noreferrer">
              openlist.team
            </Link>{" "}
            <Code>{t("policy.openlistReadOnly")}</Code>
          </Typography>
        </Alert>
      </Stack>

      <Button variant="contained" color="primary" sx={{ mt: 2 }} onClick={handleSubmit}>
        {t("policy.create")}
      </Button>
    </form>
  );
};

export default OpenListWizard;
