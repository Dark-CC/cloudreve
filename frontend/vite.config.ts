import react from "@vitejs/plugin-react-swc";
import { promises as fs } from "fs";
import { resolve } from "path";
import { defineConfig } from "vite";
// import mkcert from "vite-plugin-mkcert";
import { VitePWA } from "vite-plugin-pwa";
import { viteStaticCopy } from "vite-plugin-static-copy";

const backend = "http://localhost:5212";
const backendProxyOption = { target: backend, headers: { host: new URL(backend).host } };

// Version stamp used as the i18n localStorage cache key. package.json's version
// alone never changes between builds of the same release, so translation keys
// added after the first build stayed masked by the stale cached bundle until it
// expired (7 days). Stamping the build makes every deploy invalidate.
// NOTE: this must NOT go into version.json's "version" field: the backend
// compares that against constants.BackendVersion and logs a mismatch otherwise.
const BUILD_VERSION = `${process.env.npm_package_version}+${Date.now().toString(36)}`;

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: "prompt",
      injectRegister: "auto",
      manifest: false,
      workbox: {
        globIgnores: ["**/*leaflet*", "**/*mapbox*", "**/*Leaflet*", "**/*Mapbox*"],
        maximumFileSizeToCacheInBytes: 10000000,
        navigateFallbackDenylist: [/^\/pdfviewer.html/, /^\/api\/(.+)/, /^\/f\/(.+)/, /^\/s\/(.+)/],
      },
      devOptions: {
        enabled: true,
      },
    }),
    viteStaticCopy({
      targets: [
        {
          src: "node_modules/pdfjs-dist/build/*.mjs",
          dest: "assets/pdfjs",
        },
      ],
    }),
    {
      name: "load-stylesheet-async",
      transformIndexHtml(html) {
        return html.replace(
          /<link rel="stylesheet" crossorigin href="(.+?)">/g,
          `<link rel="stylesheet" crossorigin href="$1" media="print" onload="this.media='all'">`,
        );
      },
    },
    {
      // The backend's FrontendFileHandler substitutes these placeholders at
      // serve time; in dev nobody does, so {siteScript} etc. leak into the
      // DOM as literal text. Substitute static equivalents — serve only, the
      // production build must keep the raw placeholders.
      name: "dev-site-placeholders",
      apply: "serve",
      transformIndexHtml(html) {
        return html
          .replaceAll("{siteName}", "Cloudreve")
          .replaceAll("{siteDes}", "Cloudreve dev server")
          .replaceAll("{siteScript}", "")
          .replaceAll("{pwa_small_icon}", "/static/img/favicon.ico")
          .replaceAll("{pwa_medium_icon}", "/static/img/logo192.png")
          .replaceAll("var(--defaultThemeColor)", "#1976d2");
      },
    },
    {
      name: "generate-version",
      async writeBundle(outputOptions) {
        const version = {
          name: process.env.npm_package_name,
          // Must match application/constants.BackendVersion: statics.go compares
          // this field against it and logs a mismatch otherwise.
          //
          // Don't hardcode it: .build/build-assets.sh runs `yarn version
          // --new-version <tag>` before building, so package.json carries the tag
          // version (4.23.8, or 0.0.0-dev for a local build). Hardcoding it meant
          // every release whose tag did not match the literal shipped a stale
          // stamp and logged a mismatch. Outside that script package.json's
          // placeholder (4.0.0-next) is what you get, matching the dev backend.
          version: process.env.npm_package_version,
          build: BUILD_VERSION,
        };
        const path = resolve(__dirname, outputOptions.dir, "version.json");
        await fs.writeFile(path, JSON.stringify(version));
      },
    },
    // mkcert({
    //   hosts: ["devv5.cloudreve.org"],
    // }),
  ],
  define: {
    __ASSETS_VERSION__: JSON.stringify(BUILD_VERSION),
  },
  resolve: {
    // mui-one-time-password-input nests its own @emotion/react; without
    // dedupe two Emotion instances load and React warns on every render.
    dedupe: ["@emotion/react", "@emotion/styled", "@emotion/cache"],
  },
  build: {
    outDir: "build", // keep same as v3 with minimal changes
    rollupOptions: {
      output: {
        manualChunks: (id) => {
          const chunkMap = {
            common: [
              "vite/preload-helper",
              "vite/modulepreload-polyfill",
              "vite/dynamic-import-helper",
              "commonjsHelpers",
              "commonjs-dynamic-modules",
              "__vite-browser-external",
            ],
            monaco: ["monaco-editor"],
            codemirror: ["@codemirror"],
            excalidraw: [
              "node_modules/@excalidraw",
              "node_modules/browser-fs-access",
              "node_modules/image-blob-reduce",
              "node_modules/pica/",
            ],
            mermaid: ["node_modules/mermaid", "node_modules/katex"],
            leaflet: ["node_modules/leaflet", "node_modules/react-leaflet"],
            react: ["node_modules/react", "node_modules/react-dom"],
            mapbox: ["node_modules/mapbox-gl"],
          };

          // https://github.com/vitejs/vite/issues/5189#issuecomment-2175410148
          for (const [chunkName, patterns] of Object.entries(chunkMap)) {
            if (patterns.some((pattern) => id.includes(pattern))) {
              return chunkName;
            }
          }
        },
      },
    },
  },
  server: {
    host: "0.0.0.0",
    cors: false,
    proxy: {
      "/api": backendProxyOption,
      "/s/": backendProxyOption,
      "/f/": backendProxyOption,
      "/manifest.json": backendProxyOption,
    },
  },
});
