import { ListItemIcon, ListItemText, Tooltip } from "@mui/material";
import { useContext, useEffect, useRef, useState } from "react";
import { useSnackbar } from "notistack";
import { bindMenu, bindTrigger, usePopupState } from "material-ui-popup-state/hooks";
import { useTranslation } from "react-i18next";
import { getAllowedPolicies, setPreferredPolicy } from "../../../api/api";
import { FileType, Metadata } from "../../../api/explorer";
import { Filesystem } from "../../../util/uri.ts";
import SessionManager from "../../../session";
import { setPolicyOptionCache } from "../../../redux/globalStateSlice";
import { useAppDispatch, useAppSelector } from "../../../redux/hooks";
import { refreshFileList } from "../../../redux/thunks/filemanager";
import { DefaultCloseAction } from "../../Common/Snackbar/snackbar";
import CloudArrowIUp from "../../Icons/CloudArrowIUp";
import Checkmark from "../../Icons/Checkmark";
import { SquareMenu, SquareMenuItem, DenseDivider } from "../ContextMenu/ContextMenu";
import { ActionButton } from "./TopActions";
import { FmIndexContext } from "../FmIndexContext";

// StoragePolicySwitchButton is the Pro-aligned toolbar entry for switching
// the current directory's storage policy: a button in the top actions that
// opens a menu of the group's allowed policies. Clicking an item binds the
// directory (affects new uploads only); "follow parent" clears the binding
// so the directory inherits from the nearest bound ancestor again.
const StoragePolicySwitchButton = () => {
  const { t } = useTranslation();
  const dispatch = useAppDispatch();
  const fmIndex = useContext(FmIndexContext);
  const { enqueueSnackbar } = useSnackbar();
  const parent = useAppSelector((state) => state.fileManager[fmIndex].list?.parent);
  const currentFs = useAppSelector((state) => state.fileManager[fmIndex].current_fs);
  const searchParams = useAppSelector((state) => state.fileManager[fmIndex].search_params);
  const effectivePolicy = useAppSelector((state) => state.fileManager[fmIndex].list?.storage_policy);
  const policies = useAppSelector((state) => state.globalState.policyOptionCache);
  const [switching, setSwitching] = useState(false);
  const popupState = usePopupState({
    variant: "popover",
    popupId: "storagePolicySwitch",
  });
  const loadingRef = useRef(false);

  const isLogin = !!SessionManager.currentLoginOrNull();
  const visible =
    isLogin &&
    currentFs == Filesystem.my &&
    !searchParams &&
    parent?.type == FileType.folder &&
    !!parent?.owned;

  // Populate the shared policy option cache on first open (the same cache
  // the More-actions dialog path uses, so the two entries stay in sync).
  useEffect(() => {
    if (popupState.isOpen && policies === undefined && !loadingRef.current) {
      loadingRef.current = true;
      dispatch(getAllowedPolicies())
        .then((res) => dispatch(setPolicyOptionCache(res)))
        .catch(() => {})
        .finally(() => {
          loadingRef.current = false;
        });
    }
  }, [popupState.isOpen]);

  // Direct binding marker on the directory itself (public metadata since the
  // Pro-form alignment; "" means inherit). A dangling marker (policy since
  // removed from the allowed set) is treated as no binding.
  const direct = parent?.metadata?.[Metadata.preferred_policy] ?? "";
  const markerValid = (policies ?? []).some((p) => p.id === direct);

  const onSwitch = (policyId: string, name?: string) => {
    if (!parent || switching) {
      return;
    }
    setSwitching(true);
    dispatch(setPreferredPolicy({ uri: parent.path, policy: policyId }))
      .then(() => {
        enqueueSnackbar(
          policyId === "" ? t("fileManager.policyCleared") : t("fileManager.policySwitched", { name }),
          { variant: "success", action: DefaultCloseAction },
        );
        popupState.close();
        dispatch(refreshFileList(fmIndex));
      })
      .catch(() => {})
      .finally(() => setSwitching(false));
  };

  if (!visible) {
    return null;
  }

  return (
    <>
      <Tooltip enterDelay={200} title={t("application:fileManager.switchDirPolicy")}>
        <ActionButton
          {...bindTrigger(popupState)}
          aria-label={t("application:fileManager.switchDirPolicy")}
          disabled={switching}
        >
          <CloudArrowIUp fontSize={"small"} />
        </ActionButton>
      </Tooltip>
      <SquareMenu {...bindMenu(popupState)}>
        <SquareMenuItem onClick={() => onSwitch("")} disabled={switching}>
          <ListItemIcon>{(!direct || !markerValid) && <Checkmark fontSize="small" />}</ListItemIcon>
          <ListItemText
            primary={t("application:fileManager.policyFollowParent")}
            secondary={
              effectivePolicy
                ? t("application:fileManager.policyEffective", { name: effectivePolicy.name })
                : undefined
            }
          />
        </SquareMenuItem>
        <DenseDivider />
        {(policies ?? []).map((p) => (
          <SquareMenuItem
            key={p.id}
            onClick={() => onSwitch(p.id, p.name)}
            disabled={switching}
            title={t("application:fileManager.dirPolicyDes", { name: parent?.name ?? "" })}
          >
            <ListItemIcon>{markerValid && direct === p.id && <Checkmark fontSize="small" />}</ListItemIcon>
            <ListItemText
              primary={p.name}
              secondary={p.is_default ? t("application:fileManager.policyGroupDefault") : undefined}
            />
          </SquareMenuItem>
        ))}
      </SquareMenu>
    </>
  );
};

export default StoragePolicySwitchButton;
