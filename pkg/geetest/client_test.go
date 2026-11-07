package geetest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/stretchr/testify/require"
)

func testParams() *ValidateParams {
	return &ValidateParams{
		LotNumber:     "lot",
		CaptchaOutput: "output",
		PassToken:     "pass",
		GenTime:       time.Now().Unix(),
	}
}

func TestSignTokenDeterministic(t *testing.T) {
	a := signToken("key", "lot")
	b := signToken("key", "lot")

	require.Len(t, a, 64)
	require.Equal(t, a, b)
	require.NotEqual(t, a, signToken("key2", "lot"))
	require.NotEqual(t, a, signToken("key", "lot2"))
	// Matches the reference implementation from GeeTest's official demos:
	// hex(HMAC-SHA256(key, lot_number)).
	mac := hmac.New(sha256.New, []byte("key"))
	mac.Write([]byte("lot"))
	require.Equal(t, hex.EncodeToString(mac.Sum(nil)), a)
}

func TestVerifyValidation(t *testing.T) {
	cases := []struct {
		name   string
		cfg    *setting.GeeTest
		params *ValidateParams
	}{
		{"missing config", &setting.GeeTest{}, testParams()},
		{"missing lot", &setting.GeeTest{CaptchaID: "id", CaptchaKey: "key"},
			&ValidateParams{CaptchaOutput: "o", PassToken: "p", GenTime: time.Now().Unix()}},
		{"expired gen_time", &setting.GeeTest{CaptchaID: "id", CaptchaKey: "key"},
			&ValidateParams{LotNumber: "l", CaptchaOutput: "o", PassToken: "p", GenTime: time.Now().Add(-48 * time.Hour).Unix()}},
		{"future gen_time", &setting.GeeTest{CaptchaID: "id", CaptchaKey: "key"},
			&ValidateParams{LotNumber: "l", CaptchaOutput: "o", PassToken: "p", GenTime: time.Now().Add(48 * time.Hour).Unix()}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, err := Verify(t.Context(), nil, c.cfg, c.params)
			require.Error(t, err)
			require.False(t, ok)
		})
	}
}

func TestVerifyAgainstMockAPI(t *testing.T) {
	var gotQuery, gotBody string
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("captcha_id")
		require.NoError(t, r.ParseForm())
		gotForm = r.PostForm
		gotBody = r.PostForm.Encode()
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"success","result":"success","reason":""}`))
	}))
	defer srv.Close()

	old := validateEndpoints
	validateEndpoints = []string{srv.URL}
	defer func() { validateEndpoints = old }()

	client := request.NewClient(nil, request.WithEndpoint(srv.URL))
	cfg := &setting.GeeTest{CaptchaID: "cid", CaptchaKey: "ckey"}
	p := testParams()

	ok, err := Verify(t.Context(), client, cfg, p)
	require.NoError(t, err)
	require.True(t, ok)

	require.Equal(t, "cid", gotQuery)
	// Form-encoded body carrying the widget result and the signature.
	require.Contains(t, gotBody, "lot_number=lot")
	require.Contains(t, gotBody, "captcha_output=output")
	require.Contains(t, gotBody, "pass_token=pass")
	require.Contains(t, gotBody, "sign_token=")
	require.Equal(t, signToken("ckey", "lot"), gotForm["sign_token"][0])
	require.NotEmpty(t, gotForm["gen_time"][0])
}

func TestVerifyAPIResults(t *testing.T) {
	cases := []struct {
		name   string
		resp   string
		ok     bool
		hasErr bool
	}{
		{"success", `{"status":"success","result":"success"}`, true, false},
		{"mismatch", `{"status":"success","result":"fail","reason":"token already used"}`, false, false},
		{"api error", `{"status":"error","code":-642050,"msg":"sign token error"}`, false, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				_, _ = w.Write([]byte(c.resp))
			}))
			defer srv.Close()

			old := validateEndpoints
			validateEndpoints = []string{srv.URL}
			defer func() { validateEndpoints = old }()

			client := request.NewClient(nil, request.WithEndpoint(srv.URL))
			ok, err := Verify(t.Context(), client, &setting.GeeTest{CaptchaID: "cid", CaptchaKey: "ckey"}, testParams())
			require.Equal(t, c.ok, ok)
			if c.hasErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestVerifyFallsBackToSecondEndpoint(t *testing.T) {
	var firstCalled, secondCalled bool
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalled = true
		w.WriteHeader(502)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalled = true
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"success","result":"success"}`))
	}))
	defer second.Close()

	old := validateEndpoints
	validateEndpoints = []string{first.URL, second.URL}
	defer func() { validateEndpoints = old }()

	client := request.NewClient(nil)
	ok, err := Verify(t.Context(), client, &setting.GeeTest{CaptchaID: "cid", CaptchaKey: "ckey"}, testParams())
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, firstCalled)
	require.True(t, secondCalled)
}

func TestVerifyAllEndpointsDown(t *testing.T) {
	old := validateEndpoints
	validateEndpoints = []string{"http://127.0.0.1:1/validate", "http://127.0.0.1:2/validate"}
	defer func() { validateEndpoints = old }()

	client := request.NewClient(nil)
	ok, err := Verify(t.Context(), client, &setting.GeeTest{CaptchaID: "cid", CaptchaKey: "ckey"}, testParams())
	require.Error(t, err)
	require.False(t, ok)
	require.True(t, strings.Contains(err.Error(), "127.0.0.1"))
}

// TestDefaultEndpointsAreOfficialGuards against a regression to the retired
// api2.geetest.com host or to an endpoint that a WAF answers with HTML.
func TestDefaultEndpointsAreOfficial(t *testing.T) {
	require.NotEmpty(t, validateEndpoints)
	for _, ep := range validateEndpoints {
		require.True(t, strings.HasPrefix(ep, "https://gcaptcha4."), "unexpected endpoint %s", ep)
		require.True(t, strings.HasSuffix(ep, "/validate"), "unexpected path in endpoint %s", ep)
	}
}

// TestVerifySkipsWAFInterceptionPage covers the production failure mode: an
// upstream WAF answers with 200 and an HTML block page instead of GeeTest's
// JSON verdict. That must not be reported as a captcha mismatch; the next
// endpoint has to be tried, and if all are blocked the caller sees an error.
func TestVerifySkipsWAFInterceptionPage(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<!doctype html><html><body>Restricted Access</body></html>"))
	}))
	defer blocked.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"success","result":"success"}`))
	}))
	defer good.Close()

	old := validateEndpoints
	defer func() { validateEndpoints = old }()
	client := request.NewClient(nil)
	cfg := &setting.GeeTest{CaptchaID: "cid", CaptchaKey: "ckey"}

	// Blocked endpoint first: the good one still decides the result.
	validateEndpoints = []string{blocked.URL, good.URL}
	ok, err := Verify(t.Context(), client, cfg, testParams())
	require.NoError(t, err)
	require.True(t, ok)

	// Only blocked endpoints: surface an error rather than a silent mismatch.
	validateEndpoints = []string{blocked.URL}
	ok, err = Verify(t.Context(), client, cfg, testParams())
	require.Error(t, err)
	require.Contains(t, err.Error(), "WAF")
	require.False(t, ok)
}
