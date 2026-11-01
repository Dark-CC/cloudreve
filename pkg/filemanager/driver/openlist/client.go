package openlist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

const (
	// apiLogin exchanges the site owner's account for a JWT. The token is a
	// bare JWT with no refresh mechanism, valid for tokenExpiresIn.
	apiLogin = "/api/auth/login"
	// apiList lists a directory.
	apiList = "/api/fs/list"
	// apiGet returns one entry including its download URL.
	apiGet = "/api/fs/get"

	// listPageSize is the page size requested from /api/fs/list.
	listPageSize = 200
	// listMaxPages guards the pagination loop against a server that never
	// reports the last page.
	listMaxPages = 10000
	// tokenExpiresIn is OpenList's default JWT lifetime (`token_expires_in`
	// is measured in hours). Only used when the token carries no readable exp.
	tokenExpiresIn = 48 * time.Hour
	// refreshMargin renews the token this long before it expires, so a request
	// never races the expiry.
	refreshMargin = 30 * time.Minute
	// refreshCooldown keeps concurrent requests that cross the renewal window
	// together from each logging in and overwriting one another's token.
	refreshCooldown = time.Minute
	// requestTimeout bounds every API call; OpenList answers local API calls in
	// milliseconds, a hanging call would otherwise pin the driver lock.
	requestTimeout = 30 * time.Second
	// redactedPrefixLen is how many leading characters of a secret may be logged.
	redactedPrefixLen = 4
)

// OpenList business error codes. They arrive inside an HTTP 200 body, so the
// driver must read `code` rather than trust the status line.
const (
	// codeAuthFailure unauthenticated: "Guest user is disabled" / "token is invalidated".
	codeAuthFailure = 401
	// codeForbidden a rejected request: relative path, name conflict and the like.
	codeForbidden = 403
	// codeNotFound the requested object or multipart session does not exist.
	codeNotFound = 404
	// codeFailure OpenList's generic failure. It is reused for "object not found"
	// and "storage not found", so it must not be read as a server error.
	codeFailure = 500
)

// Refresh failure kinds surfaced to the administrator dashboard.
const (
	// RefreshErrorKindRefreshFailed OpenList refused the login request, or it
	// could not be reached at all.
	RefreshErrorKindRefreshFailed = "refresh_failed"
	// RefreshErrorKindExpired the stored account credentials no longer log in;
	// the administrator must update them.
	RefreshErrorKindExpired = "expired"
)

var (
	// ErrAuthExpired the stored account could not be logged in.
	ErrAuthExpired = errors.New("openlist login failed, please check the account and password")
	// ErrNotSupported the requested operation is not implemented by this
	// read-only driver.
	ErrNotSupported = errors.New("not supported by the OpenList policy")
	// ErrMountNotFound the configured mount path does not exist on the instance.
	ErrMountNotFound = errors.New("openlist mount path not found")
	// ErrFileNotFound the requested path does not exist.
	ErrFileNotFound = errors.New("file not found")
	// ErrInvalidPath the path escapes the configured mount point.
	ErrInvalidPath = errors.New("invalid path")
	// ErrEmptyBody OpenList returned a response without a body.
	ErrEmptyBody = errors.New("empty response body")
)

// Client talks to one OpenList instance using the site owner's account.
type Client interface {
	// ListChildren lists every direct child of the given virtual path.
	ListChildren(ctx context.Context, dirPath string) ([]FileInfo, error)
	// Get returns the entry at the given virtual path, including its raw_url.
	Get(ctx context.Context, filePath string) (*FileInfo, error)
	// Ping verifies the mount point exists and the account can log in.
	Ping(ctx context.Context) error
	// ChunkSize is the configured chunk size; read-only policies ignore it.
	ChunkSize() int64
}

type client struct {
	policy *ent.StoragePolicy
	http   request.Client
	l      logging.Logger
	chunk  int64

	// mountPath is the OpenList virtual path this policy is anchored at, with
	// no leading or trailing slash. Empty means the instance root.
	mountPath string
	// username/password are the site owner's OpenList account. They log in
	// again every time the JWT expires: OpenList has no refresh token.
	username string
	password string

	// reportRefreshError, when set, tells the policy store that a renewal
	// failed so the administrator sees it in the dashboard. kind is one of
	// RefreshErrorKind*; an empty kind clears a previous notice.
	reportRefreshError func(ctx context.Context, kind, message string) error

	// mu guards the mutable session state below.
	mu       sync.Mutex
	jwt      string
	expireAt time.Time
	// refreshNotBefore throttles logins: once this client rotated the token, no
	// further login happens before this instant.
	refreshNotBefore time.Time
}

// NewClient builds a client from an existing storage policy. The policy's
// Server is the instance address, AccessKey the account name and SecretKey its
// password.
func NewClient(policy *ent.StoragePolicy, config conf.ConfigProvider, l logging.Logger) Client {
	chunkSize := int64(0)
	mountPath := ""
	if policy.Settings != nil {
		chunkSize = policy.Settings.ChunkSize
		mountPath = normalizePath(policy.Settings.MountPath)
	}

	return &client{
		policy:    policy,
		http:      request.NewClient(config, request.WithLogger(l)),
		l:         l,
		chunk:     chunkSize,
		mountPath: mountPath,
		username:  strings.TrimSpace(policy.AccessKey),
		password:  policy.SecretKey,
	}
}

