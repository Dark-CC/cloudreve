import { Link } from "@mui/material";
import { Trans } from "react-i18next";
import { PolicyType } from "../../../api/explorer";
import { Code } from "../../Common/Code.tsx";
import type { AddWizardProps } from "./AddWizardDialog";
import CosWizard from "./Wizards/COS/CosWizard";
import KS3Wizard from "./Wizards/KS3/KS3Wizard";
import LBWizard from "./Wizards/LB/LBWizard";
import LocalWizard from "./Wizards/Local/LocalWizard";
import ObsWizard from "./Wizards/OBS/ObsWizard";
import OneDriveWizard from "./Wizards/OneDrive/OneDriveWizard";
import OssWizard from "./Wizards/OSS/OssWizard";
import QiniuWizard from "./Wizards/Qiniu/QiniuWizard";
import RemoteWizard from "./Wizards/Remote/RemoteWizard";
import S3Wizard from "./Wizards/S3/S3Wizard";
import UpyunWizard from "./Wizards/Upyun/UpyunWizard";

export interface PolicyProps {
  name: string;
  img: string;
  wizardSize?: "sm" | "md" | "lg";
  wizard?: (props: AddWizardProps) => React.ReactNode;
  chunkSizeDes?: string;
  chunkSizeMin?: number;
  chunkSizeMax?: number;
  nativeThumbDes?: string;
  nativeThumbDoc?: string;
  nativeExtractorName?: string;
  nativeExtractorDoc?: string;
  nativeExtractorDes?: string;
  nativeExtractorDesDoc?: string;
  bucketName?: string;
  bucketNameDes?: React.ReactNode;
  bucketType?: string;
  endpointName?: string;
  endpointDes?: React.ReactNode;
  akName?: string;
  skName?: string;
  credentialDes?: React.ReactNode;
  corsExposedHeaders?: string[];
  endpointNotEnforcePrefix?: boolean;
  regionCode?: string;
  regionCodeDes?: React.ReactNode;
}

