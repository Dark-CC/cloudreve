package htmlsafe

import (
	"strings"
	"testing"
)

// TestSanitizeCSSScopesSelectors is the core contract: every rule may only
// match inside the announcement container.
func TestSanitizeCSSScopesSelectors(t *testing.T) {
	in := `.card { color: #333; padding: 10px } .card h2 { margin: 0 }`
	want := "[data-cr-announcement] .card{color:#333;padding:10px}" +
		"[data-cr-announcement] .card h2{margin:0}"
	if got := SanitizeCSS(in); got != want {
		t.Fatalf("SanitizeCSS(%q)\n got: %s\nwant: %s", in, got, want)
	}
}

// TestSanitizeCSSDocumentElementMapsToContainer covers the "paste a whole page"
// case: body/html/:root rules must style the container, never the page.
func TestSanitizeCSSDocumentElementMapsToContainer(t *testing.T) {
	in := `body { background-color: #fff; display: flex; min-height: 100vh } :root { --brand: #0056b3 }`
	want := "[data-cr-announcement]{background-color:#fff;display:flex}" +
		"[data-cr-announcement]{--brand:#0056b3}"
	if got := SanitizeCSS(in); got != want {
		t.Fatalf("document element mapping\n got: %s\nwant: %s", got, want)
	}
}

