#!/usr/bin/env python3
"""Scan every t("...") / i18nKey usage under a source dir and check each key
resolves in every locale bundle."""
import json, os, re, sys

ROOT = "/root/.tds/workspaces/01a18e51-95a5-7004-8566-542b411f675f/frontend"
LOCALES = os.path.join(ROOT, "public/locales")
SRC = os.path.join(ROOT, "src")
NSS = ["common", "application", "dashboard"]

# t("key") / t('key') / i18nKey="key" / t("ns:key")
RE_T = re.compile(r"""(?:\bt\(|i18nKey=)\s*["']([A-Za-z0-9_.:'\- ]+)["']""")


RE_NS = re.compile(r'useTranslation\(\s*["\']([a-zA-Z0-9_]+)["\'](?:\s*,\s*\{[^}]*keyPrefix:\s*["\']([^"\']*)["\'])?')


def collect(path):
    """Return {key: set((namespace, keyPrefix) for the file using it)}."""
    keys = {}
    for dirpath, _, files in os.walk(path):
        for f in files:
            if not f.endswith((".tsx", ".ts")):
                continue
            text = open(os.path.join(dirpath, f), encoding="utf-8", errors="ignore").read()
            # (namespace, keyPrefix) pairs this file declares
            file_ctx = [(m[0], m[1] or "") for m in RE_NS.findall(text)]
            for m in RE_T.findall(text):
                k = m.strip()
                # skip obvious non-i18n (date formats, urls, css)
                if any(c in k for c in " /#%{}$@,"):
                    if ":" not in k or k.count(":") > 1:
                        continue
                if k.startswith("http") or k.startswith("."):
                    continue
                # Dynamic composition (e.g. t("settings." + name), or a bare
                # namespace prefix) never resolves as a full key: skip it.
                if k.endswith(".") or k.rstrip(":") in NSS or k in NSS:
                    continue
                keys.setdefault(k, set()).update(file_ctx or [(None, "")])
    return keys


def load(lng):
    out = {}
    for ns in NSS:
        p = os.path.join(LOCALES, lng, ns + ".json")
        if os.path.exists(p):
            out[ns] = json.load(open(p, encoding="utf-8"))
        else:
            out[ns] = {}
    return out


def resolve(bundles, key, default_ns):
    ns, _, path = key.partition(":") if ":" in key else (default_ns, "", key)
    if not path:
        path = key
    node = bundles.get(ns, {})
    for part in path.split("."):
        if not isinstance(node, dict) or part not in node:
            return None
        node = node[part]
    # returnObjects keys (reason lists, warning lists) are arrays, not strings:
    # both are legitimate translations.
    if isinstance(node, (str, list)):
        return node
    return None


def resolves_anywhere(bundles, key, ctx):
    """A key is present if it resolves under any (namespace, keyPrefix) pair the
    file declares, or under the i18next defaultNS fallback chain."""
    cands = []
    if ":" in key:
        ns, _, rest = key.partition(":")
        cands = [(ns, "")]
        key = rest
    else:
        cands = list(ctx)
    cands += [(ns, "") for ns in ("application", "common", "dashboard")]
    for ns, prefix in dict.fromkeys(cands):
        if not ns:
            continue
        full = f"{prefix}.{key}" if prefix else key
        if resolve(bundles, full, ns) is not None:
            return True
    return False


def main():
    target = sys.argv[1] if len(sys.argv) > 1 else SRC
    keys = collect(target)
    langs = sorted(os.listdir(LOCALES))
    langs = [l for l in langs if os.path.isdir(os.path.join(LOCALES, l))]
    print(f"locales: {len(langs)} ({', '.join(langs)})")
    print(f"scanned dir: {target}")
    print(f"distinct keys found in source: {len(keys)}\n")

    bundles = {l: load(l) for l in langs}
    missing = {}
    for k, nss in sorted(keys.items()):
        gaps = [l for l in langs if not resolves_anywhere(bundles[l], k, nss)]
        if gaps:
            missing[k] = gaps

    if not missing:
        print("NO MISSING KEYS — every t() key resolves in every locale.")
    else:
        print(f"MISSING: {len(missing)} key(s)\n")
        for k, gaps in missing.items():
            print(f"  {k}")
            print(f"    missing in {len(gaps)}/{len(langs)}: {', '.join(gaps)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