export const PolicyPropsMap: Record<PolicyType, PolicyProps> = {
  [PolicyType.local]: {
    name: "policy.local",
    img: "/static/img/local.png",
    wizardSize: "sm",
    wizard: LocalWizard,
    chunkSizeDes: "policy.chunkSizeDes",
  },
  [PolicyType.load_balance]: {
    name: "policy.load_balance",
    img: "/static/img/lb.svg",
    wizardSize: "sm",
    wizard: LBWizard,
  },
  [PolicyType.remote]: {
    name: "policy.remote",
    img: "/static/img/remote.png",
    wizardSize: "sm",
    wizard: RemoteWizard,
    nativeThumbDes: "policy.nativeThumbNailsGeneralRemote",
    nativeExtractorName: "policy.mediaExtractorNative",
    nativeExtractorDes: "policy.nativeMediaMetaExtsRemote",
    chunkSizeDes: "policy.chunkSizeDes",
  },
  [PolicyType.s3]: {
    name: "policy.s3",
    img: "/static/img/s3.png",
    wizardSize: "sm",
    wizard: S3Wizard,
    bucketName: "policy.bucketName",
    bucketType: "policy.bucketType",
    endpointName: "policy.policyEndpoint",
    endpointDes: <Trans i18nKey="policy.s3EndpointDes" ns="dashboard" components={[<Code />]} />,
    akName: "Access Key",
    skName: "Secret Key",
    chunkSizeMin: 5 * 1024 * 1024, //5MB
    chunkSizeMax: 5 * 1024 * 1024 * 1024, //5GB
    chunkSizeDes: "policy.chunkSizeDesS3",
    regionCode: "policy.s3Region",
    regionCodeDes: <Trans i18nKey={"policy.selectRegionDes"} ns="dashboard" components={[<Code />]} />,
  },
  [PolicyType.ks3]: {
    name: "policy.ks3",
    img: "/static/img/ks3.png",
    wizardSize: "sm",
    wizard: KS3Wizard,
    bucketName: "policy.bucketName",
    bucketType: "policy.bucketType",
    endpointName: "policy.policyEndpoint",
    endpointDes: <Trans i18nKey="policy.ks3selectRegionDes" ns="dashboard" components={[<Code />]} />,
    akName: "Access Key",
    skName: "Secret Key",
    corsExposedHeaders: ["ETag"],
    chunkSizeMin: 5 * 1024 * 1024, //5MB
    chunkSizeMax: 5 * 1024 * 1024 * 1024, //5GB
    chunkSizeDes: "policy.chunkSizeDesS3",
    regionCode: "policy.s3Region",
    regionCodeDes: <Trans i18nKey={"policy.ks3selectRegionDes"} ns="dashboard" components={[<Code />]} />,
  },
  [PolicyType.cos]: {
    name: "policy.cos",
    img: "/static/img/cos.png",
    wizardSize: "sm",
    wizard: CosWizard,
    bucketName: "policy.cosObsBucketName",
    bucketNameDes: (
      <Trans
        i18nKey="policy.createCOSBucketDes"
        ns="dashboard"
        components={[<Link href="https://console.cloud.tencent.com/cos/bucket" target="_blank" />, <Code />]}
      />
    ),
    bucketType: "policy.accessType",
    endpointName: "policy.accessDomain",
    endpointDes: <Trans i18nKey="policy.cosAccessDomainDes" ns="dashboard" components={[<Code />, <Code />]} />,
    chunkSizeMin: 1024 * 1024, //1MB
    chunkSizeMax: 5 * 1024 * 1024 * 1024, //5GB
    chunkSizeDes: "policy.chunkSizeDesQiniuCos",
    nativeThumbDes: "policy.nativeThumbNailsGeneralCos",
    nativeThumbDoc: "https://cloud.tencent.com/document/product/436/113312",
    nativeExtractorName: "policy.mediaExtractorCos",
    nativeExtractorDes: "policy.nativeMediaMetaExtCos",
    nativeExtractorDesDoc: "https://console.cloud.tencent.com/ci",
    nativeExtractorDoc: "https://console.cloud.tencent.com/ci",
    akName: "SecretId",
    skName: "SecretKey",
    corsExposedHeaders: ["ETag"],
    credentialDes: (
      <Trans
        i18nKey="policy.cosCredentialDes"
        ns="dashboard"
        components={[
          <Link href="https://console.cloud.tencent.com/cam/capi" target="_blank" />,
          <Code />,
          <Link href="https://console.cloud.tencent.com/cam" target="_blank" />,
        ]}
      />
    ),
  },
  [PolicyType.oss]: {
    name: "policy.oss",
    img: "/static/img/oss.png",
    wizardSize: "sm",
    wizard: OssWizard,
    chunkSizeMin: 100 * 1024, //100KB
    chunkSizeMax: 5 * 1024 * 1024 * 1024, //5GB
    chunkSizeDes: "policy.chunkSizeDesOssObs",
    nativeThumbDes: "policy.nativeThumbNailsGeneralOss",
    nativeThumbDoc: "https://help.aliyun.com/zh/oss/user-guide/overview-17/",
    nativeExtractorName: "policy.mediaExtractorOss",
    nativeExtractorDes: "policy.nativeMediaMetaExtOss",
    nativeExtractorDoc: "https://help.aliyun.com/zh/oss/user-guide/quick-start-2326698",
    nativeExtractorDesDoc: "https://help.aliyun.com/zh/oss/user-guide/quick-start-2326698",
    bucketName: "policy.bucketName",
    bucketNameDes: (
      <Trans
        i18nKey="policy.createOSSBucketDes"
        ns="dashboard"
        components={[<Link href="https://oss.console.aliyun.com/bucket" target="_blank" />, <Code />, <Code />]}
      />
    ),
    bucketType: "policy.bucketType",
    akName: "AccessKey ID",
    skName: "AccessKey Secret",
    credentialDes: (
      <Trans
        i18nKey="policy.ossAKDes"
        ns="dashboard"
        components={[
          <Link href="https://ram.console.aliyun.com/profile/access-keys" target="_blank" />,
          <Link href="https://ram.console.aliyun.com/ram/overview" target="_blank" />,
          <Code />,
        ]}
      />
    ),
    regionCode: "policy.s3Region",
    regionCodeDes: (
      <Trans
        i18nKey={"policy.ossRegionDes"}
        ns="dashboard"
        components={[
          <Code />,
          <Link href="https://www.alibabacloud.com/help/oss/regions-and-endpoints" target="_blank" />,
          <Code />,
        ]}
      />
    ),
  },
  [PolicyType.obs]: {
    name: "policy.obs",
    img: "/static/img/obs.png",
    wizardSize: "sm",
    wizard: ObsWizard,
    bucketName: "policy.cosObsBucketName",
    bucketNameDes: (
      <Trans
        i18nKey="policy.obsBucketDes"
        ns="dashboard"
        components={[
          <Link href="https://console.huaweicloud.com/console/#/obs/create" target="_blank" />,
          <Code />,
          <Code />,
          <Code />,
        ]}
      />
    ),
    bucketType: "policy.bucketPolicy",
    endpointName: "policy.policyEndpoint",
    endpointDes: <Trans i18nKey="policy.obsEndpointDes" ns="dashboard" components={[<Code />, <Code />]} />,
    endpointNotEnforcePrefix: true,
    akName: "Access Key Id",
    skName: "Secret Access Key",
    credentialDes: (
      <Trans
        i18nKey="policy.obsCredentialDes"
        ns="dashboard"
        components={[
          <Link href="https://console.huaweicloud.com/iam/#/mine/accessKey" target="_blank" />,
          <Code />,
          <Code />,
          <Code />,
        ]}
      />
    ),
    corsExposedHeaders: ["ETag"],
    chunkSizeMin: 100 * 1024, //100KB
    chunkSizeMax: 5 * 1024 * 1024 * 1024, //5GB
    chunkSizeDes: "policy.chunkSizeDesOssObs",
    nativeThumbDes: "policy.nativeThumbNailsGeneralObs",
    nativeThumbDoc: "https://support.huaweicloud.com/intl/zh-cn/usermanual-obs/obs_01_0001.html",
    nativeExtractorName: "policy.mediaExtractorObs",
    nativeExtractorDes: "policy.nativeMediaMetaExtObs",
    nativeExtractorDoc: "https://support.huaweicloud.com/intl/zh-cn/usermanual-obs/obs_01_0410.html",
    nativeExtractorDesDoc: "https://support.huaweicloud.com/intl/zh-cn/usermanual-obs/obs_01_0410.html",
  },
  [PolicyType.qiniu]: {
    name: "policy.qiniu",
    img: "/static/img/qiniu.png",
    wizardSize: "sm",
    wizard: QiniuWizard,
    bucketName: "policy.qiniuBucketName",
    bucketNameDes: (
      <Trans
        i18nKey="policy.enterQiniuBucket"
        ns="dashboard"
        components={[<Link href="https://portal.qiniu.com/kodo/bucket" target="_blank" />]}
      />
    ),
    bucketType: "policy.aclType",
    akName: "AK",
    skName: "SK",
    credentialDes: <Trans i18nKey="policy.qiniuCredentialDes" ns="dashboard" />,
    nativeThumbDes: "policy.nativeThumbNailsGeneralQiniu",
    nativeThumbDoc: "https://developer.qiniu.com/dora/api/basic-processing-images-imageview2",
    nativeExtractorName: "policy.mediaExtractorQiniu",
    nativeExtractorDes: "policy.nativeMediaMetaExtQiniu",
    nativeExtractorDoc: "https://www.qiniu.com/products/dora",
    chunkSizeMin: 1024 * 1024, //1 MB
    chunkSizeMax: 1024 * 1024 * 1024, //1GB
    chunkSizeDes: "policy.chunkSizeDesQiniuCos",
  },
  [PolicyType.upyun]: {
    name: "policy.upyun",
    img: "/static/img/upyun.png",
    wizardSize: "sm",
    wizard: UpyunWizard,
    bucketName: "policy.storageServiceName",
    bucketNameDes: (
      <Trans
        i18nKey="policy.createUpyunBucketDes"
        ns="dashboard"
        components={[<Link href="https://console.upyun.com/console/service" target="_blank" />]}
      />
    ),
    bucketType: "policy.tokenStatus",
    akName: "policy.operatorName",
    skName: "policy.operatorPassword",
    nativeThumbDes: "policy.nativeThumbNailsGeneralUpyun",
    nativeThumbDoc: "https://help.upyun.com/knowledge-base/image/",
    nativeExtractorName: "policy.mediaExtractorUpyun",
    nativeExtractorDes: "policy.nativeMediaMetaExtUpyun",
    nativeExtractorDesDoc: "https://help.upyun.com/knowledge-base/image/#e58583e695b0e68daee88eb7e58f96",
    nativeExtractorDoc: "https://help.upyun.com/knowledge-base/image/",
  },
  [PolicyType.onedrive]: {
    name: "policy.onedrive",
    img: "/static/img/onedrive.png",
    wizardSize: "sm",
    wizard: OneDriveWizard,
    chunkSizeMin: 5 * 1024 * 1024, //5MB
    chunkSizeMax: 5 * 1024 * 1024 * 1024, //5GB
    chunkSizeDes: "policy.chunkSizeDesOd",
    bucketName: "policy.storageServiceName",
    bucketNameDes: (
      <Trans
        i18nKey="policy.createUpyunBucketDes"
        ns="dashboard"
        components={[<Link href="https://console.upyun.com/console/service" target="_blank" />]}
      />
    ),
    bucketType: "policy.tokenStatus",
  },
};

// unknownPolicyProps is used when a policy carries a type this build has no
// renderer for: e.g. a policy created by a newer Cloudreve build (or a driver
// that was removed), left behind in storage_policies. Without a fallback every
// `PolicyPropsMap[policy.type].img` would throw and take the whole admin page
// down through the router error boundary, so such policies are still listed,
// just with a generic label and icon, and their edit form shows only the
// type-agnostic sections.
export const unknownPolicyProps: PolicyProps = {
  name: "policy.unknownPolicyType",
  img: "/static/img/local.png",
};

export const getPolicyProps = (type: PolicyType | string | undefined, fallback: PolicyProps = unknownPolicyProps) =>
  (type !== undefined && PolicyPropsMap[type as PolicyType]) || fallback;