// TestSanitizeCSSCombinators keeps descendant/child selectors usable.
func TestSanitizeCSSCombinators(t *testing.T) {
	cases := map[string]string{
		`.a > .b { margin: 0 }`:        "[data-cr-announcement] .a > .b{margin:0}",
		`body > .card { padding: 0 }`:  "[data-cr-announcement] > .card{padding:0}",
		`* { box-sizing: border-box }`: "[data-cr-announcement] *{box-sizing:border-box}",
	}
	for in, want := range cases {
		if got := SanitizeCSS(in); got != want {
			t.Errorf("SanitizeCSS(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSanitizeCSSKeepsMediaQuery makes responsive rules survive, scoped inside.
func TestSanitizeCSSKeepsMediaQuery(t *testing.T) {
	in := `@media (max-width: 600px) { .card { padding: 10px } }`
	want := "@media (max-width: 600px){[data-cr-announcement] .card{padding:10px}}"
	if got := SanitizeCSS(in); got != want {
		t.Fatalf("SanitizeCSS(%q)\n got: %s\nwant: %s", in, got, want)
	}
}

// TestSanitizeCSSDropsDocumentStateSelectors: a body-level state (dark mode,
// theme attribute) cannot be expressed by the container, so such a rule is
// dropped instead of being applied unconditionally.
func TestSanitizeCSSDropsDocumentStateSelectors(t *testing.T) {
	cases := []string{
		`body.dark .card { color: #fff }`,
		`html[data-theme="dark"] .card { color: #fff }`,
		`:root.dark { color: #fff }`,
	}
	for _, in := range cases {
		if got := SanitizeCSS(in); got != "" {
			t.Errorf("SanitizeCSS(%q) = %q, want dropped", in, got)
		}
	}

	// A mixed list keeps the mappable selectors only.
	got := SanitizeCSS(`.card, body.dark .x { color: #333 }`)
	if got != "[data-cr-announcement] .card{color:#333}" {
		t.Errorf("mixed selector list = %q", got)
	}
}

// TestSanitizeCSSIsIdempotent matters because the write path and the read path
// both run the sanitiser over the same stored value.
func TestSanitizeCSSIsIdempotent(t *testing.T) {
	in := `body { background: #fff; display: flex } .card { border-radius: 8px; box-shadow: 0 4px 12px rgba(0, 0, 0, .08) }` +
		`@media (max-width: 600px) { .card { padding: 10px } }`
	once := SanitizeCSS(in)
	twice := SanitizeCSS(once)
	if once != twice {
		t.Fatalf("not idempotent\n once: %s\ntwice: %s", once, twice)
	}
}

// TestSanitizeCSSStripsDangerous covers the payload table required by the
// report: nothing that loads remote content, runs script, positions against the
// viewport, or breaks out of the <style> element may survive.
func TestSanitizeCSSStripsDangerous(t *testing.T) {
	cases := []struct {
		in   string
		bad  []string
		note string
	}{
		{`a { background: url(http://evil.example/x.png) }`, []string{"url("}, "远程背景图"},
		{`a { background: url("javascript:alert(1)") }`, []string{"url(", "javascript:"}, "javascript 协议"},
		{`a { background-image: image-set("http://evil.example/x.png") }`, []string{"image-set("}, "image-set"},
		{`a { width: expression(alert(1)) }`, []string{"expression("}, "expression()"},
		{`a { behavior: url(#default#time2) }`, []string{"behavior", "url("}, "behavior"},
		{`a { position: fixed; inset: 0; z-index: 9999 }`, []string{"position", "inset", "z-index"}, "覆盖全屏"},
		{`a { min-height: 100vh; width: 100vw }`, []string{"vh", "vw"}, "视口单位"},
		{`a { transform: translateX(-100vw) }`, []string{"transform"}, "transform 逃逸"},
		{`a { filter: blur(2px); animation: spin 1s }`, []string{"filter", "animation"}, "滤镜/动画"},
		{`@import url(http://evil.example/x.css);`, []string{"@import", "url("}, "@import"},
		{`@font-face { font-family: x; src: url(http://evil.example/x.woff) }`, []string{"@font-face", "url("}, "@font-face"},
		{`@keyframes spin { from { opacity: 0 } to { opacity: 1 } }`, []string{"@keyframes"}, "@keyframes"},
		{`</style><script>alert(1)</script>`, []string{"<script", "</style"}, "闭合注入"},
		{`a { color: red } /* } body { position: fixed } */`, []string{"position"}, "注释藏匿"},
		{`a { color: red } } body { position: absolute }`, []string{"position"}, "多余右括号"},
		{`a { background: image-set(url(x) 1x) }`, []string{"url(", "image-set("}, "嵌套 url"},
		{`a { c\olor: red }`, []string{"c\\olor"}, "CSS 转义"},
	}

	for _, c := range cases {
		got := SanitizeCSS(c.in)
		lower := strings.ToLower(got)
		for _, bad := range c.bad {
			if strings.Contains(lower, strings.ToLower(bad)) {
				t.Errorf("%s: SanitizeCSS(%q) = %q still contains %q", c.note, c.in, got, bad)
			}
		}
	}
}

// TestSanitizeCSSStillAllowsFormatting is the positive control: the properties
// the announcement actually needs must survive.
func TestSanitizeCSSStillAllowsFormatting(t *testing.T) {
	in := `.card { background-color: #ffffff; border-radius: 8px; box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08); ` +
		`border-top: 5px solid #0056b3; padding: 24px 30px; font-family: -apple-system, "Segoe UI", sans-serif; ` +
		`font-size: 22px; line-height: 1.6; display: flex; justify-content: center; align-items: center; ` +
		`list-style: none; text-align: right; max-width: 600px; width: 100% }`
	got := SanitizeCSS(in)
	for _, want := range []string{
		"background-color:#ffffff", "border-radius:8px", "box-shadow:0 4px 12px rgba(0, 0, 0, 0.08)",
		"border-top:5px solid #0056b3", "padding:24px 30px", `font-family:-apple-system, "Segoe UI", sans-serif`,
		"font-size:22px", "line-height:1.6", "display:flex", "justify-content:center",
		"align-items:center", "list-style:none", "text-align:right", "max-width:600px", "width:100%",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("SanitizeCSS dropped %q\n got: %s", want, got)
		}
	}
}

// TestSanitizeCSSCustomPropertiesAreScoped keeps the "share a value between
// rules" idiom working without letting the variable reach the site: it is
// declared on the container, so only descendants inherit it.
func TestSanitizeCSSCustomPropertiesAreScoped(t *testing.T) {
	in := `:root { --brand: #0056b3 } .card { border-top: 5px solid var(--brand) }`
	want := "[data-cr-announcement]{--brand:#0056b3}[data-cr-announcement] .card{border-top:5px solid var(--brand)}"
	if got := SanitizeCSS(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestSanitizeCSSLimitsRuleCount keeps a pathological payload bounded.
func TestSanitizeCSSLimitsRuleCount(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxCSSRules*2; i++ {
		b.WriteString(".x{color:red}")
	}
	got := SanitizeCSS(b.String())
	if n := strings.Count(got, "{color:red}"); n > maxCSSRules {
		t.Fatalf("emitted %d rules, limit is %d", n, maxCSSRules)
	}
}

// TestSanitizeCSSEmpty covers the "no style block" path.
func TestSanitizeCSSEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t", "/* 只有注释 */"} {
		if got := SanitizeCSS(in); got != "" {
			t.Errorf("SanitizeCSS(%q) = %q, want empty", in, got)
		}
	}
}
