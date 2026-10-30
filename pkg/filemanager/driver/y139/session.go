package y139

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

const randomAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// randomString returns a cryptographically random alphanumeric string.
func randomString(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand never fails in practice; fall back to a timestamp so the
		// signature stays unique rather than panicking.
		for i := range buf {
			buf[i] = randomAlphabet[int(time.Now().UnixNano()+int64(i))%len(randomAlphabet)]
		}
		return string(buf)
	}

	for i := range buf {
		buf[i] = randomAlphabet[int(buf[i])%len(randomAlphabet)]
	}
	return string(buf)
}

// personalCloudHost returns the host that serves this account's personal drive,
// resolving it through the drive's route policy the first time it is needed.
// Users may pin a host in the policy to skip the lookup entirely.
func (c *client) personalCloudHost(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.resolved {
		host := c.personalHost
		c.mu.Unlock()
		return host, nil
	}
	c.mu.Unlock()

	c.mu.Lock()
	if c.resolved {
		host := c.personalHost
		c.mu.Unlock()
		return host, nil
	}

	// Renew while locked, but hand the result to the policy store only after
	// releasing c.mu: the persisting callback goes through the policy client,
	// which may rebuild this driver and re-enter the very same lock.
	before := c.authorization
	if err := c.refreshTokenLocked(ctx); err != nil {
		reporter := c.reportRefreshError
		c.mu.Unlock()
		if reporter != nil {
			kind, msg := classifyRefreshError(err)
			c.report(ctx, kind, msg)
		}
		return "", err
	}

	// The value handed to the store keeps the shape the administrator pasted,
	// while the comparison above stays on the raw payload.
	auth := c.authorizationHeader()
	save := c.persistToken
	rotated := c.authorization != before
	if rotated {
		c.refreshNotBefore = time.Now().Add(refreshCooldown)
	}

	host, err := c.queryRoutePolicyLocked(ctx)
	if err != nil {
		// A pinned server is the operator's explicit override, a failure to
		// resolve the route must not break it.
		if strings.TrimSpace(c.policy.Server) != "" {
			c.l.Warning("Failed to query the 139 route policy, keeping the configured host %q: %s",
				c.personalHost, err)
			c.resolved = true
			resolvedHost := c.personalHost
			c.mu.Unlock()
			if rotated {
				c.persist(ctx, save, auth)
			}
			return resolvedHost, nil
		}
		c.mu.Unlock()
		if rotated {
			c.persist(ctx, save, auth)
		}
		return "", err
	}

	c.personalHost = strings.TrimSuffix(host, "/")
	c.resolved = true
	resolvedHost := c.personalHost
	c.mu.Unlock()

	// A renewed credential is written back before this request returns, so the
	// value that made it into the policy is the one this process is using.
	if rotated {
		c.persist(ctx, save, auth)
	}

	c.l.Debug("139 personal drive host resolved to %q.", resolvedHost)

	return resolvedHost, nil
}

// routePolicyRequest asks the drive which host serves the personal cloud.
type routePolicyRequest struct {
	UserInfo struct {
		UserType    int    `json:"userType"`
		AccountType int    `json:"accountType"`
		AccountName string `json:"accountName"`
	} `json:"userInfo"`
	ModAddrType int `json:"modAddrType"`
}

type routePolicyResponse struct {
	RoutePolicyList []struct {
		SiteID      string `json:"siteID"`
		ModName     string `json:"modName"`
		HttpUrl     string `json:"httpUrl"`
		HttpsUrl    string `json:"httpsUrl"`
		ModAddrType int    `json:"modAddrType"`
	} `json:"routePolicyList"`
}

// queryRoutePolicyLocked resolves the personal host for this account.
func (c *client) queryRoutePolicyLocked(ctx context.Context) (string, error) {
	var payload routePolicyRequest
	payload.UserInfo.UserType = 1
	payload.UserInfo.AccountType = 1
	payload.UserInfo.AccountName = c.account
	payload.ModAddrType = 1

	var res routePolicyResponse
	if err := c.post(ctx, RouteServer+apiRoutePolicy, &payload, &res); err != nil {
		return "", err
	}

	for _, item := range res.RoutePolicyList {
		if item.ModName == "personal" && item.HttpsUrl != "" {
			return item.HttpsUrl, nil
		}
	}

	return "", fmt.Errorf("cloud drive did not return a personal drive host")
}

// refreshTokenResponse is the XML answer of the token refresh endpoint.
type refreshTokenResponse struct {
	XMLName xml.Name `xml:"root"`
	Return  string   `xml:"return"`
	Token   string   `xml:"token"`
	Desc    string   `xml:"desc"`
}

// refreshToken rotates the login token when it is close to expiry. The rotated
// token is saved back into the policy outside the lock, because the persisting
// callback touches the policy store which may re-enter this client.
func (c *client) refreshToken(ctx context.Context) error {
	save, auth, rotated, err := c.refreshAndCollect(ctx)
	if err != nil {
		return err
	}

	if rotated {
		c.persist(ctx, save, auth)
	}

	return nil
}

