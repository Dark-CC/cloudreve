package openlist

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

const (
	// RefreshMargin triggers a fresh login this long before the token expires.
	// OpenList issues a 48 hour JWT without any refresh token, so the only way
	// to stay usable is to log in again with the stored credentials.
	RefreshMargin = 30 * time.Minute
)

// jwtExpiry returns the "exp" claim of a JWT, or the zero time when the token is
// not a JWT or carries no expiry. The token is never verified: it comes from the
// instance we just logged in to, and only its expiry matters here.
func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some tokens are issued with the standard alphabet and padding.
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return time.Time{}
		}
	}

	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}
	}

	return time.Unix(claims.Exp, 0)
}

// tokenNeedsRenewal reports whether a token with the given expiry must be
// replaced now.
func tokenNeedsRenewal(exp time.Time, now time.Time) bool {
	if exp.IsZero() {
		// No expiry could be read: let OpenList decide instead of logging in on
		// every single request.
		return false
	}

	return exp.Sub(now) <= RefreshMargin
}
