package openlist

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// loginRequest is the payload of /api/auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse is the data of /api/auth/login. OpenList hands back a bare JWT
// and how long it stays valid, in hours.
type loginResponse struct {
	Token         string `json:"token"`
	TokenExpireIn int    `json:"token_expires_in"`
}

// token returns a usable JWT, logging in when the cached one is missing or
// close to expiry.
//
// OpenList issues a fixed-lifetime JWT with no refresh endpoint, so renewal is
// a fresh login with the account stored on the policy. That is the whole reason
// the policy keeps the username and password instead of a pasted token: a
// pasted token would go stale after 48 hours with no way to renew it.
func (c *client) loginToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.jwt != "" && time.Until(c.expireAt) > refreshMargin {
		token := c.jwt
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	if err := c.renew(ctx); err != nil {
		return "", err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.jwt, nil
}

// renew logs in again and caches the resulting token. Concurrent callers that
// cross the renewal window together are throttled by refreshCooldown so only
// one of them logs in.
func (c *client) renew(ctx context.Context) error {
	return c.renewWithin(ctx, false)
}

// renewRejected logs in again after the instance explicitly rejected the current
// token. It skips the cooldown: the token was refused, so reusing it is
// pointless, and the cooldown only exists to stop a burst of callers that
// crossed the expiry window together from all logging in.
func (c *client) renewRejected(ctx context.Context) error {
	return c.renewWithin(ctx, true)
}

func (c *client) renewWithin(ctx context.Context, forced bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Another request already renewed a moment ago; its token is still fresh.
	if !forced && c.jwt != "" && time.Until(c.expireAt) > refreshMargin {
		return nil
	}
	if !forced && !c.refreshNotBefore.IsZero() && time.Now().Before(c.refreshNotBefore) {
		if c.jwt != "" {
			return nil
		}
	}

	if c.username == "" || c.password == "" {
		return ErrAuthExpired
	}

	res, err := c.login(ctx)
	if err != nil {
		kind, msg := classifyRefreshError(err)
		c.mu.Unlock()
		c.report(ctx, kind, msg)
		c.mu.Lock()
		return err
	}

	c.jwt = res.Token
	c.expireAt = expiryOf(res)
	c.refreshNotBefore = time.Now().Add(refreshCooldown)

	c.l.Debug("OpenList login token renewed for account %s, expires at %s.",
		redact(c.username), c.expireAt.Format(time.RFC3339))

	return nil
}

// login performs the credential exchange.
func (c *client) login(ctx context.Context) (*loginResponse, error) {
	base := c.baseURL()
	if base == "" {
		return nil, errors.New("openlist instance address is not configured")
	}

	var res loginResponse
	err := c.post(ctx, apiLogin, "", &loginRequest{
		Username: c.username,
		Password: c.password,
	}, &res)
	if err != nil {
		if errors.Is(err, ErrAuthExpired) || isCredentialRejection(err) {
			return nil, fmt.Errorf("%w: %s", ErrAuthExpired, err.Error())
		}
		return nil, err
	}

	if strings.TrimSpace(res.Token) == "" {
		return nil, fmt.Errorf("%w: the instance returned no token", ErrAuthExpired)
	}

	return &res, nil
}

// isCredentialRejection reports whether OpenList refused the account itself
// rather than failing for an unrelated reason.
func isCredentialRejection(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	msg := strings.ToLower(apiErr.Message)
	return strings.Contains(msg, "password") || strings.Contains(msg, "username") ||
		strings.Contains(msg, "incorrect") || strings.Contains(msg, "wrong")
}

// expiryOf derives when a token dies: its own `exp` claim when readable,
// otherwise OpenList's default lifetime.
func expiryOf(res *loginResponse) time.Time {
	if exp, ok := jwtExpiry(res.Token); ok {
		return exp
	}

	hours := res.TokenExpireIn
	if hours <= 0 {
		return time.Now().Add(tokenExpiresIn)
	}

	return time.Now().Add(time.Duration(hours) * time.Hour)
}

// jwtExpiry reads the `exp` claim from a JWT without verifying it. The claim is
// only used to decide when to log in again, never for trust.
func jwtExpiry(token string) (time.Time, bool) {
	segments := strings.Split(token, ".")
	if len(segments) < 2 {
		return time.Time{}, false
	}

	raw, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return time.Time{}, false
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}

	return time.Unix(claims.Exp, 0), true
}

// classifyRefreshError maps a login failure to the kind shown to the
// administrator.
func classifyRefreshError(err error) (string, string) {
	if err == nil {
		return "", ""
	}

	if errors.Is(err, ErrAuthExpired) {
		return RefreshErrorKindExpired,
			"the OpenList account could not log in, please check the username and password"
	}

	return RefreshErrorKindRefreshFailed,
		fmt.Sprintf("logging in to the OpenList instance failed: %s", err)
}
