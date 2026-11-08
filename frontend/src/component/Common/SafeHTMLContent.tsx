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
  "a", "abbr", "b", "blockquote", "br", "caption", "center", "cite", "code",
  "col", "colgroup", "dd", "del", "details", "dfn", "div", "dl", "dt", "em",
  "figcaption", "figure", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "img",
  "ins", "kbd", "li", "mark", "ol", "p", "pre", "q", "rp", "rt", "ruby", "s",
  "samp", "small", "span", "strike", "strong", "sub", "summary", "sup", "table",
  "tbody", "td", "tfoot", "th", "thead", "tr", "tt", "u", "ul", "var", "wbr",
]);

const ALLOWED_ATTRS = new Set([
  "class", "id", "title", "dir", "lang",
  "href", "target", "alt", "src", "width", "height", "loading",
  "colspan", "rowspan", "align", "valign", "scope", "span", "start", "type",
  "value", "reversed", "open", "border", "cellpadding", "cellspacing", "size",
  "noshade", "name", "rel",
]);

// Attributes whose entire subtree is unusable and must be removed with content.
const DROP_WITH_CONTENT = new Set([
  "script", "style", "noscript", "template", "iframe", "object", "embed",
  "applet", "param", "svg", "math", "canvas", "audio", "video", "source",
  "track", "form", "input", "button", "select", "option", "textarea", "label",
  "fieldset", "legend", "meta", "base", "link", "title", "head", "frame",
  "frameset", "noframes", "portal",
]);

const UNSAFE_SCHEMES = ["javascript:", "vbscript:", "data:", "blob:", "filesystem:"];

const isSafeURL = (raw: string): boolean => {
  const v = raw.replace(/[\t\n\r\0]/g, "").trim().toLowerCase();
  return !UNSAFE_SCHEMES.some((s) => v.startsWith(s));
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
        const isStyle = name === "style" || name === "srcdoc" || name === "formaction";
        const allowed = ALLOWED_ATTRS.has(name);
        const safeValue =
          name === "href" || name === "src" ? isSafeURL(attr.value) : true;

        if (isEvent || isStyle || !allowed || !safeValue) {
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

// SafeHTMLContent renders administrator-authored announcement HTML.
//
// Rendering is done through innerHTML, which by itself never executes <script>
// elements; combined with the allow-list above (and the authoritative
// server-side sanitiser) it is safe to display. No <script> re-creation is done
// here on purpose — unlike CustomHTMLContent, which executes footer scripts on
// behalf of the administrator.
const SafeHTMLContent = ({ html, sx }: SafeHTMLContentProps) => {
  const safe = useMemo(() => sanitizeHTML(html), [html]);

  return (
    <Box
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
