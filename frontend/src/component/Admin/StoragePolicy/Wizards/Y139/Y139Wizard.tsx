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
 * Y139Wizard collects the browser session of the China Mobile cloud drive (139).
 *
 * The drive has no third party OAuth application: every request must carry the
 * Authorization header the official web client sends. The administrator copies
 * it out of the devtools network panel once; the driver keeps the session alive
 * by refreshing the token before it expires.
 *
 * The same four fields are editable later on the policy detail page, except the
 * endpoint, which neither form shows: the driver discovers it from the
 * credentials (`queryRoutePolicy`) and the optional `server` value a policy may
 * carry is an operator override, not something to type in an add dialog.
 */
const Y139Wizard = ({ onSubmit }: AddWizardProps) => {
  const { t } = useTranslation("dashboard");
  const formRef = useRef<HTMLFormElement>(null);
  const [policy, setPolicy] = useState<StoragePolicy>({
    id: 0,
    node_id: 0,
    name: "",
    type: PolicyType.y139,
    is_private: true,
    dir_name_rule: "uploads/{uid}/{path}",
    settings: {
      chunk_size: 100 << 20,
    },
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

        <SettingForm title={t("policy.y139Authorization")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            required
            multiline
            minRows={3}
            placeholder="Basic ..."
            value={policy.access_key}
            onChange={(e) => setPolicy({ ...policy, access_key: e.target.value })}
          />
          <NoMarginHelperText>{t("policy.y139AuthorizationDes")}</NoMarginHelperText>
        </SettingForm>

        <SettingForm title={t("policy.y139AppKey")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            required
            value={policy.secret_key}
            onChange={(e) => setPolicy({ ...policy, secret_key: e.target.value })}
          />
          <NoMarginHelperText>{t("policy.y139AppKeyDes")}</NoMarginHelperText>
        </SettingForm>

        <SettingForm title={t("policy.y139Root")} lgWidth={12}>
          <DenseFilledTextField
            fullWidth
            value={policy.bucket_name}
            onChange={(e) => setPolicy({ ...policy, bucket_name: e.target.value })}
          />
          <NoMarginHelperText>{t("policy.y139RootDes")}</NoMarginHelperText>
        </SettingForm>

        {/* No endpoint field: the drive resolves its storage host from the
            credentials at request time, so there is nothing for the
            administrator to fill in here. */}
        <Alert severity="warning">
          <Typography variant="body2" component="div" sx={{ whiteSpace: "pre-line" }}>
            {t("policy.y139Risk")}
          </Typography>
          <Typography variant="body2" sx={{ mt: 1 }}>
            {t("policy.y139Help")}{" "}
            <Link href="https://yun.139.com/" target="_blank" rel="noreferrer">
              yun.139.com
            </Link>{" "}
            <Code>F12</Code>
          </Typography>
        </Alert>
      </Stack>

      <Button variant="contained" color="primary" sx={{ mt: 2 }} onClick={handleSubmit}>
        {t("policy.create")}
      </Button>
    </form>
  );
};

export default Y139Wizard;
