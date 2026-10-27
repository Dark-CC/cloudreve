package updatecheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"4.19.1", "4.19.2", true},
		{"v4.19.1", "v4.19.2", true},
		{"4.19.2", "4.19.2", false},
		{"4.19.2", "4.19.1", false},
		{"4.19.2", "4.20.0", true},
		{"4.19.9", "4.20.0", true},
		{"4.19.9", "5.0.0", true},
		{"4.19.2-rc1", "4.19.2", true},
		{"4.19.2", "4.19.2-rc1", false},
		{"5.0.0", "4.99.9", false},
		{"4.19", "4.19.1", true},
	}
	for _, c := range cases {
		if got := IsNewer(c.current, c.latest); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestLatestReleaseNoServerRelease(t *testing.T) {
	// Repository hosts only non-server tag families; LatestRelease must
	// return ErrNoRelease (and never cache the outcome).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"tag_name": "desktop-v1.2.3", "html_url": "u", "body": "b"},
			{"tag_name": "android-v2.0.0", "html_url": "u", "body": "b"}
		]`))
	}))
	defer ts.Close()
	orig := releaseAPI
	releaseAPI = ts.URL
	defer func() { releaseAPI = orig }()

	rel, err := LatestRelease(context.Background(), nil)
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("LatestRelease() error = %v, want ErrNoRelease", err)
	}
	if rel != nil {
		t.Fatalf("LatestRelease() = %v, want nil", rel)
	}
}
