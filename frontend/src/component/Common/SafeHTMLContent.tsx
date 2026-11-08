import { Box } from "@mui/material";
import { useMemo } from "react";

export interface SafeHTMLContentProps {
  html: string;
  sx?: Record<string, unknown>;
}

// Tags that may survive the client-side pass. This mirrors the server-side
// allow-list in pkg/htmlsafe: the server is the authoritative filter, and this
// second pass exists so a compromised or downgraded server response still
// cannot inject script or UI-redressing markup into the page.
const ALLOWED_TAGS = new Set([
  "a",
  "abbr",
  "b",
  "blockquote",
  "br",
  "caption",
  "center",
  "cite",
  "code",
  "col",
  "colgroup",
  "dd",
  "del",
  "details",
  "dfn",
  "div",
  "dl",
  "dt",
  "em",
  "figcaption",
  "figure",
  "h1",
  "h2",
  "h3",
  "h4",
  "h5",
  "h6",
  "hr",
  "i",
  "img",
  "ins",
  "kbd",
  "li",
  "mark",
  "ol",
  "p",
  "pre",
  "q",
  "rp",
  "rt",
  "ruby",
  "s",
  "samp",
  "small",
  "span",
  "strike",
  "strong",
  "sub",
  "summary",
  "sup",
  "table",
  "tbody",
  "td",
  "tfoot",
  "th",
  "thead",
  "tr",
  "tt",
  "u",
  "ul",
  "var",
  "wbr",
]);

const ALLOWED_ATTRS = new Set([
  "class",
  "id",
  "title",
  "dir",
  "lang",
  "style",
  "href",
  "target",
  "alt",
  "src",
  "width",
  "height",
  "loading",
  "colspan",
  "rowspan",
  "align",
  "valign",
  "scope",
  "span",
  "start",
  "type",
  "value",
  "reversed",
  "open",
  "border",
  "cellpadding",
  "cellspacing",
  "size",
  "noshade",
  "name",
  "rel",
]);

// Attributes whose entire subtree is unusable and must be removed with content.
// `style` is handled before this list is consulted: its text is filtered by
// sanitizeCSS and re-emitted as a scoped stylesheet, never as-is.
const DROP_WITH_CONTENT = new Set([
  "script",
  "style",
  "noscript",
  "template",
  "iframe",
  "object",
  "embed",
  "applet",
  "param",
  "svg",
  "math",
  "canvas",
  "audio",
  "video",
  "source",
  "track",
  "form",
  "input",
  "button",
  "select",
  "option",
  "textarea",
  "label",
  "fieldset",
  "legend",
  "meta",
  "base",
  "link",
  "title",
  "head",
  "frame",
  "frameset",
  "noframes",
  "portal",
]);

const UNSAFE_SCHEMES = ["javascript:", "vbscript:", "data:", "blob:", "filesystem:"];

// SCOPE_ATTR is the attribute the announcement container carries. It must match
// htmlsafe.ScopeAttribute on the server: every selector is rewritten against it
// so an announcement cannot restyle the site around it.
const SCOPE_ATTR = "data-cr-announcement";
const SCOPE_PREFIX = `[${SCOPE_ATTR}]`;

// ALLOWED_CSS_PROPS mirrors allowedCSSProperties in pkg/htmlsafe/css.go.
const ALLOWED_CSS_PROPS = new Set([
  "color",
  "opacity",
  "visibility",
  "font",
  "font-family",
  "font-size",
  "font-weight",
  "font-style",
  "font-variant",
  "font-stretch",
  "line-height",
  "letter-spacing",
  "word-spacing",
  "text-align",
  "text-indent",
  "text-transform",
  "text-shadow",
  "text-decoration",
  "text-decoration-line",
  "text-decoration-color",
  "text-decoration-style",
  "text-decoration-thickness",
  "text-underline-offset",
  "white-space",
  "word-break",
  "overflow-wrap",
  "word-wrap",
  "vertical-align",
  "table-layout",
  "float",
  "clear",
  "background",
  "background-color",
  "background-image",
  "background-position",
  "background-size",
  "background-repeat",
  "background-clip",
  "background-origin",
  "background-attachment",
  "width",
  "height",
  "min-width",
  "min-height",
  "max-width",
  "max-height",
  "aspect-ratio",
  "box-sizing",
  "box-shadow",
  "margin",
  "margin-top",
  "margin-right",
  "margin-bottom",
  "margin-left",
  "padding",
  "padding-top",
  "padding-right",
  "padding-bottom",
  "padding-left",
  "border",
  "border-width",
  "border-style",
  "border-color",
  "border-radius",
  "border-collapse",
  "border-spacing",
  "border-top",
  "border-right",
  "border-bottom",
  "border-left",
  "border-top-width",
  "border-top-style",
  "border-top-color",
  "border-right-width",
  "border-right-style",
  "border-right-color",
  "border-bottom-width",
  "border-bottom-style",
  "border-bottom-color",
  "border-left-width",
  "border-left-style",
  "border-left-color",
  "border-top-left-radius",
  "border-top-right-radius",
  "border-bottom-left-radius",
  "border-bottom-right-radius",
  "outline",
  "outline-color",
  "outline-style",
  "outline-width",
  "outline-offset",
  "display",
  "order",
  "flex",
  "flex-grow",
  "flex-shrink",
  "flex-basis",
  "flex-direction",
  "flex-wrap",
  "flex-flow",
  "gap",
  "row-gap",
  "column-gap",
  "align-items",
  "align-content",
  "align-self",
  "justify-content",
  "justify-items",
  "justify-self",
  "place-items",
  "place-content",
  "place-self",
  "grid",
  "grid-gap",
  "grid-column-gap",
  "grid-row-gap",
  "grid-template",
  "grid-template-columns",
  "grid-template-rows",
  "grid-template-areas",
  "grid-auto-flow",
  "grid-auto-columns",
  "grid-auto-rows",
  "grid-column",
  "grid-row",
  "grid-area",
  "columns",
  "column-count",
  "overflow",
  "overflow-x",
  "overflow-y",
  "object-fit",
  "object-position",
  "list-style",
  "list-style-type",
  "list-style-position",
]);

