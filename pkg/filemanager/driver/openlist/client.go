package openlist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

// OpenList API endpoints. OpenList publishes no OpenAPI/Swagger document, so
// the contract below is maintained by hand against the version tested in
// docs/openlist-api-feasibility.md (v4.2.9). Keep every path here.
const (
	apiLogin = "/api/auth/login"
	apiList  = "/api/fs/list"
	apiGet   = "/api/fs/get"

	// listPageSize is the page size requested from /api/fs/list.
	listPageSize = 200

	// redactedPrefixLen is how many leading characters of a secret may be logged.
	redactedPrefixLen = 4

	// httpCallTimeout bounds a single API request so a hung OpenList cannot
	// pin a Cloudreve request forever.
	httpCallTimeout = 60 * time.Second
)

// Refresh failure kinds surfaced to the administrator dashboard, mirroring the
// 139 driver so both land in the same notice list.
const (
	// RefreshErrorKindLoginFailed OpenList refused the stored username/password.
	RefreshErrorKindLoginFailed = "login_failed"
)

var (
	// ErrAuthExpired the credentials were rejected, or the token is no longer
	// accepted and logging in again failed.
	ErrAuthExpired = errors.New("openlist rejected the stored credentials")
	// ErrFileNotFound the requested path does not exist on the mounted storage.
	ErrFileNotFound = errors.New("file not found")
	// ErrStorageNotFound the mount path of the policy does not exist (or was
	// removed) on that OpenList instance.
	ErrStorageNotFound = errors.New("openlist storage not found")
	// ErrPathEscaped OpenList refused the path outright.
	ErrPathEscaped = errors.New("openlist refused the requested path")
	// ErrRateLimited OpenList throttled the sign-in attempts; waiting clears it.
	ErrRateLimited = errors.New("openlist is throttling sign-in attempts, try again in a few minutes")
	// ErrEmptyBody OpenList answered without a body.
	ErrEmptyBody = errors.New("empty response body from openlist")
)

// Client talks to one OpenList instance using the credentials of the account
// the administrator created there.
type Client interface {
	// Login obtains a fresh token and keeps it for this client.
	Login(ctx context.Context) error
	// Ping verifies the mount path resolves to a readable directory.
	Ping(ctx context.Context) error
	// ListChildren lists one directory (page through the whole listing).
	ListChildren(ctx context.Context, dir string) ([]FileInfo, error)
	// GetFileInfo returns the metadata of one entry, including its raw URL.
	GetFileInfo(ctx context.Context, p string) (*FileInfo, error)
	// MountPath returns the configured anchor prefix without surrounding slashes.
	MountPath() string
}

type client struct {
	policy  *ent.StoragePolicy
	http    request.Client
	l       logging.Logger
	baseURL string
	mount   string
	user    string
	pass    string

	// reportRefreshError, when set, tells the policy store that a renewal
	// failed so the administrator sees it in the dashboard. An empty kind
	// clears a previous notice.
	reportRefreshError func(ctx context.Context, kind, message string) error

	token    string
	tokenExp time.Time
}

// NewClient builds a client from an existing storage policy.
func NewClient(policy *ent.StoragePolicy, config conf.ConfigProvider, l logging.Logger) Client {
	base := strings.TrimSuffix(strings.TrimSpace(policy.Server), "/")
	if base == "" {
		base = DefaultServer
	}

	return &client{
		policy:  policy,
		http:    request.NewClient(config, request.WithLogger(l)),
		l:       l,
		baseURL: base,
		mount:   normalizePath(policyMountPath(policy)),
		user:    strings.TrimSpace(policy.AccessKey),
		pass:    policy.SecretKey,
	}
}

// DefaultServer is only a last resort: a policy without a server cannot talk to
// a real instance, but the constructor must not fail before the administrator
// has typed one in.
const DefaultServer = "http://127.0.0.1:5244"

// MountPath returns the anchor prefix, normalized.
func (c *client) MountPath() string {
	return c.mount
}

// SetRefreshErrorReporter installs the callback used to surface a failed
// credential renewal to the administrator.
func SetRefreshErrorReporterFn(d Client, fn func(ctx context.Context, kind, message string) error) {
	if c, ok := d.(*client); ok {
		c.reportRefreshError = fn
	}
}

