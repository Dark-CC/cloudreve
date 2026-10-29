import { Box, Container, ListItemText, Stack, Typography } from "@mui/material";
import Grid from "@mui/material/Grid2";
import { SelectChangeEvent } from "@mui/material/Select";
import { useQueryState } from "nuqs";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getStoragePolicyList } from "../../../api/api";
import { StoragePolicy } from "../../../api/dashboard";
import { PolicyType } from "../../../api/explorer";
import { getPolicyProps } from "./policyProps";
import { useAppDispatch } from "../../../redux/hooks";
import { DenseSelect, SecondaryButton } from "../../Common/StyledComponents";
import { SquareMenuItem } from "../../FileManager/ContextMenu/ContextMenu";
import Add from "../../Icons/Add";
import ArrowSync from "../../Icons/ArrowSync";
import PageContainer from "../../Pages/PageContainer";
import PageHeader from "../../Pages/PageHeader";
import { BorderedCardClickable } from "../Common/AdminCard";
import TablePagination from "../Common/TablePagination";
import AddWizardDialog from "./AddWizardDialog";
import SelectProvider from "./SelectProvider";
import StoragePolicyCard from "./StoragePolicyCard";

export const PageQuery = "page";
export const PageSizeQuery = "page_size";
export const OrderByQuery = "order_by";
export const OrderDirectionQuery = "order_direction";
export const PolicyTypeQuery = "policy_type";

// The policy type -> presentation table (and the safe lookup helper) lives in
// ./policyProps so the edit form can use it without an import cycle through
// this page component.
export { PolicyPropsMap, getPolicyProps, unknownPolicyProps } from "./policyProps";
export type { PolicyProps } from "./policyProps";

const StoragePolicySetting = () => {
  const { t } = useTranslation("dashboard");
  const dispatch = useAppDispatch();
  const [loading, setLoading] = useState(true);
  const [policies, setPolicies] = useState<StoragePolicy[]>([]);
  const [page, setPage] = useQueryState(PageQuery, { defaultValue: "1" });
  const [pageSize, setPageSize] = useQueryState(PageSizeQuery, {
    defaultValue: "11",
  });
  const [orderBy, setOrderBy] = useQueryState(OrderByQuery, {
    defaultValue: "",
  });
  const [orderDirection, setOrderDirection] = useQueryState(OrderDirectionQuery, { defaultValue: "desc" });
  const [policyType, setPolicyType] = useQueryState(PolicyTypeQuery, {
    defaultValue: " ",
  });
  const [count, setCount] = useState(0);
  const [selectProviderOpen, setSelectProviderOpen] = useState(false);
  const [newPolicyType, setNewPolicyType] = useState<PolicyType | null>(null);
  const [createNewOpen, setCreateNewOpen] = useState(false);

  const [open, setOpen] = useState(false);
  const pageInt = parseInt(page) ?? 1;
  const pageSizeInt = parseInt(pageSize) ?? 11;

  useEffect(() => {
    fetchPolicies();
  }, [page, pageSize, orderBy, orderDirection, policyType]);

  const fetchPolicies = () => {
    setLoading(true);
    dispatch(
      getStoragePolicyList({
        page: pageInt,
        page_size: pageSizeInt,
        order_by: orderBy ?? "",
        order_direction: orderDirection ?? "desc",
        conditions: {
          policy_type: policyType == " " ? "" : policyType,
        },
      }),
    )
      .then((res) => {
        setPolicies(res.policies);
        setPageSize(res.pagination.page_size.toString());
        setCount(res.pagination.total_items ?? 0);
        setLoading(false);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  const onAddNewPolcyWithType = useCallback((type: PolicyType) => {
    setNewPolicyType(type);
    setSelectProviderOpen(false);
    setCreateNewOpen(true);
  }, []);

  const onPolicyTypeChange = useCallback((e: SelectChangeEvent<unknown>) => {
    setPolicyType(e.target.value as PolicyType);
    setPage("1");
  }, []);

  return (
    <PageContainer>
      <SelectProvider
        open={selectProviderOpen}
        onClose={() => setSelectProviderOpen(false)}
        onSelect={onAddNewPolcyWithType}
      />
      {newPolicyType && (
        <AddWizardDialog open={createNewOpen} onClose={() => setCreateNewOpen(false)} type={newPolicyType} />
      )}
      <Container maxWidth="xl">
        <PageHeader title={t("dashboard:nav.storagePolicy")} />
        <Stack direction="row" spacing={1} sx={{ mb: 2 }}>
          <SecondaryButton onClick={fetchPolicies} disabled={loading} variant={"contained"} startIcon={<ArrowSync />}>
            {t("node.refresh")}
          </SecondaryButton>
          <DenseSelect
            value={policyType}
            onChange={onPolicyTypeChange}
            renderValue={(v) => (
              <Typography variant="body2">
                {v == " " ? t("policy.all") : t(getPolicyProps(v as PolicyType).name)}
              </Typography>
            )}
          >
            <SquareMenuItem value={" "}>
              <ListItemText
                slotProps={{
                  primary: { variant: "body2" },
                }}
              >
                {t("policy.all")}
              </ListItemText>
            </SquareMenuItem>
            {Object.values(PolicyType).map((type) => (
              <SquareMenuItem key={type} value={type}>
                <ListItemText
                  slotProps={{
                    primary: { variant: "body2" },
                  }}
                >
                  {t(getPolicyProps(type).name)}
                </ListItemText>
              </SquareMenuItem>
            ))}
          </DenseSelect>
        </Stack>
        <Grid container spacing={2}>
          <Grid
            size={{
              xs: 12,
              md: 6,
              lg: 4,
            }}
          >
            <BorderedCardClickable
              onClick={() => setSelectProviderOpen(true)}
              sx={{
                height: "100%",
                borderStyle: "dashed",
                display: "flex",
                alignItems: "center",
                gap: 1,
                justifyContent: "center",
                color: (t) => t.palette.text.secondary,
              }}
            >
              <Add />
              <Typography variant="h6">{t("policy.newStoragePolicy")}</Typography>
            </BorderedCardClickable>
          </Grid>
          {!loading && policies.map((p) => <StoragePolicyCard key={p.id} policy={p} onRefresh={fetchPolicies} />)}
          {loading &&
            policies.length > 0 &&
            policies.map((p) => <StoragePolicyCard key={`loading-${p.id}`} loading={true} />)}
          {loading &&
            policies.length === 0 &&
            Array.from(Array(5)).map((_, index) => (
              <StoragePolicyCard key={`loading-placeholder-${index}`} loading={true} />
            ))}
        </Grid>
        {count > 0 && (
          <Box sx={{ mt: 1 }}>
            <TablePagination
              page={pageInt}
              totalItems={count}
              rowsPerPage={pageSizeInt}
              rowsPerPageOptions={[11, 25, 50, 100, 200, 500, 1000]}
              onRowsPerPageChange={(value) => setPageSize(value.toString())}
              onChange={(_, value) => setPage(value.toString())}
            />
          </Box>
        )}
      </Container>
    </PageContainer>
  );
};

export default StoragePolicySetting;
