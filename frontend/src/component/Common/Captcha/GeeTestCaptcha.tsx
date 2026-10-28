import { Box, GlobalStyles } from "@mui/material";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAppSelector } from "../../../redux/hooks.ts";
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
    initGeetest4?: (config: Record<string, unknown>, callback: (captchaObj: GeeTestCaptchaObj) => void) => void;
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

// GeeTestCaptcha mounts the GeeTest v4 (行为验证 4.0) popup widget. The
// widget's own button (appended into the mount point by the SDK) both opens
// the challenge popup and shows its verified state, so no extra button or
// manual code input is rendered here. On success the widget yields
// { lot_number, captcha_output, pass_token, gen_time }, which the backend
// verifies via the /v4/validate.php secondary validation API.
const GeeTestCaptcha = ({ onStateChange, generation, fullWidth, ...rest }: GeeTestCaptchaProps) => {
  const { i18n } = useTranslation("common");
  const captchaId = useAppSelector((state) => state.siteConfig.basic.config.geetest_captcha_id);
  const [ready, setReady] = useState(false);
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
            onStateChangeRef.current({
              captcha_output: result.captcha_output,
              lot_number: result.lot_number,
              pass_token: result.pass_token,
              gen_time: result.gen_time,
            });
          }
        });
        captchaObj.onError(() => {
          // The widget surfaces its own error/retry UI; nothing to do here.
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
      captchaRef.current?.reset?.();
    }
  }, [generation]);

  if (!captchaId) {
    return null;
  }

  return (
    <Box sx={{ ...(fullWidth && { width: "100%" }) }} {...rest}>
      {/* Stretch the SDK-appended button so it lines up with the surrounding
          MUI outlined inputs: full width, same height and border radius. */}
      <GlobalStyles
        styles={(theme) => ({
          ".cloudreve-geetest-mount .geetest_holder": {
            width: "100% !important",
            height: "56px !important",
            borderRadius: `${theme.shape.borderRadius}px !important`,
          },
          ".cloudreve-geetest-mount .geetest_tip_container": {
            width: "calc(100% - 2px) !important",
            height: "100% !important",
          },
          ".cloudreve-geetest-mount .geetest_content": {
            width: "100% !important",
          },
        })}
      />
      <div className={"cloudreve-geetest-mount"} ref={containerRef} />
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
