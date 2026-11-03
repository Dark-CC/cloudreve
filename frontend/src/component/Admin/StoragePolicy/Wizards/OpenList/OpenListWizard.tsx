import { Alert, Button, Link, Stack, Typography } from "@mui/material";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { StoragePolicy } from "../../../../../api/dashboard";
import { PolicyType } from "../../../../../api/explorer";
import { Code } from "../../../../Common/Code.tsx";
import { DenseFilledTextField } from "../../../../Common/StyledComponents";
import SettingForm from "../../../../Pages/Setting/SettingForm";
import { NoMarginHelperText } from "../../../Settings/Settings";
import { AddWizardProps } from "../../AddWizardDialog";

/**
 * OpenListWizard collects the connection to one OpenList instance.
 *
 * Every site administrator runs their own OpenList deployment; this site only
 * calls its HTTP API and hands the browser OpenList's own download URL, so file
 * content never passes through here. The account needs read access to the mount
 * path filled in below.
 *
 * The policy is anchored to `settings.mount_path`: every request is resolved
 * under it, so the policy can never reach a sibling mount of the same instance.
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
    settings: {
      mount_path: "/",
    },
    file_name_rule: "{originname}",
    edges: {},
  });

  const setMountPath = (mountPath: string) => {
    setPolicy({ ...policy, settings: { ...policy.settings, mount_path: mountPath } });
  };

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

        <SettingForm title={t("policy.openListServer")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            required
            placeholder="https://openlist.example.com"
            value={policy.server}
            onChange={(e) => setPolicy({ ...policy, server: e.target.value })}
          />
          <NoMarginHelperText>{t("policy.openListServerDes")}</NoMarginHelperText>
        </SettingForm>

        <SettingForm title={t("policy.accessCredential")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            required
            placeholder={t("policy.openListUsername")}
            value={policy.access_key}
            onChange={(e) => setPolicy({ ...policy, access_key: e.target.value })}
          />
          <DenseFilledTextField
            fullWidth
            required
            sx={{ mt: 1 }}
            type="password"
            placeholder={t("policy.openListPassword")}
            value={policy.secret_key}
            onChange={(e) => setPolicy({ ...policy, secret_key: e.target.value })}
          />
          <NoMarginHelperText>{t("policy.openListCredentialDes")}</NoMarginHelperText>
        </SettingForm>

        <SettingForm title={t("policy.openListMountPath")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            placeholder="/"
            value={policy.settings?.mount_path ?? ""}
            onChange={(e) => setMountPath(e.target.value)}
          />
          <NoMarginHelperText>{t("policy.openListMountPathDes")}</NoMarginHelperText>
        </SettingForm>

        <Alert severity="info">
          <Typography variant="body2" component="div" sx={{ whiteSpace: "pre-line" }}>
            {t("policy.openListReadOnlyNotice")}
          </Typography>
          <Typography variant="body2" sx={{ mt: 1 }}>
            {t("policy.openListHelp")}{" "}
            <Link href="https://oplist.org" target="_blank" rel="noreferrer">
              <Code>oplist.org</Code>
            </Link>
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
