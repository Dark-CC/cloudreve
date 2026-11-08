// Package htmlsafe sanitises administrator-authored rich text (currently the
// site announcement) before it is rendered as HTML in the user-facing UI.
//
// The announcement is editable through the admin settings page, so its content
// reaches every logged-in user's browser. Rendering it as raw HTML without
// filtering would turn that settings field into a stored XSS sink: one mistaken
// or malicious paste (or a compromised admin session) would execute script in
// every visitor's page, with access to their session token and files.
//
// The policy below is an allow-list: anything not explicitly named is dropped,
// markup is re-serialised from parsed tokens rather than pattern-matched, and
// the input is never echoed back verbatim. Sanitising server-side (instead of in
// the browser) means the guarantee holds for every client, including the mobile
// and desktop apps, and cannot be bypassed by calling the API directly.
package htmlsafe

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ScopeAttribute marks the container the announcement is rendered in. Every
// selector emitted from a <style> block is rewritten to match only inside an
// element carrying it (see SanitizeCSS), which is what keeps announcement CSS
// from repainting the rest of the site. The value is shared by contract with
// frontend/src/component/Common/SafeHTMLContent.tsx.
const ScopeAttribute = "data-cr-announcement"

// allowedTags is the set of elements permitted in announcement content.
//
// The list is deliberately limited to structural typography and layout. The
// guiding rule: an allowed element must not be able to run code, load remote
// content on its own, or repaint the surrounding UI (which would enable
// click-jacking / UI redressing against the site chrome).
//
// Excluded on purpose, with the reason:
//   - script, noscript, template, object, embed, applet, param — execute or
//     instantiate code.
//   - iframe, frame, frameset, portal — load an attacker-controlled document on
//     top of the site; also a classic phishing surface.
//   - form, input, button, select, option, textarea, label, fieldset, legend —
//     can forge a login form inside the announcement ("enter your password to
//     continue"), the most direct way to harvest credentials through a modal.
//   - meta, base, link, title, head — retarget relative URLs or inject headers.
//   - svg, math, and every namespaced foreign element — long history of
//     sanitizer bypasses, and unnecessary for text announcements.
//   - style element — handled separately: its text is passed through
//     SanitizeCSS (selector scoping + declaration filtering) and re-emitted as
//     one scoped <style>. An unfiltered style element never reaches the output.
//   - video, audio, source, track — auto-loads remote media; embedding is still
//     possible through an allowed <a> link instead.
type allowedTags = map[string]struct{}

var allowedTagsSet = allowedTags{
	"a": {}, "abbr": {}, "b": {}, "blockquote": {}, "br": {}, "caption": {},
	"center": {}, "cite": {}, "code": {}, "col": {}, "colgroup": {}, "dd": {},
	"del": {}, "details": {}, "dfn": {}, "div": {}, "dl": {}, "dt": {}, "em": {},
	"figcaption": {}, "figure": {}, "h1": {}, "h2": {}, "h3": {}, "h4": {},
	"h5": {}, "h6": {}, "hr": {}, "i": {}, "img": {}, "ins": {}, "kbd": {},
	"li": {}, "mark": {}, "ol": {}, "p": {}, "pre": {}, "q": {}, "rp": {}, "rt": {},
	"ruby": {}, "s": {}, "samp": {}, "small": {}, "span": {}, "strike": {},
	"strong": {}, "sub": {}, "summary": {}, "sup": {}, "table": {}, "tbody": {},
	"td": {}, "tfoot": {}, "th": {}, "thead": {}, "tr": {}, "tt": {}, "u": {},
	"ul": {}, "var": {}, "wbr": {},
}

// globalAttributes are allowed on every permitted element. `class` is included
// so existing markdown/editor output keeps its styling hooks; `style` is
// admitted only through sanitizeDeclarations (see writeAttrs).
var globalAttributes = map[string]struct{}{
	"class": {}, "id": {}, "title": {}, "dir": {}, "lang": {},
}

// tagAttributes are the extra attributes permitted for a given tag. Every
// attribute not listed here (and not in globalAttributes) is dropped, which
// removes all on* event handlers, `srcdoc`, `formaction`, `xlink:href`, etc.
var tagAttributes = map[string]map[string]struct{}{
	"a":       {"href": {}, "target": {}, "rel": {}, "name": {}},
	"img":     {"src": {}, "alt": {}, "width": {}, "height": {}, "loading": {}},
	"td":      {"colspan": {}, "rowspan": {}, "align": {}, "valign": {}},
	"th":      {"colspan": {}, "rowspan": {}, "align": {}, "valign": {}, "scope": {}},
	"col":     {"span": {}, "width": {}},
	"ol":      {"start": {}, "type": {}, "reversed": {}},
	"ul":      {"type": {}},
	"li":      {"value": {}},
	"details": {"open": {}},
	"table":   {"border": {}, "cellpadding": {}, "cellspacing": {}, "width": {}},
	"hr":      {"size": {}, "width": {}, "noshade": {}},
	"p":       {"align": {}},
	"div":     {"align": {}},
}

// voidTags are elements that must never carry children or a closing tag.
var voidTags = map[string]struct{}{
	"br": {}, "col": {}, "hr": {}, "img": {}, "wbr": {},
}

// unsafeURLSchemes are rejected for href/src. javascript: and vbscript: execute
// on click/navigation; data: can carry an HTML/SVG payload; blob: and filesystem:
// can reference previously-injected content.
var unsafeURLSchemes = []string{
	"javascript:", "vbscript:", "data:", "blob:", "filesystem:",
}

