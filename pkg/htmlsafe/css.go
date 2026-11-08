package htmlsafe

import (
	"strings"
)

// ScopeAttribute (declared in htmlsafe.go) is the attribute the announcement
// container carries. Every selector that survives this file is rewritten so it
// can only match inside that container.
//
// Why scope the CSS instead of trusting the administrator: the announcement is
// shown to every logged-in user inside a dialog, so an unscoped rule such as
// `button { display: none }` or a viewport-covering overlay would repaint the
// rest of the site for everyone. Prefixing every selector keeps the styling the
// administrator wrote while making it impossible for it to reach the site
// chrome.

const (
	// maxCSSBytes bounds the CSS we are willing to parse. Announcements are
	// hand-written notices; anything larger is not a formatting request.
	maxCSSBytes = 64 << 10
	// maxCSSRules bounds the number of rules emitted, so a pathological
	// payload cannot turn into megabytes of cssText.
	maxCSSRules = 500
	// maxCSSNesting bounds @media-in-@media recursion.
	maxCSSNesting = 3
)

var scopeSelectorPrefix = "[" + ScopeAttribute + "]"

// allowedCSSProperties is the property allow-list. A property not named here is
// dropped from the declaration list, which is what keeps `position: fixed`,
// `z-index`, `transform`, `filter`, `animation` and every vendor escape hatch
// out of announcement styling.
var allowedCSSProperties = map[string]struct{}{
	// 颜色与文字
	"color": {}, "opacity": {}, "visibility": {},
	"font": {}, "font-family": {}, "font-size": {}, "font-weight": {},
	"font-style": {}, "font-variant": {}, "font-stretch": {},
	"line-height": {}, "letter-spacing": {}, "word-spacing": {},
	"text-align": {}, "text-indent": {}, "text-transform": {},
	"text-shadow": {}, "text-decoration": {}, "text-decoration-line": {},
	"text-decoration-color": {}, "text-decoration-style": {},
	"text-decoration-thickness": {}, "text-underline-offset": {},
	"white-space": {}, "word-break": {}, "overflow-wrap": {}, "word-wrap": {},
	"vertical-align": {}, "table-layout": {}, "float": {}, "clear": {},

	// 背景（不含图片：值层面的 url() 检查会拦住任何远程加载）
	"background": {}, "background-color": {}, "background-image": {},
	"background-position": {}, "background-size": {}, "background-repeat": {},
	"background-clip": {}, "background-origin": {}, "background-attachment": {},

	// 盒模型
	"width": {}, "height": {}, "min-width": {}, "min-height": {},
	"max-width": {}, "max-height": {}, "aspect-ratio": {},
	"box-sizing": {}, "box-shadow": {},
	"margin": {}, "margin-top": {}, "margin-right": {},
	"margin-bottom": {}, "margin-left": {},
	"padding": {}, "padding-top": {}, "padding-right": {},
	"padding-bottom": {}, "padding-left": {},
	"border": {}, "border-width": {}, "border-style": {}, "border-color": {},
	"border-radius": {}, "border-collapse": {}, "border-spacing": {},
	"border-top": {}, "border-right": {}, "border-bottom": {}, "border-left": {},
	"border-top-width": {}, "border-top-style": {}, "border-top-color": {},
	"border-right-width": {}, "border-right-style": {}, "border-right-color": {},
	"border-bottom-width": {}, "border-bottom-style": {}, "border-bottom-color": {},
	"border-left-width": {}, "border-left-style": {}, "border-left-color": {},
	"border-top-left-radius": {}, "border-top-right-radius": {},
	"border-bottom-left-radius": {}, "border-bottom-right-radius": {},
	"outline": {}, "outline-color": {}, "outline-style": {},
	"outline-width": {}, "outline-offset": {},

	// 布局
	"display": {}, "order": {},
	"flex": {}, "flex-grow": {}, "flex-shrink": {}, "flex-basis": {},
	"flex-direction": {}, "flex-wrap": {}, "flex-flow": {},
	"gap": {}, "row-gap": {}, "column-gap": {},
	"align-items": {}, "align-content": {}, "align-self": {},
	"justify-content": {}, "justify-items": {}, "justify-self": {},
	"place-items": {}, "place-content": {}, "place-self": {},
	"grid": {}, "grid-gap": {}, "grid-column-gap": {}, "grid-row-gap": {},
	"grid-template": {}, "grid-template-columns": {}, "grid-template-rows": {},
	"grid-template-areas": {}, "grid-auto-flow": {}, "grid-auto-columns": {},
	"grid-auto-rows": {}, "grid-column": {}, "grid-row": {}, "grid-area": {},
	"columns": {}, "column-count": {},
	"overflow": {}, "overflow-x": {}, "overflow-y": {},

	// 其他纯展示属性
	"object-fit": {}, "object-position": {},
	"list-style": {}, "list-style-type": {}, "list-style-position": {},
}

