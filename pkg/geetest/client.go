// Package geetest verifies GeeTest v4 (行为验证 4.0) captcha tickets via
// the official secondary validation API (/v4/validate.php).
package geetest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

const (
	// genTimeTolerance bounds how old a solve may be before local rejection.
	// GeeTest's server already single-uses pass tokens; this is an additional
	// replay guard against grossly stale submissions and server clock skew.
	genTimeTolerance = 24 * time.Hour
)

// validateEndpoints are the official endpoints, tried in order. Overridden
// in tests.
//
// The host list mirrors Config.apiServers inside the official gt4.js SDK
// (gcaptcha4.geetest.com / gcaptcha4.geevisit.com), and the path is the
// server-side secondary validation API documented for GeeTest v4. The older
// api2.geetest.com host no longer resolves and api.geetest.com is fronted by
// an EdgeOne WAF that answers /v4/validate.php with a bare 567 interception
// page, so neither can be used for validation.
var validateEndpoints = []string{
	"https://gcaptcha4.geetest.com/validate",
	"https://gcaptcha4.geevisit.com/validate",
}

// ValidateParams holds the fields yielded by the gt4.js widget on success.
type ValidateParams struct {
	LotNumber     string
	CaptchaOutput string
	PassToken     string
	GenTime       int64
}

type validateResponse struct {
	Status string `json:"status"`
	Result string `json:"result"`
	Reason string `json:"reason"`
	Code   int    `json:"code"`
	Msg    string `json:"msg"`
}

// Verify submits a widget result to GeeTest's secondary validation API.
// It returns true only when GeeTest explicitly reports a successful match.
func Verify(ctx context.Context, client request.Client, cfg *setting.GeeTest, p *ValidateParams) (bool, error) {
	if cfg.CaptchaID == "" || cfg.CaptchaKey == "" {
		return false, fmt.Errorf("missing captcha_id or captcha_key")
	}

	if p.LotNumber == "" || p.CaptchaOutput == "" || p.PassToken == "" || p.GenTime == 0 {
		return false, fmt.Errorf("incomplete captcha result")
	}

	// gen_time freshness guard.
	if diff := time.Since(time.Unix(p.GenTime, 0)); diff > genTimeTolerance || diff < -genTimeTolerance {
		return false, fmt.Errorf("captcha result expired: gen_time %d", p.GenTime)
	}

	// Per the official server-side integration guide, the request is a
	// form-encoded POST carrying the widget result plus sign_token, the
	// HMAC-SHA256 hex digest of lot_number keyed by captcha_key.
	form := url.Values{}
	form.Set("lot_number", p.LotNumber)
	form.Set("captcha_output", p.CaptchaOutput)
	form.Set("pass_token", p.PassToken)
	form.Set("gen_time", strconv.FormatInt(p.GenTime, 10))
	form.Set("sign_token", signToken(cfg.CaptchaKey, p.LotNumber))

	var lastErr error
	for _, endpoint := range validateEndpoints {
		target := endpoint + "?captcha_id=" + url.QueryEscape(cfg.CaptchaID)
		res, err := client.Request(
			"POST",
			target,
			strings.NewReader(form.Encode()),
			request.WithContext(ctx),
			request.WithHeader(http.Header{
				"Content-Type": []string{"application/x-www-form-urlencoded"},
			}),
		).CheckHTTPResponse(http.StatusOK).GetResponse()
		if err != nil {
			lastErr = err
			continue
		}

		// A WAF/CDN interception page is not a GeeTest verdict: treat it as an
		// endpoint failure so the next official endpoint gets a chance. Without
		// this the caller cannot tell "GeeTest rejected the ticket" apart from
		// "the endpoint was blocked", and reports a bogus captcha mismatch.
		if !looksLikeJSON(res) {
			lastErr = fmt.Errorf("endpoint %s returned a non-JSON response of %d bytes (likely blocked by an upstream WAF/CDN)",
				endpoint, len(res))
			continue
		}

		var parsed validateResponse
		if err := json.Unmarshal([]byte(res), &parsed); err != nil {
			lastErr = fmt.Errorf("failed to parse captcha result: %w", err)
			continue
		}

		switch {
		case parsed.Status == "success":
			return parsed.Result == "success", nil
		case parsed.Status == "error":
			// API-level error (bad sign token, unknown captcha_id, ...).
			return false, fmt.Errorf("captcha api error %d: %s", parsed.Code, parsed.Msg)
		default:
			return false, fmt.Errorf("unexpected captcha api response: %s", strings.TrimSpace(string(res)))
		}
	}

	return false, lastErr
}

// looksLikeJSON reports whether the response body starts with a JSON object,
// i.e. whether it can plausibly be a GeeTest API verdict rather than an HTML
// error page injected by an upstream WAF or CDN.
func looksLikeJSON(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "{")
}

// signToken builds the sign_token form field: the HMAC-SHA256 hex digest of
// lot_number, keyed by captcha_key, as specified by the GeeTest v4
// server-side validation guide.
func signToken(key, lotNumber string) string {
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "%s", lotNumber)
	return hex.EncodeToString(mac.Sum(nil))
}
