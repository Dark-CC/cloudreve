package htmlsafe

import (
	"os"
	"strings"
	"testing"
)

// The verification page is a real browser check, not a unit test: it renders the
// sanitised announcement and asserts with getComputedStyle that (a) the authored
// styling survived, (b) nothing outside the announcement container picked it up,
// (c) the viewport-sizing declarations are gone, and (d) no script ran.
//
// It is regenerated from the current sanitiser output with:
//
//	HTMLSAFE_UPDATE_DOCS=1 go test ./pkg/htmlsafe/ -run TestGenerateVerificationPage
//
// The result is committed as docs/announcement-css/css-scope-verification.html
// so the page can be opened (or previewed) without a build step.
const verificationPageTemplate = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>公告受控 CSS 作用域验证</title>
<style>
  /* 仅用于本验证页自身的外观，与公告无关 */
  html, body { margin: 0; padding: 0; background: #ffffff; }
  body { font: 14px/1.6 system-ui, -apple-system, "Segoe UI", sans-serif; padding: 20px 24px; color: #222; }
  h1 { font-size: 20px; margin: 0 0 8px; }
  h2 { font-size: 15px; margin: 28px 0 8px; padding-top: 8px; border-top: 1px solid #e6e6e6; }
  p.lead { color: #555; margin: 0 0 8px; }
  .frame { border: 1px dashed #bbb; padding: 8px; }
  #results { background: #111; color: #d8f5d0; padding: 12px; border-radius: 6px; font: 12px/1.5 monospace; white-space: pre-wrap; }
</style>
</head>
<body>
<h1>公告受控 CSS 作用域验证</h1>
<p class="lead">本页由 pkg/htmlsafe 的测试生成：B 段是服务端清洗后的真实输出（与站点保存的值逐字节一致）。页面加载后由脚本逐条断言并把结果写到最下方的黑框里。</p>

<h2>A. 对照：同一段内容放在公告容器之外</h2>
<p class="lead">由脚本从 B 段克隆而来，不带任何公告样式。若 A 段出现白底圆角卡片，说明样式泄漏了。</p>
<div class="frame" id="baseline"></div>

<h2>B. 公告容器（真实清洗输出）</h2>
<p class="lead">容器带 data-cr-announcement 标记，公告的每条选择器都要求落在它里面。</p>
<div class="frame">
<div id="announcement" data-cr-announcement>
{{SANITIZED}}
</div>
</div>

<h2>C. 污染检查：容器外的同名类</h2>
<p class="lead">下面这个元素也叫 announcement-card，但它不在公告容器里，必须保持无样式。</p>
<div class="frame">
  <div id="outside" class="announcement-card">
    <span class="notice-box" id="outside-notice">外部元素的 notice-box</span>
    <button id="outside-button">外部按钮</button>
  </div>
</div>

<h2>断言结果</h2>
<pre id="results">PENDING</pre>

<script>
(function () {
  "use strict";

  var results = [];
  var alerts = [];
  var errors = [];

  window.alert = function (m) { alerts.push(String(m)); };
  window.onerror = function (m) { errors.push(String(m)); };

  function assertEq(name, actual, expected) {
    results.push({ name: name, ok: String(actual) === String(expected), actual: String(actual), expected: String(expected) });
  }

  function assertNot(name, actual, forbidden) {
    results.push({ name: name, ok: String(actual) !== String(forbidden), actual: String(actual), expected: "≠ " + forbidden });
  }

  function assertTrue(name, ok, detail) {
    results.push({ name: name, ok: !!ok, actual: detail === undefined ? "" : String(detail), expected: "true" });
  }

  var scope = document.getElementById("announcement");
  var baseline = document.getElementById("baseline");

  // A 段：把公告内容克隆到作用域之外。
  Array.prototype.slice.call(scope.children).forEach(function (node) {
    if (node.tagName === "STYLE") { return; }
    baseline.appendChild(node.cloneNode(true));
  });

  var cs = function (el) { return getComputedStyle(el); };
  var card = scope.querySelector(".announcement-card");
  var notice = scope.querySelector(".notice-box");
  var stopBadge = scope.querySelector(".status-stop");
  var normalBadge = scope.querySelector(".status-normal");
  var listItem = scope.querySelector(".detail-list li");
  var footer = scope.querySelector(".footer");

  // ---- B 段：作者写的样式必须还在 ----
  assertEq("容器 display", cs(scope).display, "flex");
  assertEq("容器 justify-content", cs(scope).justifyContent, "center");
  assertEq("容器 background-color", cs(scope).backgroundColor, "rgb(244, 246, 248)");
  assertEq("容器 min-height（100vh 已丢弃）", cs(scope).minHeight, "0px");
  assertTrue("容器高度未覆盖整屏", scope.getBoundingClientRect().height < window.innerHeight * 0.9,
    scope.getBoundingClientRect().height + "px / 视口 " + window.innerHeight + "px");

  assertEq("卡片 background-color", cs(card).backgroundColor, "rgb(255, 255, 255)");
  assertEq("卡片 border-radius", cs(card).borderRadius, "8px");
  assertNot("卡片 box-shadow", cs(card).boxShadow, "none");
  assertEq("卡片 border-top-width", cs(card).borderTopWidth, "5px");
  assertEq("卡片 border-top-color", cs(card).borderTopColor, "rgb(0, 86, 179)");
  assertEq("卡片 max-width", cs(card).maxWidth, "600px");

  assertEq("提示框 background-color", cs(notice).backgroundColor, "rgb(255, 248, 225)");
  assertEq("提示框 border-left-width", cs(notice).borderLeftWidth, "4px");
  assertEq("提示框 border-left-color", cs(notice).borderLeftColor, "rgb(255, 193, 7)");

  assertEq("暂停徽章 color", cs(stopBadge).color, "rgb(198, 40, 40)");
  assertEq("暂停徽章 background-color", cs(stopBadge).backgroundColor, "rgb(255, 235, 238)");
  assertEq("正常徽章 color", cs(normalBadge).color, "rgb(46, 125, 50)");
  assertEq("正常徽章 background-color", cs(normalBadge).backgroundColor, "rgb(232, 245, 233)");

  assertEq("列表项 display", cs(listItem).display, "flex");
  assertEq("页脚 text-align", cs(footer).textAlign, "right");

  // ---- A 段：同样的内容在容器外必须完全没有样式 ----
  var plainCard = baseline.querySelector(".announcement-card");
  assertEq("对照卡片 border-radius", cs(plainCard).borderRadius, "0px");
  assertEq("对照卡片 background-color", cs(plainCard).backgroundColor, "rgba(0, 0, 0, 0)");
  assertEq("对照卡片 border-top-width", cs(plainCard).borderTopWidth, "0px");
  var plainNotice = baseline.querySelector(".notice-box");
  assertEq("对照提示框 background-color", cs(plainNotice).backgroundColor, "rgba(0, 0, 0, 0)");

  // ---- C 段：页面其余部分未被污染 ----
  var outside = document.getElementById("outside");
  assertEq("容器外同名类 background-color", cs(outside).backgroundColor, "rgba(0, 0, 0, 0)");
  assertEq("容器外同名类 border-radius", cs(outside).borderRadius, "0px");
  assertEq("容器外同名类 box-shadow", cs(outside).boxShadow, "none");
  assertEq("页面 body 背景未被改写", cs(document.body).backgroundColor, "rgb(255, 255, 255)");
  assertNot("容器外按钮未被隐藏", cs(document.getElementById("outside-button")).display, "none");

  // ---- 安全：没有脚本、没有弹窗、没有报错 ----
  assertEq("公告内 script 元素数量", scope.querySelectorAll("script").length, 0);
  assertEq("公告内 on* 事件属性数量", scope.querySelectorAll("[onclick],[onerror],[onload],[onmouseover]").length, 0);
  assertEq("alert 调用次数", alerts.length, 0);
  assertEq("脚本错误数量", errors.length, 0);

  // ---- 样式文本本身：每条选择器都必须带作用域前缀 ----
  var styleEl = scope.querySelector("style");
  assertTrue("公告内含 style 元素", !!styleEl);
  var unscoped = [];
  var totalRules = 0;
  function checkRules(rules) {
    for (var i = 0; i < rules.length; i++) {
      var rule = rules[i];
      if (rule.selectorText !== undefined) {
        totalRules += 1;
        var parts = rule.selectorText.split(",");
        for (var j = 0; j < parts.length; j++) {
          if (parts[j].indexOf("[data-cr-announcement]") !== 0) { unscoped.push(parts[j]); }
        }
      } else if (rule.cssRules !== undefined) {
        checkRules(rule.cssRules);
      }
    }
  }
  if (styleEl) {
    var sheet = new CSSStyleSheet();
    sheet.replaceSync(styleEl.textContent);
    checkRules(sheet.cssRules);
  }
  assertTrue("样式规则数量 > 0", totalRules > 0, totalRules);
  assertEq("未加作用域前缀的选择器数量", unscoped.length, 0);
  if (unscoped.length) { results[results.length - 1].actual = unscoped.join(" | "); }

  // ---- 输出 ----
  var failed = results.filter(function (r) { return !r.ok; });
  var lines = results.map(function (r) {
    if (r.ok) { return "ASSERT PASS  " + r.name; }
    return "ASSERT FAIL  " + r.name + "  实际=" + r.actual + "  期望=" + r.expected;
  });
  lines.push("");
  lines.push("SUMMARY pass=" + (results.length - failed.length) + " fail=" + failed.length);
  document.getElementById("results").textContent = lines.join("\n");
  document.title = (failed.length === 0 ? "PASS" : "FAIL") + " 公告受控 CSS 作用域验证";
  if (window.console) { window.console.log(lines.join("\n")); }
})();
</script>
</body>
</html>
`

// TestGenerateVerificationPage regenerates the committed browser-verification
// page from the current sanitiser output. It is skipped unless explicitly asked
// for, so a normal test run stays read-only.
func TestGenerateVerificationPage(t *testing.T) {
	if os.Getenv("HTMLSAFE_UPDATE_DOCS") == "" {
		t.Skip("set HTMLSAFE_UPDATE_DOCS=1 to regenerate docs/announcement-css/css-scope-verification.html")
	}

	raw, err := os.ReadFile("../../docs/announcement-css/sample.html")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	sanitized := Sanitize(string(raw))
	if sanitized == "" {
		t.Fatal("sanitised sample is empty")
	}

	page := strings.Replace(verificationPageTemplate, "{{SANITIZED}}", sanitized, 1)
	if err := os.WriteFile("../../docs/announcement-css/css-scope-verification.html", []byte(page), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
}
