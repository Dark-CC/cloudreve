import { Alert, Box, Link, Stack } from "@mui/material";
import { useContext } from "react";
import { Trans } from "react-i18next";
import { Link as RouterLink } from "react-router-dom";
import { PolicyPropsMap } from "../policyProps";
import { PolicyType } from "../../../../api/explorer";
import {
  BasicInfoSection,
  DownloadSection,
  EncryptionSection,
  LoadBalanceSection,
  MediaMetadataSection,
  StorageAndUploadSection,
  ThumbnailsSection,
} from "./FormSections";
import { StoragePolicySettingContext } from "./StoragePolicySettingWrapper";

const StoragePolicyForm = () => {
  const { formRef, values } = useContext(StoragePolicySettingContext);

  // A policy whose type this build has no driver for (created by another
  // Cloudreve build, or its driver was removed) has no meaningful form: every
  // type specific section would render empty defaults and saving it would
  // overwrite settings we cannot represent. Show an explanatory banner instead.
  const recognizedType = !!PolicyPropsMap[values.type as PolicyType];

  if (!recognizedType) {
    return (
      <Box component={"form"} ref={formRef} onSubmit={(e) => e.preventDefault()}>
        <Alert severity="warning">
          <Trans
            i18nKey="policy.unknownPolicyTypeHint"
            ns="dashboard"
            values={{ type: values.type }}
            components={[<strong />]}
          />
        </Alert>
      </Box>
    );
  }

  return (
    <Box component={"form"} ref={formRef} onSubmit={(e) => e.preventDefault()}>
      {!values.edges?.groups?.length && (
        <Alert severity="warning" sx={{ mb: 3 }}>
          <Trans
            i18nKey="policy.noBindedGroupWarning"
            ns="dashboard"
            components={[<Link component={RouterLink} to="/admin/group" />]}
          />
        </Alert>
      )}
      <Stack spacing={5}>
        <BasicInfoSection />
        <LoadBalanceSection />
        <StorageAndUploadSection />
        <DownloadSection />
        <ThumbnailsSection />
        <MediaMetadataSection />
        <EncryptionSection />
      </Stack>
    </Box>
  );
};

export default StoragePolicyForm;