// BANNED_CSS_VALUE mirrors bannedCSSValueTokens: remote loading, script
// execution, parser tricks.
const BANNED_CSS_VALUE = [
  "url(",
  "image-set(",
  "src(",
  "expression(",
  "behavior",
  "-moz-binding",
  "javascript:",
  "vbscript:",
  "data:",
  "@",
  "\\",
  "/*",
  "</",
];

// Viewport-relative units: inside a modal they are what turn a harmless height
// into a full-screen cover.
const BANNED_UNIT_RE = /(^|[^\w-])(vh|vw|vmin|vmax|lvh|lvw|svh|svw|dvh|dvw)(?![\w-])/;

const MAX_CSS_RULES = 500;

const isSafeURL = (raw: string): boolean => {
  const v = raw
    .replace(/[\t\n\r\0]/g, "")
    .trim()
    .toLowerCase();
  return !UNSAFE_SCHEMES.some((s) => v.startsWith(s));
};

const isAllowedCSSProp = (prop: string): boolean => {
  const p = prop.toLowerCase();
  return p.startsWith("--") || ALLOWED_CSS_PROPS.has(p);
};

const isSafeCSSValue = (value: string): boolean => {
  const v = value.toLowerCase();
  if (BANNED_CSS_VALUE.some((token) => v.includes(token))) {
    return false;
  }
  if (BANNED_UNIT_RE.test(v)) {
    return false;
  }
  return !v.includes(";");
};

