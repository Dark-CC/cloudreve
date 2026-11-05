import { Box } from "@mui/material";
import * as React from "react";
import { createContext, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { CSSTransition, SwitchTransition } from "react-transition-group";
import { getStoragePolicyDetail, upsertStoragePolicy } from "../../../../api/api.ts";
import { StoragePolicy } from "../../../../api/dashboard.ts";
import { PolicyType } from "../../../../api/explorer.ts";
import { useAppDispatch } from "../../../../redux/hooks.ts";
import FacebookCircularProgress from "../../../Common/CircularProgress.tsx";
import { SavingFloat } from "../../Settings/SettingWrapper.tsx";
import { SharePointDriverPending } from "./FormSections/BasicInfoSection.tsx";

export interface StoragePolicySettingWrapperProps {
  policyID: number;
  children: React.ReactNode;
  onPolicyChange: (policy: StoragePolicy) => void;
}

export interface StoragePolicySettingContextProps {
  values: StoragePolicy;
  setPolicy: (f: (p: StoragePolicy) => StoragePolicy) => void;
  formRef?: React.RefObject<HTMLFormElement>;
}

const defaultPolicy: StoragePolicy = {
  id: 0,
  type: PolicyType.local,
  name: "",
  edges: {},
  dir_name_rule: "uploads/{uid}/{path}",
  file_name_rule: "{originname}",
};

export const StoragePolicySettingContext = createContext<StoragePolicySettingContextProps>({
  values: { ...defaultPolicy },
  setPolicy: () => {},
});

const StoragePolicySettingWrapper = ({ policyID, children, onPolicyChange }: StoragePolicySettingWrapperProps) => {
  const dispatch = useAppDispatch();
  const { t } = useTranslation("dashboard");
  const [values, setValues] = useState<StoragePolicy>({
    ...defaultPolicy,
  });
  const [modifiedValues, setModifiedValues] = useState<StoragePolicy>({
    ...defaultPolicy,
  });
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const formRef = useRef<HTMLFormElement>(null);

  const showSaveButton = useMemo(() => {
    return JSON.stringify(modifiedValues) !== JSON.stringify(values);
  }, [modifiedValues, values]);

  useEffect(() => {
    setLoading(true);
    dispatch(getStoragePolicyDetail(policyID))
      .then((res) => {
        setValues(res);
        setModifiedValues(res);
        onPolicyChange(res);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [policyID]);

  const revert = () => {
    setModifiedValues(values);
  };

  const submit = () => {
    if (formRef.current) {
      if (!formRef.current.checkValidity()) {
        formRef.current.reportValidity();
        return;
      }
    }

    // A policy saved with an empty path rule expands to an empty save path, so
    // every upload against it fails or lands as a nameless object and the file
    // stays listed as "uploading" forever. The backend applies the same
    // defaults, this only keeps the form from ever submitting blank rules.
    const payload: StoragePolicy = {
      ...modifiedValues,
      edges: {},
      dir_name_rule:
        modifiedValues.dir_name_rule?.trim() || defaultPolicy.dir_name_rule || "uploads/{uid}/{path}",
      file_name_rule: modifiedValues.file_name_rule?.trim() || defaultPolicy.file_name_rule || "{originname}",
    };

    setSubmitting(true);
    dispatch(
      upsertStoragePolicy({
        policy: payload,
      }),
    )
      .then((res) => {
        setValues(res);
        setModifiedValues(res);
        onPolicyChange(res);
      })
      .finally(() => {
        setSubmitting(false);
      });
  };

  return (
    <StoragePolicySettingContext.Provider
      value={{
        values: modifiedValues,
        setPolicy: setModifiedValues,
        formRef,
      }}
    >
      <SwitchTransition>
        <CSSTransition
          addEndListener={(node, done) => node.addEventListener("transitionend", done, false)}
          classNames="fade"
          key={`${loading}`}
        >
          <Box sx={{ mt: 3 }}>
            {loading && (
              <Box
                sx={{
                  pt: 20,
                  height: "100%",
                  display: "flex",
                  justifyContent: "center",
                  alignItems: "center",
                }}
              >
                <FacebookCircularProgress />
              </Box>
            )}
            {!loading && (
              <Box>
                {children}
                <SavingFloat
                  in={showSaveButton}
                  disabled={modifiedValues?.settings?.od_driver === SharePointDriverPending}
                  submitting={submitting}
                  revert={revert}
                  submit={submit}
                />
              </Box>
            )}
          </Box>
        </CSSTransition>
      </SwitchTransition>
    </StoragePolicySettingContext.Provider>
  );
};

export default StoragePolicySettingWrapper;
