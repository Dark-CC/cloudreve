package setting

import (
	"context"
	"strings"
	"testing"
)

// TestLocalizedSanitizesAnnouncement covers the read-side filter: whatever is
// stored, the value handed to the client must be safe HTML. This is the path
// that actually reaches the browser, so it is the one that matters most.
func TestLocalizedSanitizesAnnouncement(t *testing.T) {
	dirty := `<h2>维护通知</h2><script>alert(document.cookie)</script><p>请勿上传</p>`

	clean := sanitizeLocalized("announcement", dirty)
	if strings.Contains(strings.ToLower(clean), "<script") {
		t.Fatalf("sanitizeLocalized left a script tag: %q", clean)
	}
	if !strings.Contains(clean, "<h2>维护通知</h2>") || !strings.Contains(clean, "<p>请勿上传</p>") {
		t.Errorf("sanitizeLocalized dropped legitimate formatting: %q", clean)
	}
}

// TestLocalizedLeavesOtherSettingsAlone guards against the filter leaking onto
// settings that are not rendered as HTML (and must not be rewritten).
func TestLocalizedLeavesOtherSettingsAlone(t *testing.T) {
	raw := `<b>not html here</b>`
	if got := sanitizeLocalized("siteName", raw); got != raw {
		t.Errorf("sanitizeLocalized altered a non-HTML setting: %q", got)
	}
	if got := sanitizeLocalized("siteDes", raw); got != raw {
		t.Errorf("sanitizeLocalized altered a non-HTML setting: %q", got)
	}
}

// TestLocalizedSanitizesPlainTextAnnouncement makes sure legacy plain-text
// announcements survive untouched apart from escaping.
func TestLocalizedSanitizesPlainTextAnnouncement(t *testing.T) {
	plain := "尊敬的用户：\n美国节点预计 11月15日 后恢复下载服务。"
	got := sanitizeLocalized("announcement", plain)
	if !strings.Contains(got, "美国节点预计") {
		t.Errorf("sanitizeLocalized mangled a plain-text announcement: %q", got)
	}
}

// TestLocalizedEmptyAnnouncementStaysEmpty keeps the "no announcement" case.
func TestLocalizedEmptyAnnouncementStaysEmpty(t *testing.T) {
	if got := sanitizeLocalized("announcement", ""); got != "" {
		t.Errorf("sanitizeLocalized(%q) = %q, want empty", "", got)
	}
}

// TestLocalizedContextIsIgnored documents that the filter is applied without
// touching the context, so it cannot panic on a nil one.
func TestLocalizedNoContextPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("sanitizeLocalized panicked: %v", r)
		}
	}()
	_ = sanitizeLocalized("announcement", "<p>ok</p>")
	_ = context.Background()
}