// splitTopLevel splits on a separator that is not inside a string, a
// parenthesised group or a bracket group.
const splitTopLevel = (input: string, sep: string): string[] => {
  const parts: string[] = [];
  let paren = 0;
  let bracket = 0;
  let quote = "";
  let start = 0;

  for (let i = 0; i < input.length; i += 1) {
    const c = input[i];
    if (quote) {
      if (c === "\\") {
        i += 1;
      } else if (c === quote) {
        quote = "";
      }
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      continue;
    }
    if (c === "(") {
      paren += 1;
    } else if (c === ")") {
      paren = Math.max(0, paren - 1);
    } else if (c === "[") {
      bracket += 1;
    } else if (c === "]") {
      bracket = Math.max(0, bracket - 1);
    } else if (paren === 0 && bracket === 0 && c === sep) {
      parts.push(input.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(input.slice(start));
  return parts;
};

// splitFirstCompound splits a complex selector after its first compound
// selector, returning that selector, the following combinator and the rest.
const splitFirstCompound = (sel: string): { first: string; combinator: string; rest: string } => {
  let paren = 0;
  let bracket = 0;
  let quote = "";

  for (let i = 0; i < sel.length; i += 1) {
    const c = sel[i];
    if (quote) {
      if (c === "\\") {
        i += 1;
      } else if (c === quote) {
        quote = "";
      }
      continue;
    }
    if (c === "'" || c === '"') {
      quote = c;
      continue;
    }
    if (c === "(") {
      paren += 1;
      continue;
    }
    if (c === ")") {
      paren = Math.max(0, paren - 1);
      continue;
    }
    if (c === "[") {
      bracket += 1;
      continue;
    }
    if (c === "]") {
      bracket = Math.max(0, bracket - 1);
      continue;
    }
    if (paren > 0 || bracket > 0) {
      continue;
    }
    if (c === ">" || c === "+" || c === "~") {
      return {
        first: sel.slice(0, i).trim(),
        combinator: c,
        rest: sel.slice(i + 1).trim(),
      };
    }
    if (/\s/.test(c)) {
      return { first: sel.slice(0, i).trim(), combinator: " ", rest: sel.slice(i).trim() };
    }
  }
  return { first: sel.trim(), combinator: "", rest: "" };
};

const hasDocumentPrefix = (lower: string, name: string): boolean => {
  if (!lower.startsWith(name) || lower.length === name.length) {
    return false;
  }
  const next = lower[name.length];
  return next === "." || next === "#" || next === "[" || next === ":";
};

// scopeSelector mirrors scopeSelector in pkg/htmlsafe/css.go: document-level
// selectors map onto the container, selectors that depend on page state are
// dropped, everything else is narrowed to the container's subtree.
const scopeSelector = (raw: string): string => {
  const sel = raw.trim();
  if (!sel) {
    return "";
  }
  // The server already scoped the stored value; keep it as-is.
  if (sel.startsWith(SCOPE_PREFIX)) {
    return sel;
  }

  const { first, combinator, rest } = splitFirstCompound(sel);
  if (!first) {
    return "";
  }

  const lower = first.toLowerCase();
  let base: string;
  if (lower === "html" || lower === "body" || lower === ":root") {
    base = SCOPE_PREFIX;
  } else if (
    hasDocumentPrefix(lower, "html") ||
    hasDocumentPrefix(lower, "body") ||
    hasDocumentPrefix(lower, ":root")
  ) {
    // `body.dark …`: the container cannot express page-level state.
    return "";
  } else {
    base = `${SCOPE_PREFIX} ${first}`;
  }

  if (!rest) {
    return base;
  }
  return combinator === " " ? `${base} ${rest}` : `${base} ${combinator} ${rest}`;
};

const scopeSelectorList = (selectorText: string): string =>
  splitTopLevel(selectorText, ",").map(scopeSelector).filter(Boolean).join(",");

// declarationsText filters a declaration block down to allow-listed properties
// with harmless values.
const declarationsText = (style: CSSStyleDeclaration): string => {
  const out: string[] = [];
  for (let i = 0; i < style.length; i += 1) {
    const prop = style.item(i);
    const value = style.getPropertyValue(prop).trim();
    if (!value || !isAllowedCSSProp(prop) || !isSafeCSSValue(value)) {
      continue;
    }
    const priority = style.getPropertyPriority(prop);
    out.push(priority ? `${prop}:${value} !${priority}` : `${prop}:${value}`);
  }
  return out.join(";");
};

type ScopedGroup = { name: "media" | "supports"; condition: string; rules: CSSRuleList };

// groupAtRule recognises the two conditional group rules worth keeping. Every
// other at-rule (@import, @font-face, @keyframes, @page, @layer …) is dropped.
const groupAtRule = (rule: CSSRule): ScopedGroup | null => {
  if (typeof CSSMediaRule !== "undefined" && rule instanceof CSSMediaRule) {
    const media = rule as CSSMediaRule & { conditionText?: string };
    const condition = (media.conditionText ?? media.media?.mediaText ?? "").trim();
    return { name: "media", condition, rules: rule.cssRules };
  }
  if (typeof CSSSupportsRule !== "undefined" && rule instanceof CSSSupportsRule) {
    const condition = (rule.conditionText ?? "").trim();
    return { name: "supports", condition, rules: rule.cssRules };
  }
  return null;
};

// conditionIsSafe keeps a media/supports prelude that cannot smuggle a
// declaration or a remote load into the output.
const conditionIsSafe = (condition: string): boolean => {
  if (!condition || condition.includes(";")) {
    return false;
  }
  const lower = condition.toLowerCase();
  return !BANNED_CSS_VALUE.some((token) => lower.includes(token));
};

// walkRules rebuilds the stylesheet as text, keeping only scoped style rules and
// scoped @media / @supports groups.
const walkRules = (rules: CSSRuleList, budget: { left: number }): string => {
  let out = "";
  for (let i = 0; i < rules.length; i += 1) {
    if (budget.left <= 0) {
      break;
    }
    const rule = rules[i];
    if (typeof CSSStyleRule !== "undefined" && rule instanceof CSSStyleRule) {
      const selectors = scopeSelectorList(rule.selectorText);
      const decls = declarationsText(rule.style);
      if (!selectors || !decls) {
        continue;
      }
      budget.left -= 1;
      out += `${selectors}{${decls}}`;
      continue;
    }
    const group = groupAtRule(rule);
    if (!group || !conditionIsSafe(group.condition)) {
      continue;
    }
    const inner = walkRules(group.rules, budget);
    if (!inner) {
      continue;
    }
    out += `@${group.name} ${group.condition}{${inner}}`;
  }
  return out;
};

// sanitizeCSS scopes and filters one stylesheet's text. The browser's own CSS
// parser does the tokenising, so the result is exactly what the engine meant by
// the input; a parse failure drops the block rather than guessing.
const sanitizeCSS = (css: string): string => {
  if (!css.trim()) {
    return "";
  }
  // @import is rejected outright by replaceSync and is never needed here.
  const stripped = css.replace(/@import\b[^;]*;?/gi, "");
  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(stripped);
    return walkRules(sheet.cssRules, { left: MAX_CSS_RULES });
  } catch {
    return "";
  }
};

// sanitizeHTML filters a parsed fragment in place and returns serialised HTML.
const sanitizeHTML = (input: string): string => {
  if (!input) {
    return "";
  }
  const template = document.createElement("template");
  template.innerHTML = input;

  const walk = (parent: Node) => {
    Array.from(parent.childNodes).forEach((node) => {
      if (node.nodeType === Node.COMMENT_NODE) {
        node.remove();
        return;
      }
      if (node.nodeType !== Node.ELEMENT_NODE) {
        return;
      }

      const el = node as Element;
      const tag = el.tagName.toLowerCase();

      if (tag === "style") {
        // The server emits one scoped <style> ahead of the content; re-running
        // the same transformation keeps it idempotent, and an unexpected style
        // block from anywhere else is scoped just the same.
        const scoped = sanitizeCSS(el.textContent ?? "");
        if (scoped) {
          el.textContent = scoped;
        } else {
          el.remove();
        }
        return;
      }

      if (!ALLOWED_TAGS.has(tag)) {
        if (DROP_WITH_CONTENT.has(tag)) {
          el.remove();
          return;
        }
        // Unknown-but-harmless wrapper: keep the text, drop the tag.
        const frag = document.createDocumentFragment();
        while (el.firstChild) {
          frag.appendChild(el.firstChild);
        }
        el.replaceWith(frag);
        walk(parent);
        return;
      }

      Array.from(el.attributes).forEach((attr) => {
        const name = attr.name.toLowerCase();
        const isEvent = name.startsWith("on");
        const isScriptedAttr = name === "srcdoc" || name === "formaction";

        if (name === "style") {
          // Inline styles get the same property/value filter as the stylesheet.
          const el2 = el as HTMLElement;
          for (let i = el2.style.length - 1; i >= 0; i -= 1) {
            const prop = el2.style.item(i);
            const value = el2.style.getPropertyValue(prop);
            if (!isAllowedCSSProp(prop) || !isSafeCSSValue(value)) {
              el2.style.removeProperty(prop);
            }
          }
          if (!el2.getAttribute("style")) {
            el.removeAttribute("style");
          }
          return;
        }

        const allowed = ALLOWED_ATTRS.has(name);
        const safeValue = name === "href" || name === "src" ? isSafeURL(attr.value) : true;

        if (isEvent || isScriptedAttr || !allowed || !safeValue) {
          el.removeAttribute(attr.name);
        }
      });

      // Reverse tabnabbing guard, matching the server behaviour.
      if (tag === "a" && (el.getAttribute("target") ?? "").toLowerCase() === "_blank") {
        el.setAttribute("rel", "noopener noreferrer");
      }

      walk(el);
    });
  };

  walk(template.content);
  return template.innerHTML;
};

// scopeProps carries the container marker as a plain data attribute so the
// scoped stylesheet has something to match.
const scopeProps: Record<string, string> = { [SCOPE_ATTR]: "" };

// SafeHTMLContent renders administrator-authored announcement HTML.
//
// Rendering is done through innerHTML, which by itself never executes <script>
// elements; combined with the allow-list above (and the authoritative
// server-side sanitiser) it is safe to display. No <script> re-creation is done
// here on purpose — unlike CustomHTMLContent, which executes footer scripts on
// behalf of the administrator.
//
// The container carries SCOPE_ATTR, which is what confines the announcement's
// own CSS to this subtree: the stylesheet's selectors all require it.
const SafeHTMLContent = ({ html, sx }: SafeHTMLContentProps) => {
  const safe = useMemo(() => sanitizeHTML(html), [html]);

  return (
    <Box
      {...scopeProps}
      sx={{
        wordBreak: "break-word",
        "& img": { maxWidth: "100%", height: "auto" },
        "& table": { maxWidth: "100%", borderCollapse: "collapse" },
        "& td, & th": { padding: "4px 8px", border: "1px solid", borderColor: "divider" },
        "& a": { color: "primary.main" },
        "& pre": { overflowX: "auto" },
        ...sx,
      }}
      dangerouslySetInnerHTML={{ __html: safe }}
    />
  );
};

export default SafeHTMLContent;
