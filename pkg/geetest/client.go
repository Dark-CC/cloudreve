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
var validateEndpoints = []string{
	"https://api2.geetest.com/v4/validate.php",
	"https://api.geetest.com/v4/validate.php",
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

		var parsed validateResponse
		if err := json.Unmarshal([]byte(res), &parsed); err != nil {
			return false, fmt.Errorf("failed to parse captcha result: %w", err)
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

// signToken builds the sign_token form field: the HMAC-SHA256 hex digest of
// lot_number, keyed by captcha_key, as specified by the GeeTest v4
// server-side validation guide.
func signToken(key, lotNumber string) string {
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "%s", lotNumber)
	return hex.EncodeToString(mac.Sum(nil))
}