// apiEnvelope is the response wrapper of every OpenList API endpoint.
//
// OpenList reports business failures inside an HTTP 200 response and only the
// `code` field tells success from failure, so this type is what decides.
type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// loginRequest is the payload of /api/auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse is the data of /api/auth/login.
type loginResponse struct {
	Token string `json:"token"`
}

// Login authenticates with the stored username and password and keeps the
// resulting token in memory (and in the policy).
func (c *client) Login(ctx context.Context) error {
	if c.user == "" || c.pass == "" {
		return fmt.Errorf("%w: the policy holds no OpenList username or password", ErrAuthExpired)
	}

	body, err := json.Marshal(&loginRequest{Username: c.user, Password: c.pass})
	if err != nil {
		return fmt.Errorf("failed to encode the login request: %w", err)
	}

	// The login call must stay unauthenticated: routing it through post() would
	// ask tokenFor() for a token, which logs in again, and recurse forever.
	var out loginResponse
	if err := c.do(ctx, http.MethodPost, apiLogin, "", body, &out, false); err != nil {
		return err
	}

	if out.Token == "" {
		return fmt.Errorf("%w: openlist returned an empty token", ErrAuthExpired)
	}

	c.token = out.Token
	c.tokenExp = jwtExpiry(out.Token)
	c.l.Debug("OpenList logged in as %s, token %s valid until %s.",
		c.user, redact(c.user), c.tokenExp.Format(time.RFC3339))

	return nil
}

// report tells the policy store about a login problem. Failures of the report
// itself are only logged.
func (c *client) report(ctx context.Context, kind, message string) {
	if c.reportRefreshError == nil {
		return
	}

	if err := c.reportRefreshError(ctx, kind, message); err != nil {
		c.l.Warning("Failed to record the OpenList credential status: %s", err)
	}
}

// tokenFor returns the token to use for the next request, logging in when none
// is cached or the cached one is about to expire.
//
// OpenList issues a 48 hour JWT and offers no refresh token, so the only way to
// stay usable is to log in again with the stored credentials.
func (c *client) tokenFor(ctx context.Context) (string, error) {
	if c.token != "" && (c.tokenExp.IsZero() || time.Until(c.tokenExp) > RefreshMargin) {
		return c.token, nil
	}

	if err := c.Login(ctx); err != nil {
		c.report(ctx, RefreshErrorKindLoginFailed, describeLoginError(err))
		return "", err
	}

	// The credentials work again, the previous notice is stale.
	c.report(ctx, "", "")

	return c.token, nil
}

// invalidate drops the cached token so the next request logs in again.
func (c *client) invalidate() {
	c.token = ""
	c.tokenExp = time.Time{}
}

// Ping verifies the configured mount path resolves on that instance, so the
// administrator gets a readable error instead of an empty file tree.
func (c *client) Ping(ctx context.Context) error {
	if c.mount == "" {
		return nil
	}

	// ListChildren joins the mount prefix itself, so the anchor directory is
	// addressed as the mount root here.
	if _, err := c.ListChildren(ctx, ""); err != nil {
		return err
	}

	return nil
}

// get performs one authenticated GET request.
func (c *client) get(ctx context.Context, api, query string, out any) error {
	return c.do(ctx, http.MethodGet, api, query, nil, out, true)
}

// post performs one authenticated POST request with a JSON body.
func (c *client) post(ctx context.Context, api string, payload any, out any, query string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode request for %s: %w", api, err)
	}

	return c.do(ctx, http.MethodPost, api, query, body, out, true)
}

// do sends one request. When authenticated is set the token is attached and a
// 401 answer retries the call exactly once after logging in again, which is what
// makes the 48 hour token expiry invisible to the user.
func (c *client) do(ctx context.Context, method, api, query string, body []byte, out any, authenticated bool) error {
	err := c.attempt(ctx, method, api, query, body, out, authenticated)
	if err == nil || !authenticated || !errors.Is(err, ErrAuthExpired) {
		return err
	}

	c.invalidate()
	if loginErr := c.Login(ctx); loginErr != nil {
		c.report(ctx, RefreshErrorKindLoginFailed, describeLoginError(loginErr))
		return loginErr
	}

	c.report(ctx, "", "")

	return c.attempt(ctx, method, api, query, body, out, authenticated)
}

