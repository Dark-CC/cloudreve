import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import Backend from "i18next-http-backend";
import LanguageDetector from "i18next-browser-languagedetector";

// The desktop client is a Simplified-Chinese-first product: it starts in
// zh-CN no matter what the system locale is. Only an explicit `?lng=` on the
// webview URL (the native shell always appends the effective locale, and the
// in-app language selector reloads the window with it) or a runtime
// i18n.changeLanguage() call from that selector overrides the default.
// See docs/ops/desktop-release-0.3.13.md.
export const DEFAULT_LANGUAGE = "zh-CN";

i18n
  .use(Backend)
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    fallbackLng: DEFAULT_LANGUAGE,
    debug: import.meta.env.DEV,
    ns: ["common"],
    load: "currentOnly",
    defaultNS: "common",
    interpolation: {
      escapeValue: false,
    },
    backend: {
      loadPath: "/locales/{{lng}}/{{ns}}.json",
    },
    supportedLngs: ['en-US', 'zh-CN', 'zh-TW', 'ja', 'de', 'fr', 'es', 'ko', 'ru', 'pl', 'it'],
    detection: {
      // Only the URL query param is honoured. `navigator` and `htmlTag` are
      // intentionally left out so the system language never picks the UI
      // language; without a `lng` value i18next falls back to zh-CN above.
      order: ['querystring'],
      // Look for 'lng' in query string
      lookupQuerystring: 'lng',
      // Cache the detected language
      caches: [],
    },
  });

i18n.on("languageChanged", (lng) => {
  document.documentElement.setAttribute("lang", lng);
});

export const languages = [
  {
    code: "en-US",
    displayName: "English",
  },
  {
    code: "zh-CN",
    displayName: "简体中文",
  },
  {
    code: "zh-TW",
    displayName: "繁體中文",
  },
  {
    code: "ja",
    displayName: "日本語",
  },
  {
    code: "de",
    displayName: "Deutsch",
  },
  {
    code: "fr",
    displayName: "Français",
  },
  {
    code: "es",
    displayName: "Español",
  },
  {
    code: "ko",
    displayName: "한국어",
  },
  {
    code: "ru",
    displayName: "Русский",
  },
  {
    code: "pl",
    displayName: "Polski",
  },
  {
    code: "it",
    displayName: "Italiano",
  },
];

export default i18n;