// Sanitize parses untrusted HTML and re-serialises it with only allow-listed
// elements and attributes. Text content is preserved; disallowed markup is
// unwrapped (children kept) or dropped entirely (for void/hostile elements).
func Sanitize(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}

	// Parse a fragment rather than a whole document: the announcement is not a
	// standalone page, and ParseFragment keeps leading text nodes. It also
	// flattens a pasted full document (doctype, html, head, body wrappers) into
	// its parts, which is what an administrator pasting a saved page expects.
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(input), ctx)
	if err != nil {
		// Never fall back to returning the raw input — dropping is the safe
		// failure mode for a sanitizer.
		return ""
	}

	// Style sheets are collected first and handled by SanitizeCSS, which scopes
	// every selector to the announcement container. They are still dropped by
	// writeNode below, so no <style> ever reaches the output unfiltered.
	var styleText strings.Builder
	for _, n := range nodes {
		collectStyleText(n, &styleText)
	}

	var b strings.Builder
	if css := SanitizeCSS(styleText.String()); css != "" {
		b.WriteString("<style>")
		b.WriteString(css)
		b.WriteString("</style>")
	}
	for _, n := range nodes {
		writeNode(&b, n)
	}

	return b.String()
}

// collectStyleText appends the text of every <style> element in the tree, in
// document order.
func collectStyleText(n *html.Node, b *strings.Builder) {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "style") {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				b.WriteString(c.Data)
			}
		}
		b.WriteString("\n")
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectStyleText(c, b)
	}
}

// writeNode serialises n and its subtree, applying the allow-list.
func writeNode(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		// The html package escapes text when writing, but we are writing to a
		// strings.Builder, so escape the few characters that matter.
		b.WriteString(escapeText(n.Data))
	case html.ElementNode:
		tag := strings.ToLower(n.Data)

		if _, ok := allowedTagsSet[tag]; !ok {
			// script/style and friends must vanish with their contents;
			// other unknown wrappers are unwrapped so their text survives.
			if dropWithContents(tag) {
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				writeNode(b, c)
			}
			return
		}

		b.WriteString("<")
		b.WriteString(tag)
		writeAttrs(b, tag, n.Attr)
		b.WriteString(">")

		if _, isVoid := voidTags[tag]; isVoid {
			return
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeNode(b, c)
		}

		b.WriteString("</")
		b.WriteString(tag)
		b.WriteString(">")
	case html.CommentNode:
		// Comments can hide conditional-comment payloads and are never needed.
		return
	case html.DoctypeNode, html.DocumentNode, html.ErrorNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeNode(b, c)
		}
	}
}

// dropWithContents reports whether a disallowed tag should take its subtree
// with it. Only for elements whose content is code or fallback markup.
func dropWithContents(tag string) bool {
	switch tag {
	case "script", "style", "noscript", "template", "iframe", "object",
		"embed", "applet", "param", "svg", "math", "canvas", "audio", "video",
		"source", "track", "form", "input", "button", "select", "option",
		"textarea", "label", "fieldset", "legend", "meta", "base", "link",
		"title", "head", "frame", "frameset", "noframes", "portal":
		return true
	}
	return false
}

// writeAttrs emits only allow-listed attributes, with value-level checks.
func writeAttrs(b *strings.Builder, tag string, attrs []html.Attribute) {
	perTag := tagAttributes[tag]

	for _, a := range attrs {
		// Namespaced attributes (svg:xlink, xml:base, xlink:href) are skipped
		// outright: they are the usual vehicle for sanitizer bypasses.
		if a.Namespace != "" {
			continue
		}

		name := strings.ToLower(a.Key)

		// Defence in depth: refuse every event handler even if a future edit
		// adds one to a list above.
		if strings.HasPrefix(name, "on") || name == "srcdoc" || name == "formaction" {
			continue
		}

		// style is admitted at all only through the declaration filter below,
		// which keeps the visual properties an administrator needs and drops
		// everything that positions, animates or loads remote content.
		if name == "style" {
			if decls := sanitizeDeclarations(a.Val); decls != "" {
				b.WriteString(" style=\"")
				b.WriteString(escapeAttr(decls))
				b.WriteString("\"")
			}
			continue
		}

		if _, ok := globalAttributes[name]; !ok {
			if _, ok := perTag[name]; !ok {
				continue
			}
		}

		value := a.Val

		switch name {
		case "href", "src":
			if !safeURL(value) {
				continue
			}
		case "target":
			// target=_blank without noopener lets the opened page reach back
			// into this window through window.opener.
			if strings.EqualFold(strings.TrimSpace(value), "_blank") {
				b.WriteString(` rel="noopener noreferrer"`)
			}
		case "rel":
			// rel is rewritten above when target=_blank; skip the raw value so
			// an attacker cannot strip the protection by supplying their own.
			continue
		}

		b.WriteString(" ")
		b.WriteString(name)
		b.WriteString(`="`)
		b.WriteString(escapeAttr(value))
		b.WriteString(`"`)
	}
}

// safeURL rejects schemes that execute or embed documents. Relative URLs and
// http(s)/mailto/tel are allowed. Leading whitespace and control characters are
// stripped first because browsers ignore them when parsing the scheme.
func safeURL(raw string) bool {
	v := strings.TrimSpace(raw)
	// Strip characters browsers skip inside a scheme (e.g. "java\tscript:").
	v = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r == '\x00' {
			return -1
		}
		return r
	}, v)
	lower := strings.ToLower(v)

	for _, s := range unsafeURLSchemes {
		if strings.HasPrefix(lower, s) {
			return false
		}
	}
	return true
}

// escapeText escapes the characters that would otherwise re-open markup.
func escapeText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// escapeAttr escapes a value placed inside a double-quoted attribute.
func escapeAttr(s string) string {
	s = escapeText(s)
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}