// attempt is one single HTTP round trip plus envelope decoding.
func (c *client) attempt(ctx context.Context, method, api, query string, body []byte, out any, authenticated bool) error {
	target := c.baseURL + api
	if query != "" {
		target += "?" + query
	}

	header := http.Header{}
	header.Set("Accept", "application/json")
	if body != nil {
		header.Set("Content-Type", "application/json")
	}

	if authenticated {
		token, err := c.tokenFor(ctx)
		if err != nil {
			return err
		}
		// OpenList expects the bare JWT, without a "Bearer " scheme.
		header.Set("Authorization", token)
	}

	callCtx, cancel := context.WithTimeout(ctx, httpCallTimeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	res := c.http.Request(method, target, reader,
		request.WithContext(callCtx),
		request.WithHeader(header),
	)
	if res.Err != nil {
		return fmt.Errorf("failed to request %s: %w", api, res.Err)
	}

	raw, err := res.GetResponse()
	if err != nil {
		return fmt.Errorf("failed to read response of %s: %w", api, err)
	}

	return decode(api, res.Response.StatusCode, raw, out)
}

// decode interprets the OpenList envelope.
//
// The HTTP status says almost nothing: OpenList answers 200 even for "token
// invalidated" and "object not found", and puts the outcome in `code`. Both the
// status and the body code therefore have to agree before a call is a success.
func decode(api string, statusCode int, raw string, out any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("%w (HTTP %d, %s)", ErrEmptyBody, statusCode, api)
	}

	var env apiEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return fmt.Errorf("failed to decode response of %s (HTTP %d): %w", api, statusCode, err)
	}

	// The body code is authoritative; the HTTP status only breaks a tie when
	// a non-2xx answer still carries a success-looking body.
	if env.Code != 200 {
		return mapEnvelopeError(api, statusCode, env.Code, env.Message)
	}

	if statusCode < 200 || statusCode >= 300 {
		return mapEnvelopeError(api, statusCode, env.Code, env.Message)
	}

	if out == nil || len(env.Data) == 0 {
		return nil
	}

	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("failed to decode data of %s: %w", api, err)
	}

	return nil
}

// mapEnvelopeError converts an OpenList business error into one of this
// driver's sentinels. OpenList reuses code 500 for "object not found", so the
// message has to be inspected rather than the status.
func mapEnvelopeError(api string, statusCode, code int, message string) error {
	msg := strings.TrimSpace(message)
	lower := strings.ToLower(msg)

	switch {
	// OpenList throttles repeated failed sign-ins and answers 200 with code
	// 429. It must not be read as a wrong password: the credentials may well
	// be right, so the administrator is told to wait instead.
	case code == 429 || statusCode == http.StatusTooManyRequests:
		return &APIError{Code: code, Message: msg, Err: ErrRateLimited}
	case code == 401 || statusCode == http.StatusUnauthorized:
		return &APIError{Code: code, Message: msg, Err: ErrAuthExpired}
	case strings.Contains(lower, "storage not found"):
		return &APIError{Code: code, Message: msg, Err: ErrStorageNotFound}
	case strings.Contains(lower, "object not found") || strings.Contains(lower, "not found"):
		return &APIError{Code: code, Message: msg, Err: ErrFileNotFound}
	case code == 403 && (strings.Contains(lower, "relative path") || strings.Contains(lower, "not allowed")):
		return &APIError{Code: code, Message: msg, Err: ErrPathEscaped}
	}

	return &APIError{
		Code:    code,
		Message: msg,
		Err:     fmt.Errorf("openlist rejected %s (code %d, HTTP %d)", api, code, statusCode),
	}
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

// describeLoginError turns a login failure into the sentence shown on the
// dashboard. The credentials themselves never appear here.
func describeLoginError(err error) string {
	if err == nil {
		return ""
	}

	if errors.Is(err, ErrRateLimited) {
		return "openlist is throttling sign-in attempts after too many failures, wait a few minutes"
	}

	if errors.Is(err, ErrAuthExpired) {
		return "openlist rejected the stored username or password"
	}

	return fmt.Sprintf("logging in to openlist failed: %s", err)
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