// SetRefreshErrorReporterFn installs the callback used to surface a failed
// credential renewal to the administrator.
func SetRefreshErrorReporterFn(d Client, fn func(ctx context.Context, kind, message string) error) {
	if c, ok := d.(*client); ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.reportRefreshError = fn
	}
}

func (c *client) ChunkSize() int64 {
	return c.chunk
}

// baseURL returns the instance address without a trailing slash.
func (c *client) baseURL() string {
	return strings.TrimSuffix(strings.TrimSpace(c.policy.Server), "/")
}

// envelope is the response wrapper every OpenList endpoint uses. Success is
// signalled by a 200 `code`; anything else carries a message in `message`.
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// APIError is an error returned by the OpenList API.
type APIError struct {
	Code    int
	Message string
	Err     error
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Err.Error(), e.Message)
	}
	return e.Err.Error()
}

func (e *APIError) Unwrap() error {
	return e.Err
}

// call performs one authenticated API request against the instance. A 401 is
// retried once after logging in again, which is the whole renewal path: the
// token has a fixed lifetime and no refresh endpoint exists.
func (c *client) call(ctx context.Context, api string, payload any, out any) error {
	token, err := c.loginToken(ctx)
	if err != nil {
		return err
	}

	err = c.post(ctx, api, token, payload, out)
	if err == nil || !errors.Is(err, ErrAuthExpired) {
		return err
	}

	// The token was rejected (expired early, or the instance was restarted with
	// a new jwt_secret). Log in again once and retry.
	if err := c.renewRejected(ctx); err != nil {
		return err
	}

	token, err = c.loginToken(ctx)
	if err != nil {
		return err
	}

	return c.post(ctx, api, token, payload, out)
}

// post performs one API request with the given token.
func (c *client) post(ctx context.Context, api string, token string, payload any, out any) error {
	base := c.baseURL()
	if base == "" {
		return errors.New("openlist instance address is not configured")
	}

	var body string
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to encode request for %s: %w", api, err)
		}
		body = string(raw)
	}

	res := c.http.Request(http.MethodPost, base+api, strings.NewReader(body),
		request.WithContext(ctx),
		request.WithTimeout(requestTimeout),
		request.WithHeader(c.headers(token)),
	)
	if res.Err != nil {
		return fmt.Errorf("failed to request %s on the openlist instance: %w", api, res.Err)
	}

	respBody, err := res.GetResponse()
	if err != nil {
		return fmt.Errorf("failed to read the response of %s: %w", api, err)
	}

	return c.decode(api, respBody, res.Response.StatusCode, out)
}

// headers builds the header set every API call needs. OpenList expects the raw
// JWT: no "Bearer " prefix.
func (c *client) headers(token string) http.Header {
	h := http.Header{}
	h.Set("Accept", "application/json")
	h.Set("Content-Type", "application/json")
	if token != "" {
		h.Set("Authorization", token)
	}
	return h
}

// decode interprets the OpenList envelope. The business code, not the HTTP
// status, decides success: OpenList answers errors with HTTP 200.
func (c *client) decode(api, respBody string, statusCode int, out any) error {
	var env envelope
	if err := json.Unmarshal([]byte(respBody), &env); err != nil {
		return fmt.Errorf("failed to decode the response of %s: %w", api, err)
	}

	if env.Code == 200 {
		if out == nil || len(env.Data) == 0 {
			return nil
		}
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("failed to decode data of %s: %w", api, err)
		}
		return nil
	}

	if statusCode >= 500 && env.Code == 0 {
		// No OpenList code at all with a 5xx status means the request never
		// reached a handler (reverse proxy, instance down).
		return fmt.Errorf("openlist returned HTTP %d for %s", statusCode, api)
	}

	return mapError(api, env.Code, env.Message)
}

// mapError converts an OpenList business error into one of the driver's
// sentinels or a descriptive error carrying the original code.
func mapError(api string, code int, message string) error {
	msg := strings.TrimSpace(message)

	switch code {
	case codeAuthFailure:
		return &APIError{Code: code, Message: msg, Err: ErrAuthExpired}
	case codeForbidden:
		if strings.Contains(msg, "relative path") {
			return &APIError{Code: code, Message: msg, Err: ErrInvalidPath}
		}
		return &APIError{Code: code, Message: msg,
			Err: fmt.Errorf("openlist rejected %s (code %d)", api, code)}
	case codeNotFound:
		return &APIError{Code: code, Message: msg, Err: ErrFileNotFound}
	case codeFailure:
		// OpenList reuses 500 for "object not found" and "storage not found",
		// so the message decides which sentinel applies.
		if strings.Contains(msg, "storage not found") {
			return &APIError{Code: code, Message: msg, Err: ErrMountNotFound}
		}
		if strings.Contains(msg, "object not found") {
			return &APIError{Code: code, Message: msg, Err: ErrFileNotFound}
		}
	}

	return &APIError{Code: code, Message: msg,
		Err: fmt.Errorf("openlist rejected %s (code %d)", api, code)}
}

// report tells the policy store about a renewal problem. Failures of the report
// itself are only logged: surfacing an error must never break the request it
// describes.
func (c *client) report(ctx context.Context, kind, message string) {
	if c.reportRefreshError == nil {
		return
	}

	if err := c.reportRefreshError(ctx, kind, message); err != nil {
		c.l.Warning("Failed to record the OpenList credential status: %s", err)
	}
}

// redact keeps only a short prefix of a secret for diagnostics.
func redact(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "<empty>"
	}
	if len(secret) <= redactedPrefixLen {
		return "***"
	}
	return secret[:redactedPrefixLen] + "***"
}