// bannedCSSValueTokens are rejected wherever they appear in a value. url() and
// image-set() load remote content (tracking pixels, exfiltration, and — via
// javascript:/data: — execution); expression()/behavior are legacy script
// vectors; @ and backslash open parser-differential tricks; </ would close the
// <style> element early.
var bannedCSSValueTokens = []string{
	"url(", "image-set(", "src(", "expression(", "behavior", "-moz-binding",
	"javascript:", "vbscript:", "data:", "@", "\\", "/*", "</", "{", "}",
}

// bannedCSSUnits are viewport-relative units. They are what turns a harmless
// `min-height` into a full-screen cover inside a modal, so the declaration that
// uses one is dropped.
var bannedCSSUnits = map[string]struct{}{
	"vh": {}, "vw": {}, "vmin": {}, "vmax": {},
	"lvh": {}, "lvw": {}, "svh": {}, "svw": {}, "dvh": {}, "dvw": {},
}

// SanitizeCSS rewrites author CSS so that every rule matches only inside the
// announcement container, keeping only allow-listed properties and harmless
// values. The transformation is idempotent: the write path and the read path
// both run it, and so does the browser.
func SanitizeCSS(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	if len(input) > maxCSSBytes {
		input = input[:maxCSSBytes]
	}

	var b strings.Builder
	w := &cssWriter{}
	w.writeRuleList(&b, input, 0)
	return b.String()
}

type cssWriter struct {
	rules int
}

// writeRuleList consumes a sequence of rules (the top level of a stylesheet, or
// the body of a conditional group rule).
func (w *cssWriter) writeRuleList(b *strings.Builder, src string, depth int) {
	if depth > maxCSSNesting {
		return
	}

	i := 0
	for i < len(src) {
		for i < len(src) && isCSSSpace(src[i]) {
			i++
		}
		if i >= len(src) {
			return
		}
		// Comments are dropped: they are the usual hiding place for payloads
		// split across a "property/*x*/:value" boundary.
		if src[i] == '/' && i+1 < len(src) && src[i+1] == '*' {
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				return
			}
			i += j + 4
			continue
		}
		if src[i] == '}' {
			i++
			continue
		}

		idx, stop := indexTopLevel(src[i:], "{;")
		if idx < 0 {
			return
		}
		idx += i
		prelude := strings.TrimSpace(src[i:idx])
		if stop == ';' {
			// Statement at-rules (@import, @charset) and stray declarations.
			i = idx + 1
			continue
		}

		end := matchBrace(src, idx)
		if end < 0 {
			return
		}
		w.writeRule(b, prelude, src[idx+1:end], depth)
		i = end + 1
	}
}

// writeRule turns one `prelude { body }` into either a scoped style rule or a
// scoped conditional group rule.
func (w *cssWriter) writeRule(b *strings.Builder, prelude, body string, depth int) {
	if strings.HasPrefix(prelude, "@") {
		switch atRuleName(prelude) {
		case "media", "supports":
			if !safeAtRulePrelude(prelude) {
				return
			}
			var inner strings.Builder
			w.writeRuleList(&inner, body, depth+1)
			if inner.Len() == 0 {
				return
			}
			b.WriteString(prelude)
			b.WriteString("{")
			b.WriteString(inner.String())
			b.WriteString("}")
		default:
			// @import, @font-face, @keyframes, @page, @layer, @charset … are all
			// dropped: none of them are needed to style a notice.
		}
		return
	}

	if w.rules >= maxCSSRules {
		return
	}

	decls := sanitizeDeclarations(body)
	if decls == "" {
		return
	}
	selectors := scopeSelectors(prelude)
	if selectors == "" {
		return
	}

	w.rules++
	b.WriteString(selectors)
	b.WriteString("{")
	b.WriteString(decls)
	b.WriteString("}")
}

