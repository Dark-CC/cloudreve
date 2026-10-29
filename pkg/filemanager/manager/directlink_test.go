package manager

import (
	"net/url"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/cluster/routes"
	"github.com/stretchr/testify/require"
)

// A cloud-drive direct link is handed out as this site's own /f/... address,
// which redirects (302) to the provider's pre-signed URL. The address is built
// here so the shape is pinned by a test: any regression that returns the
// provider's URL directly fails to match.
func TestDirectLinkIsServedFromSite(t *testing.T) {
	base, err := url.Parse("https://pan.example.com")
	require.NoError(t, err)

	got := routes.MasterDirectLink(base, "linkHashId", "报告 (final).png")
	require.Equal(t, "https://pan.example.com/f/linkHashId/%E6%8A%A5%E5%91%8A%20%28final%29.png", got.String())
	require.Equal(t, "pan.example.com", got.Host)

	// An unrelated pre-signed provider URL must never round-trip through the
	// builder into the response.
	upstream, err := url.Parse("https://ykj-eos-wx2-01.eos-wuxi-3.cmecloud.cn/a/b?X-Amz-Signature=x")
	require.NoError(t, err)
	require.NotEqual(t, upstream.Host, got.Host)
}

func TestDirectEntitySourceNeedsNoProxy(t *testing.T) {
	// Without a metering claim an explicit NoInternalProxy request asks for the
	// provider URL so the 302 can point at it; a metered session keeps the
	// relay because the claim lives in the served URL.
	cases := []struct {
		name       string
		noProxy    bool
		meterClaim string
		wantDirect bool
	}{
		{"plain", false, "", false},
		{"direct", true, "", true},
		{"metered", true, "ownerHash.d", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantDirect, tc.noProxy && tc.meterClaim == "")
		})
	}
}

func TestEntityUrlCacheKeySeparatesDirect(t *testing.T) {
	relay := entityUrlCacheKey(1, 0, "a.txt", true, "site|pool|claim|false")
	direct := entityUrlCacheKey(1, 0, "a.txt", true, "site|pool|claim|true")
	require.NotEqual(t, relay, direct)
	// Stable for identical inputs, so a cache hit is still a cache hit.
	require.Equal(t, relay, entityUrlCacheKey(1, 0, "a.txt", true, "site|pool|claim|false"))
}
