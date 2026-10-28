import { Box } from "@mui/material";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAppSelector } from "../../../redux/hooks.ts";
import { SecondaryButton } from "../StyledComponents.tsx";
import { CaptchaParams } from "./Captcha.tsx";

const GEETEST_SCRIPT = "https://static.geetest.com/v4/gt4.js";
const GEETEST_SCRIPT_ID = "geetest-gt4-script";

export interface GeeTestCaptchaProps {
  onStateChange: (state: CaptchaParams) => void;
  generation: number;
  fullWidth?: boolean;
  [x: string]: unknown;
}

export interface GeeTestValidateResult {
  lot_number: string;
  captcha_output: string;
  pass_token: string;
  gen_time: string;
}

declare global {
  interface Window {
    initGeetest4?: (
      config: Record<string, unknown>,
      callback: (captchaObj: GeeTestCaptchaObj) => void,
    ) => void;
  }
}

interface GeeTestCaptchaObj {
  appendTo: (selector: string | HTMLElement) => void;
  showCaptcha: () => void;
  reset: () => void;
  destroy: () => void;
  onSuccess: (fn: () => void) => void;
  onError: (fn: (err: unknown) => void) => void;
  getValidate: () => GeeTestValidateResult;
}

// GeeTestCaptcha renders a verify button that opens the GeeTest v4
// (行为验证 4.0) popup widget. On success the widget yields
// { lot_number, captcha_output, pass_token, gen_time }, which the backend
// verifies via the /v4/validate.php secondary validation API.
const GeeTestCaptcha = ({ onStateChange, generation, fullWidth, ...rest }: GeeTestCaptchaProps) => {
  const { t, i18n } = useTranslation("common");
  const captchaId = useAppSelector((state) => state.siteConfig.basic.config.geetest_captcha_id);
  const [ready, setReady] = useState(false);
  const [verified, setVerified] = useState(false);
  const captchaRef = useRef<GeeTestCaptchaObj | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const onStateChangeRef = useRef(onStateChange);

  useEffect(() => {
    onStateChangeRef.current = onStateChange;
  }, [onStateChange]);

  // Load the gt4.js SDK once.
  useEffect(() => {
    if (!captchaId) {
      return;
    }

    let script = document.getElementById(GEETEST_SCRIPT_ID) as HTMLScriptElement | null;
    const onLoad = () => setReady(true);
    if (!script) {
      script = document.createElement("script");
      script.id = GEETEST_SCRIPT_ID;
      script.src = GEETEST_SCRIPT;
      script.async = true;
      script.onload = onLoad;
      document.head.appendChild(script);
    } else if (window.initGeetest4) {
      setReady(true);
    } else {
      script.onload = onLoad;
    }
  }, [captchaId]);

  // Init (or re-init) the widget once the SDK and a mount point exist.
  useEffect(() => {
    if (!captchaId || !ready || !containerRef.current || !window.initGeetest4) {
      return;
    }

    captchaRef.current?.destroy?.();
    captchaRef.current = null;
    setVerified(false);

    window.initGeetest4(
      {
        captchaId,
        product: "popup",
        language: mapGeeTestLanguage(i18n.language),
      },
      (captchaObj) => {
        captchaObj.onSuccess(() => {
          const result = captchaObj.getValidate();
          if (result?.captcha_output) {
            setVerified(true);
            onStateChangeRef.current({
              captcha_output: result.captcha_output,
              lot_number: result.lot_number,
              pass_token: result.pass_token,
              gen_time: result.gen_time,
            });
          }
        });
        captchaObj.onError(() => {
          setVerified(false);
        });
        captchaObj.appendTo(containerRef.current!);
        captchaRef.current = captchaObj;
      },
    );

    return () => {
      captchaRef.current?.destroy?.();
      captchaRef.current = null;
    };
  }, [captchaId, ready]);

  // Regeneration (e.g. a failed login attempt) invalidates a previous solve.
  useEffect(() => {
    if (generation > 0) {
      setVerified(false);
      captchaRef.current?.reset?.();
    }
  }, [generation]);

  const openCaptcha = () => {
    captchaRef.current?.showCaptcha();
  };

  if (!captchaId) {
    return null;
  }

  return (
    <Box sx={{ textAlign: "center", ...(fullWidth && { width: "100%" }) }} {...rest}>
      <div ref={containerRef} />
      <SecondaryButton
        variant="outlined"
        fullWidth={fullWidth}
        onClick={openCaptcha}
        disabled={!ready}
        color={verified ? "success" : "primary"}
      >
        {verified ? t("captcha.verified") : t("captcha.verify")}
      </SecondaryButton>
    </Box>
  );
};

// mapGeeTestLanguage converts an i18next code ("zh-CN", "en-US", ...) to the
// tags accepted by the GeeTest widget ("zh-cn", "en", ...). Unknown codes are
// passed through lowercased; GeeTest falls back to its default on mismatch.
const mapGeeTestLanguage = (lang: string | undefined) => {
  if (!lang) return "zh-cn";
  const normalized = lang.toLowerCase();
  if (normalized.startsWith("en")) return "en";
  return normalized;
};

export default GeeTestCaptcha;