// atRuleName returns the lower-case at-rule name without the leading '@'.
func atRuleName(prelude string) string {
	name := strings.TrimPrefix(prelude, "@")
	if i := strings.IndexAny(name, " \t\r\n\f("); i >= 0 {
		name = name[:i]
	}
	return strings.ToLower(name)
}

// safeAtRulePrelude keeps @media/@supports preludes that cannot smuggle a
// declaration or a URL into the output.
func safeAtRulePrelude(prelude string) bool {
	// The leading '@name' is the at-rule itself, not content: strip it before
	// checking values, otherwise every prelude would hit the '@' token.
	lower := strings.ToLower(strings.TrimPrefix(prelude, "@"))
	for _, token := range bannedCSSValueTokens {
		if strings.Contains(lower, token) {
			return false
		}
	}
	return !strings.Contains(lower, ";")
}

// scopeSelectors scopes a comma-separated selector list. A selector that cannot
// be mapped into the container (for example `body.dark .x`, where the container
// has no way to know whether the page is in dark mode) is dropped rather than
// approximated, because a wrong analogue would style the notice incorrectly.
func scopeSelectors(prelude string) string {
	parts := splitTopLevel(prelude, ',')
	scoped := make([]string, 0, len(parts))
	for _, part := range parts {
		if s, ok := scopeSelector(strings.TrimSpace(part)); ok {
			scoped = append(scoped, s)
		}
	}
	if len(scoped) == 0 {
		return ""
	}
	return strings.Join(scoped, ",")
}

// scopeSelector rewrites one complex selector.
func scopeSelector(sel string) (string, bool) {
	if sel == "" {
		return "", false
	}
	// Idempotency: the stored value has already been scoped by the write path,
	// so a second pass must not stack another prefix.
	if strings.HasPrefix(sel, scopeSelectorPrefix) {
		return sel, true
	}

	first, combinator, rest := splitFirstCompound(sel)
	if first == "" {
		return "", false
	}

	lower := strings.ToLower(first)
	var base string
	switch {
	case lower == "html" || lower == "body" || lower == ":root":
		// The page/document element becomes the container itself, so
		// `body { background: #f4f6f8 }` paints the notice background and
		// `body { display: flex }` centres the notice inside the dialog.
		base = scopeSelectorPrefix
	case hasDocumentPrefix(lower, "html"), hasDocumentPrefix(lower, "body"), hasDocumentPrefix(lower, ":root"):
		// `body.dark …`, `html[data-theme] …`, `:root.dark`: the container
		// cannot express the page-level state, so drop rather than guess.
		return "", false
	default:
		// Everything else keeps its own selector and is merely narrowed to the
		// subtree. `*` lands here too, so `* { box-sizing: border-box }` still
		// reaches the descendants of the announcement.
		base = scopeSelectorPrefix + " " + first
	}

	if rest == "" {
		return base, true
	}
	if combinator == " " {
		return base + " " + rest, true
	}
	return base + " " + combinator + " " + rest, true
}

// hasDocumentPrefix reports whether the compound selector starts with the given
// document selector followed by further simple selectors.
func hasDocumentPrefix(lower, name string) bool {
	if !strings.HasPrefix(lower, name) || len(lower) == len(name) {
		return false
	}
	next := lower[len(name)]
	return next == '.' || next == '#' || next == '[' || next == ':'
}

// splitFirstCompound splits a complex selector after its first compound
// selector and returns that selector, the combinator that follows it (" ",
// ">", "+", "~") and the remainder.
func splitFirstCompound(sel string) (first, combinator, rest string) {
	depthParen, depthBracket := 0, 0
	i := 0
	for i < len(sel) {
		c := sel[i]
		switch {
		case c == '\\':
			i += 2
			continue
		case c == '\'' || c == '"':
			q := c
			i++
			for i < len(sel) {
				if sel[i] == '\\' {
					i += 2
					continue
				}
				if sel[i] == q {
					i++
					break
				}
				i++
			}
			continue
		case c == '(':
			depthParen++
		case c == ')':
			if depthParen > 0 {
				depthParen--
			}
		case c == '[':
			depthBracket++
		case c == ']':
			if depthBracket > 0 {
				depthBracket--
			}
		case depthParen == 0 && depthBracket == 0:
			switch c {
			case '>', '+', '~':
				return strings.TrimSpace(sel[:i]), string(c), strings.TrimSpace(sel[i+1:])
			case ' ', '\t', '\r', '\n', '\f':
				return strings.TrimSpace(sel[:i]), " ", strings.TrimSpace(sel[i:])
			}
		}
		i++
	}
	return strings.TrimSpace(sel), "", ""
}

