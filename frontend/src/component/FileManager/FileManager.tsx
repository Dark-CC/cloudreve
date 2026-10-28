import { Box, Stack, useMediaQuery, useTheme } from "@mui/material";
import { useEffect, useRef } from "react";
import { useHotkeys } from "react-hotkeys-hook";
import { useSnackbar } from "notistack";
import { useTranslation } from "react-i18next";
import { getAllowedPolicies, getUserSettings } from "../../api/api.ts";
import useNavigation from "../../hooks/useNavigation.tsx";
import { clearSelected } from "../../redux/fileManagerSlice.ts";
import {
  resetDialogs,
  setNotifyPolicyChange,
  setPolicyOptionCache,
} from "../../redux/globalStateSlice.ts";
import { useAppDispatch, useAppSelector } from "../../redux/hooks.ts";
import { resetFm, selectAll, shortCutDelete } from "../../redux/thunks/filemanager.ts";
import SessionManager from "../../session/index.ts";
import ImageViewer from "../Viewers/ImageViewer/ImageViewer.tsx";
import Explorer from "./Explorer/Explorer.tsx";
import { FmIndexContext } from "./FmIndexContext.tsx";
import PaginationFooter from "./Pagination/PaginationFooter.tsx";
import { ReadMe } from "./ReadMe/ReadMe.tsx";
import Sidebar from "./Sidebar/Sidebar.tsx";
import SidebarDialog from "./Sidebar/SidebarDialog.tsx";
import NavHeader from "./TopBar/NavHeader.tsx";

export const FileManagerIndex = {
  main: 0,
  selector: 1,
};

export interface FileManagerProps {
  index?: number;
  initialPath?: string;
  skipRender?: boolean;
}

export const FileManager = ({ index = 0, initialPath, skipRender }: FileManagerProps) => {
  const dispatch = useAppDispatch();
  const theme = useTheme();
  const isMobile = useMediaQuery(theme.breakpoints.down("sm"));
  const isTablet = useMediaQuery(theme.breakpoints.down("md"));

  useNavigation(index, initialPath);

  const policyCache = useAppSelector((state) => state.globalState.policyOptionCache);
  const notifyPolicyChange = useAppSelector((state) => state.globalState.notifyPolicyChange);
  const currentPath = useAppSelector((state) => state.fileManager[index].path);
  const currentFs = useAppSelector((state) => state.fileManager[index].current_fs);
  // Both fields come from the same list response, so the reminder effect below
  // always sees the (path, policy) pair as the server resolved it — never the
  // navigation path paired with the previous listing's stale policy.
  const listedPath = useAppSelector((state) => state.fileManager[index].list?.parent?.path);
  const effectivePolicyId = useAppSelector((state) => state.fileManager[index].list?.storage_policy?.id);
  const effectivePolicyName = useAppSelector((state) => state.fileManager[index].list?.storage_policy?.name);
  const { enqueueSnackbar } = useSnackbar();
  const { t } = useTranslation();
  // Last (path, policy) seen by the reminder effect; kept in a ref so the
  // effect can tell genuine navigations from policy switches (a switch
  // refreshes the same listing and must not double-toast).
  const lastPolicyHint = useRef<{ path?: string; policyId?: string }>({});

  useEffect(() => {
    if (index == FileManagerIndex.main) {
      dispatch(resetDialogs());
      // Populate the allowed-policy cache for context-menu gating when the
      // session was restored from storage rather than a fresh login.
      if (policyCache === undefined && SessionManager.currentLoginOrNull()) {
        dispatch(getAllowedPolicies())
          .then((policies) => dispatch(setPolicyOptionCache(policies)))
          .catch(() => {});
      }
      // Same for the "policy change reminder" preference.
      if (notifyPolicyChange === undefined && SessionManager.currentLoginOrNull()) {
        dispatch(getUserSettings())
          .then((settings) => dispatch(setNotifyPolicyChange(!!settings.notify_storage_policy_change)))
          .catch(() => {});
      }
      return () => {
        dispatch(resetFm(index));
      };
    }
  }, []);

  // Pro-aligned reminder: when the user navigates into a directory whose
  // effective storage policy differs from the previously listed one, show a
  // one-line toast (opt-in via user settings; only in "my" filesystem where
  // the user's policies apply).
  useEffect(() => {
    if (index != FileManagerIndex.main || currentFs != "my" || !currentPath || !listedPath || !effectivePolicyId) {
      return;
    }
    const prev = lastPolicyHint.current;
    if (
      notifyPolicyChange &&
      prev.path &&
      prev.path != listedPath &&
      prev.policyId &&
      prev.policyId != effectivePolicyId
    ) {
      enqueueSnackbar(t("application:fileManager.enteredPolicyDir", { name: effectivePolicyName }), {
        variant: "info",
        autoHideDuration: 4000,
      });
    }
    lastPolicyHint.current = { path: listedPath, policyId: effectivePolicyId };
  }, [listedPath, effectivePolicyId, notifyPolicyChange, currentFs, currentPath, index]);

  const selectAllRef = useHotkeys<HTMLElement>(
    ["Control+a", "Meta+a"],
    () => {
      dispatch(selectAll(index));
    },
    { enabled: index == FileManagerIndex.main, preventDefault: true },
  );

  const delRef = useHotkeys<HTMLElement>(
    ["meta+backspace", "delete"],
    () => {
      dispatch(shortCutDelete(index));
    },
    { enabled: index == FileManagerIndex.main, preventDefault: true },
  );

  const escRef = useHotkeys<HTMLElement>(
    "esc",
    () => {
      dispatch(clearSelected({ index, value: {} }));
    },
    { enabled: index == FileManagerIndex.main, preventDefault: true },
  );

  if (skipRender) {
    return null;
  }

  return (
    <FmIndexContext.Provider value={index}>
      <Stack
        onClick={(e) => {
          e.currentTarget.focus();
        }}
        ref={(ref) => {
          selectAllRef(ref);
          delRef(ref);
          escRef(ref);
        }}
        direction={"column"}
        sx={{
          flexGrow: 1,
          mb: index == FileManagerIndex.main && !isMobile ? 1 : 0,
          overflow: "auto",
          "&:focus": {
            outline: "none",
          },
        }}
        tabIndex={0}
        spacing={1}
      >
        <NavHeader />
        <Box sx={{ display: "flex", flexGrow: 1, overflowY: "auto" }}>
          <Explorer />
          {index == FileManagerIndex.main && (isTablet ? <SidebarDialog /> : <Sidebar />)}
          {index == FileManagerIndex.main && <ReadMe />}
        </Box>
        <PaginationFooter />
      </Stack>
      {index == FileManagerIndex.main && <ImageViewer />}
    </FmIndexContext.Provider>
  );
};
