import { Alert, AlertTitle, Box, Link, Stack, Typography } from "@mui/material";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link as RouterLink } from "react-router-dom";
import { getCredentialRefreshNotices, RefreshNotice } from "../../../api/api.ts";
import { useAppDispatch } from "../../../redux/hooks.ts";

// CredentialNotice shows the storage policies whose automatically renewed
// credential is currently failing. It is data driven: once a renewal succeeds
// the backend clears the flag and the banner disappears on the next load, so it
// never turns into a permanent red bar.
const CredentialNotice = () => {
  const { t } = useTranslation("dashboard");
  const dispatch = useAppDispatch();
  const [notices, setNotices] = useState<RefreshNotice[]>([]);

  const load = useCallback(() => {
    dispatch(getCredentialRefreshNotices())
      .then((r) => setNotices(Array.isArray(r) ? r : []))
      .catch(() => setNotices([]));
  }, []);

  useEffect(() => {
    load();
  }, []);

  if (notices.length === 0) {
    return null;
  }

  const describe = (n: RefreshNotice) => {
    switch (n.kind) {
      case "expired":
        return t("summary.credentialExpired", {
          name: n.policy_name,
          defaultValue: "「{{name}}」的移动云盘凭证已过期，续期失败，请手动更新凭证。",
        });
      case "persist_failed":
        return t("summary.credentialPersistFailed", {
          name: n.policy_name,
          defaultValue: "「{{name}}」的移动云盘凭证已续期但未能保存，重启后会回退，请检查数据库。",
        });
      default:
        return t("summary.credentialRefreshFailed", {
          name: n.policy_name,
          defaultValue: "「{{name}}」的移动云盘凭证续期失败，请手动更新凭证。",
        });
    }
  };

  return (
    <Alert severity="warning" sx={{ mb: 3 }}>
      <AlertTitle>{t("summary.credentialNoticeTitle", { defaultValue: "存储策略凭证需要处理" })}</AlertTitle>
      <Stack spacing={0.5}>
        {notices.map((n) => (
          <Box key={n.policy_id}>
            <Typography variant="body2" component="span">
              {describe(n)}
            </Typography>{" "}
            <Link component={RouterLink} to={`/admin/policy/${n.policy_id}`} variant="body2">
              {t("summary.goToPolicy", { defaultValue: "去处理" })}
            </Link>
          </Box>
        ))}
      </Stack>
    </Alert>
  );
};

export default CredentialNotice;