// sanitizeDeclarations filters a declaration block down to allow-listed
// properties with harmless values. It is used both for the <style> text and for
// inline style attributes.
func sanitizeDeclarations(block string) string {
	out := make([]string, 0, 8)
	for _, raw := range splitTopLevel(block, ';') {
		decl := strings.TrimSpace(raw)
		if decl == "" {
			continue
		}
		idx, stop := indexTopLevel(decl, ":")
		if stop != ':' || idx <= 0 {
			continue
		}
		name := strings.TrimSpace(decl[:idx])
		value := strings.TrimSpace(decl[idx+1:])
		if name == "" || value == "" {
			continue
		}
		if !allowedCSSProperty(name) {
			continue
		}
		if !safeCSSValue(value) {
			continue
		}
		out = append(out, name+":"+value)
	}
	return strings.Join(out, ";")
}

// allowedCSSProperty reports whether a declaration may be kept. Custom
// properties (--brand-color) are allowed so an administrator can share a value
// between rules; they are value-checked like any other declaration and, being
// scoped to the container, they cannot leak into the site.
func allowedCSSProperty(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "--") {
		return true
	}
	if strings.ContainsAny(lower, "*_$&\\/@") {
		// Star/underscore hacks and anything that is not a plain identifier.
		return false
	}
	_, ok := allowedCSSProperties[lower]
	return ok
}

// safeCSSValue rejects values that load remote content, run script, break out
// of the <style> element, or size things against the viewport.
func safeCSSValue(value string) bool {
	lower := strings.ToLower(value)
	for _, token := range bannedCSSValueTokens {
		if strings.Contains(lower, token) {
			return false
		}
	}
	if hasBannedUnit(lower) {
		return false
	}
	// A value must not carry a declaration separator: `color: red; position:
	// fixed` pasted into an inline style attribute would otherwise survive.
	return !strings.Contains(lower, ";")
}

// hasBannedUnit scans for numbers followed by a viewport-relative unit.
func hasBannedUnit(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= '0' && c <= '9') && c != '.' {
			continue
		}
		j := i
		for j < len(value) && ((value[j] >= '0' && value[j] <= '9') || value[j] == '.') {
			j++
		}
		k := j
		for k < len(value) && value[k] >= 'a' && value[k] <= 'z' {
			k++
		}
		if k > j {
			if _, banned := bannedCSSUnits[value[j:k]]; banned {
				return true
			}
		}
		i = k - 1
	}
	return false
}

// indexTopLevel finds the first byte from stops that is not inside a string, a
// parenthesised group or a comment.
func indexTopLevel(s string, stops string) (int, byte) {
	depth := 0
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return -1, 0
			}
			i += j + 4
			continue
		case c == '\'' || c == '"':
			q := c
			i++
			for i < len(s) {
				if s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == q {
					i++
					break
				}
				i++
			}
			continue
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0 && strings.IndexByte(stops, c) >= 0:
			return i, c
		}
		i++
	}
	return -1, 0
}

// matchBrace returns the index of the '}' matching the '{' at openIdx.
func matchBrace(s string, openIdx int) int {
	depth := 0
	i := openIdx
	for i < len(s) {
		c := s[i]
		switch {
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return -1
			}
			i += j + 4
			continue
		case c == '\'' || c == '"':
			q := c
			i++
			for i < len(s) {
				if s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == q {
					i++
					break
				}
				i++
			}
			continue
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// splitTopLevel splits s on sep, ignoring separators inside strings, comments
// and parenthesised groups.
func splitTopLevel(s string, sep byte) []string {
	parts := make([]string, 0, 4)
	depth := 0
	start := 0
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return append(parts, s[start:])
			}
			i += j + 4
			continue
		case c == '\'' || c == '"':
			q := c
			i++
			for i < len(s) {
				if s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == q {
					i++
					break
				}
				i++
			}
			continue
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0 && c == sep:
			parts = append(parts, s[start:i])
			start = i + 1
		}
		i++
	}
	return append(parts, s[start:])
}

func isCSSSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f'
}