// persist hands the renewed credential to the policy store. A failure is
// logged and swallowed on purpose: the drive is still usable with the token in
// memory until the process restarts, and a bookkeeping error must not fail the
// user's request. The failure is still reported so the administrator can act
// before a restart throws the renewed token away.
func (c *client) persist(ctx context.Context, save func(context.Context, string) error, auth string) {
	if save == nil {
		return
	}

	if err := save(ctx, auth); err != nil {
		c.l.Warning("Refreshed the 139 login token but failed to save it back to the policy: %s", err)
		c.report(ctx, RefreshErrorKindPersistFailed, fmt.Sprintf(
			"the renewed credential could not be saved: %s", err))
	}
}

// report tells the policy store about a renewal problem. Failures of the report
// itself are only logged: surfacing an error must never break the request it is
// describing.
func (c *client) report(ctx context.Context, kind, message string) {
	if c.reportRefreshError == nil {
		return
	}

	if err := c.reportRefreshError(ctx, kind, message); err != nil {
		c.l.Warning("Failed to record the 139 credential renewal status: %s", err)
	}
}

// classifyRefreshError maps a renewal error to the kind shown to the
// administrator.
func classifyRefreshError(err error) (string, string) {
	if err == nil {
		return "", ""
	}

	if errors.Is(err, ErrAuthExpired) {
		return RefreshErrorKindExpired, "the login token expired and could not be renewed, paste a fresh Authorization header"
	}

	return RefreshErrorKindRefreshFailed, fmt.Sprintf("renewing the login token failed: %s", err)
}

// refreshAndCollect runs the refresh under the lock and reports what happened.
// The throttle (refreshNotBefore) keeps concurrent requests that cross the
// renewal window together from each calling the endpoint and overwriting one
// another's result; only the caller that actually rotated the token is told to
// persist it.
func (c *client) refreshAndCollect(ctx context.Context) (func(context.Context, string) error, string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	before := c.authorization
	if err := c.refreshTokenLocked(ctx); err != nil {
		return c.persistToken, c.authorization, false, err
	}

	rotated := c.authorization != before
	if rotated {
		c.refreshNotBefore = time.Now().Add(refreshCooldown)
	}

	return c.persistToken, c.authorizationHeader(), rotated, nil
}

// refreshTokenLocked implements the refresh; the caller must hold c.mu.
func (c *client) refreshTokenLocked(ctx context.Context) error {
	if c.authorization == "" {
		return ErrAuthExpired
	}

	decoded, err := base64.StdEncoding.DecodeString(c.authorization)
	if err != nil {
		return fmt.Errorf("%w: authorization is not valid base64", ErrAuthExpired)
	}

	segments := strings.Split(string(decoded), ":")
	if len(segments) < 3 {
		return fmt.Errorf("%w: authorization is malformed", ErrAuthExpired)
	}

	c.account = segments[1]
	token := segments[2]

	fields := strings.Split(token, "|")
	if len(fields) < 4 {
		// The token carries no expiry, leave it alone and let the drive decide.
		return nil
	}

	expiryMs, err := parseInt64(fields[3])
	if err != nil {
		return nil
	}

	remain := time.Until(time.UnixMilli(expiryMs))
	if remain > refreshMargin {
		return nil
	}

	// Another request already renewed the token a moment ago. The drive issues
	// tokens valid for about a month, so skipping a refresh inside the cooldown
	// is always safe and avoids a burst of competing refresh calls.
	if !c.refreshNotBefore.IsZero() && time.Now().Before(c.refreshNotBefore) {
		return nil
	}

	if remain < 0 {
		return fmt.Errorf("%w: the login token has expired", ErrAuthExpired)
	}

	// The refresh endpoint speaks XML, not the JSON envelope.
	body := "<root><token>" + token + "</token><account>" + segments[1] +
		"</account><clienttype>" + refreshClientTyp + "</clienttype></root>"

	header := http.Header{}
	header.Set("Content-Type", "application/xml")
	header.Set("Authorization", "Basic "+c.authorization)

	res := c.http.Request(http.MethodPost, RefreshServer+apiRefreshToken, strings.NewReader(body),
		request.WithContext(ctx),
		request.WithHeader(header),
	)
	if res.Err != nil {
		return fmt.Errorf("failed to refresh the 139 login token: %w", res.Err)
	}

	raw, err := res.GetResponse()
	if err != nil {
		return fmt.Errorf("failed to read the 139 token refresh response: %w", err)
	}

	var refreshed refreshTokenResponse
	if err := xml.Unmarshal([]byte(raw), &refreshed); err != nil {
		return fmt.Errorf("failed to decode the 139 token refresh response: %w", err)
	}

	if refreshed.Return != "0" || refreshed.Token == "" {
		return fmt.Errorf("%w: token refresh was rejected (%s)", ErrAuthExpired, strings.TrimSpace(refreshed.Desc))
	}

	c.authorization = base64.StdEncoding.EncodeToString(
		[]byte(segments[0] + ":" + segments[1] + ":" + refreshed.Token))
	c.l.Debug("139 login token refreshed, new token %s.", redact(c.authorization))

	return nil
}

func parseInt64(s string) (int64, error) {
	var out int64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &out)
	return out, err
}
