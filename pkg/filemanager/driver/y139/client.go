package y139

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/chunk/backoff"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

const (
	// DefaultServer is used when the drive's route policy cannot be queried.
	DefaultServer = "https://personal-kd-njs.yun.139.com"
	// RouteServer answers which host serves the personal drive for this account.
	RouteServer = "https://user-njs.yun.139.com"
	// RefreshServer issues a refreshed login token from an existing one.
	RefreshServer = "https://aas.caiyun.feixin.10086.cn:443"
	// DefaultRootFileID is the file ID of the drive root folder.
	DefaultRootFileID = "root"

	// Client type values the drive uses to decide per-file size limits.
	clientTypeWeb = "9"
	clientTypePC  = "13"

	appVersion = "7.14.0"
	appChannel = "10000034"

	apiList          = "/file/list"
	apiDownloadURL   = "/file/getDownloadUrl"
	apiCreate        = "/file/create"
	apiGetUploadURL  = "/file/getUploadUrl"
	apiComplete      = "/file/complete"
	apiUpdate        = "/file/update"
	apiBatchMove     = "/file/batchMove"
	apiBatchCopy     = "/file/batchCopy"
	apiBatchTrash    = "/recyclebin/batchTrash"
	apiRoutePolicy   = "/user/route/qryRoutePolicy"
	apiRefreshToken  = "/tellin/authTokenRefresh.do"
	refreshClientTyp = "656"

	// downloadURLTTL is the validity of a download URL (X-Amz-Expires=900).
	downloadURLTTL = 15 * time.Minute
	// uploadURLTTL is the validity of a part upload URL (X-Amz-Expires=7200).
	uploadURLTTL = 2 * time.Hour
	// refreshMargin triggers a token refresh this long before it expires.
	refreshMargin = 15 * 24 * time.Hour
	// refreshCooldown keeps concurrent requests that cross the renewal window
	// together from each refreshing and overwriting one another's token.
	refreshCooldown = time.Minute
	// partBatchSize is the maximum number of part URLs the create call signs.
	partBatchSize = 100
	// deleteBatchSize limits how many file IDs go into one trash call.
	deleteBatchSize = 100
	// listPageSize is the page size requested from /file/list.
	listPageSize = 100
	// redactedPrefixLen is how many leading characters of a secret may be logged.
	redactedPrefixLen = 6
)

// Refresh failure kinds surfaced to the administrator dashboard.
const (
	// RefreshErrorKindRefreshFailed the drive refused the renewal request, or it
	// could not be reached at all.
	RefreshErrorKindRefreshFailed = "refresh_failed"
	// RefreshErrorKindPersistFailed the token was renewed but could not be
	// written back, so a restart falls back to the previous one.
	RefreshErrorKindPersistFailed = "persist_failed"
	// RefreshErrorKindExpired the credential expired and can no longer be
	// renewed; a fresh Authorization header must be pasted.
	RefreshErrorKindExpired = "expired"
)

var (
	// ErrAuthExpired the pasted login state was rejected and could not be refreshed.
	ErrAuthExpired     = errors.New("login state expired, please paste a fresh Authorization header") // ErrContentRejected the cloud drive's content audit blocked this object.
	ErrContentRejected = errors.New("object was rejected by the cloud drive content audit")
	// ErrFileNotFound the requested path does not exist.
	ErrFileNotFound = errors.New("file not found")
	// ErrEmptyBody cloud drive returned a response without a body.
	ErrEmptyBody = errors.New("empty response body")
)

// Client talks to the China Mobile cloud drive (139) personal drive API using
// the caller's own web login state.
type Client interface {
	ListChildren(ctx context.Context, parentFileID string) ([]FileInfo, error)
	ResolvePath(ctx context.Context, savePath string) (string, error)
	ParentOf(ctx context.Context, savePath string) (string, error)
	EnsureFolder(ctx context.Context, savePath string) (string, error)
	Meta(ctx context.Context, savePath string) (*FileInfo, error)
	GetDownloadURL(ctx context.Context, fileID string) (*DownloadURL, error)
	Create(ctx context.Context, req *CreateRequest) (*CreateResponse, error)
	GetUploadURL(ctx context.Context, fileID, uploadID string, infos []partInfo) ([]uploadedPart, error)
	Complete(ctx context.Context, req *CompleteRequest) error
	UploadParts(ctx context.Context, file *fs.UploadRequest, parts []uploadedPart, chunkSize int64) error
	Trash(ctx context.Context, fileIDs []string) error
	Rename(ctx context.Context, fileID, name string) error

	ChunkSize() int64
}

type client struct {
	policy *ent.StoragePolicy
	http   request.Client
	l      logging.Logger
	chunk  int64

	// persistToken, when set, stores a refreshed login token back into the
	// policy so a restart reuses it instead of the stale pasted value.
	persistToken   func(ctx context.Context, authorization string) error
	hadBasicPrefix bool

	// reportRefreshError, when set, tells the policy store that a renewal
	// failed so the administrator sees it in the dashboard. kind is one of
	// RefreshErrorKind*; an empty kind clears a previous notice.
	reportRefreshError func(ctx context.Context, kind, message string) error

	// appKey is the x-yun-app-key header the web client sends, copied from the
	// browser by the administrator. It is not a secret of the drive account but
	// it is required by the API and must not be invented here.
	appKey string

	// mu guards the mutable session state below.
	mu            sync.Mutex
	authorization string
	account       string
	rootFileID    string
	personalHost  string
	resolved      bool

	// refreshNotBefore throttles token renewal: once this client rotated the
	// token, no further refresh happens before this instant. A rebuilt client
	// starts at the zero value, which never blocks a legitimate refresh.
	refreshNotBefore time.Time
}

// NewClient builds a client from an existing storage policy.
func NewClient(policy *ent.StoragePolicy, config conf.ConfigProvider, l logging.Logger) Client {
	chunkSize := int64(50 << 20) // 50 MB
	if policy.Settings != nil && policy.Settings.ChunkSize > 0 {
		chunkSize = policy.Settings.ChunkSize
	}

	root := strings.TrimSpace(policy.BucketName)
	if root == "" {
		root = DefaultRootFileID
	}

	// Users may paste the header verbatim or only its base64 payload.
	auth, hadPrefix := splitAuthorization(policy.AccessKey)

	host := strings.TrimSuffix(strings.TrimSpace(policy.Server), "/")
	if host == "" {
		host = DefaultServer
	}

	appKey := strings.TrimSpace(policy.SecretKey)

	return &client{
		policy:         policy,
		http:           request.NewClient(config, request.WithLogger(l)),
		l:              l,
		chunk:          chunkSize,
		authorization:  auth,
		appKey:         appKey,
		rootFileID:     root,
		personalHost:   host,
		hadBasicPrefix: hadPrefix,
	}
}

// splitAuthorization separates the "Basic " scheme from the base64 payload a
// user pasted, reporting whether the scheme was there. A renewed token is
// written back in the same shape, so the edit form keeps showing a complete
// header.
func splitAuthorization(pasted string) (payload string, hadScheme bool) {
	trimmed := strings.TrimSpace(pasted)
	lower := strings.ToLower(trimmed)
	if idx := strings.Index(lower, "basic "); idx >= 0 {
		return strings.TrimSpace(trimmed[idx+len("basic "):]), true
	}

	return trimmed, false
}

// authorizationHeader renders the current login token in the shape it is stored
// in, so a renewed credential lands in the policy exactly like a pasted one.
func (c *client) authorizationHeader() string {
	if c.hadBasicPrefix {
		return "Basic " + c.authorization
	}

	return c.authorization
}

func (c *client) ChunkSize() int64 {
	return c.chunk
}

// SetTokenPersister installs the callback used to store a refreshed login token
// back into the storage policy.
func SetTokenPersisterFn(d Client, fn func(ctx context.Context, authorization string) error) {
	if c, ok := d.(*client); ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.persistToken = fn
	}
}

// SetRefreshErrorReporter installs the callback used to surface a failed
// credential renewal to the administrator.
func SetRefreshErrorReporterFn(d Client, fn func(ctx context.Context, kind, message string) error) {
	if c, ok := d.(*client); ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.reportRefreshError = fn
	}
}

// clientType reports which client the drive should treat us as. The web client
// allows up to 50 GB per file, the PC client up to 500 GB.
func (c *client) clientType() string {
	if c.policy.Settings != nil && strings.EqualFold(strings.TrimSpace(c.policy.Settings.Region), "pc") {
		return clientTypePC
	}
	return clientTypeWeb
}

// clientInfo builds the x-yun-client-info header value.
func (c *client) clientInfo() string {
	return "||" + c.clientType() + "|" + appVersion + "|chrome|120.0.0.0|||windows 10||zh-CN|||dW5kZWZpbmVk||"
}

// authHeader returns the Authorization header value, without the "Basic " prefix.
//
// It must not take c.mu: headers() is called from the request paths that
// already hold the lock, most notably the route-policy and token-refresh
// calls made under it by personalCloudHost. Taking the lock here deadlocks
// the very first request of every process (Mu is not reentrant).
func (c *client) authHeader() string {
	return c.authorization
}

// accountName extracts the account from the login token. The token payload is
// base64("pc:<account>:<token-with-expiry>"), the third segment carries the
// expiry as its fourth |-separated field.
func (c *client) accountName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accountNameLocked()
}

// accountNameLocked is accountName for callers that already hold c.mu.
func (c *client) accountNameLocked() string {
	if c.account != "" {
		return c.account
	}

	decoded, err := base64.StdEncoding.DecodeString(c.authorization)
	if err != nil {
		return ""
	}

	segments := strings.Split(string(decoded), ":")
	if len(segments) < 3 {
		return ""
	}

	c.account = segments[1]
	return c.account
}

// signDigest reproduces the request signature the web client sends in
// mcloud-sign: <ts>,<random>,<digest>.
func signDigest(body, ts, random string) string {
	escaped := encodeURIComponent(body)
	chars := strings.Split(escaped, "")
	sort.Strings(chars)
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Join(chars, "")))

	bodyDigest := md5Hex(encoded)
	saltDigest := md5Hex(ts + ":" + random)

	return strings.ToUpper(md5Hex(bodyDigest + saltDigest))
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// encodeURIComponent mirrors the browser's encodeURIComponent used before the
// signature digest is computed.
func encodeURIComponent(s string) string {
	escaped := url.QueryEscape(s)
	replacer := strings.NewReplacer(
		"+", "%20",
		"%21", "!",
		"%27", "'",
		"%28", "(",
		"%29", ")",
		"%2A", "*",
	)
	return replacer.Replace(escaped)
}

// headers builds the header set every personal drive API call needs.
func (c *client) headers(body string) http.Header {
	ts := time.Now().Format("2006-01-02 15:04:05")
	random := randomString(16)

	h := http.Header{}
	h.Set("Accept", "application/json, text/plain, */*")
	h.Set("Authorization", "Basic "+c.authHeader())
	h.Set("Caller", "web")
	h.Set("Cms-Device", "default")
	h.Set("Content-Type", "application/json")
	h.Set("Mcloud-Channel", "1000101")
	h.Set("Mcloud-Client", "10701")
	h.Set("Mcloud-Route", "001")
	h.Set("Mcloud-Sign", fmt.Sprintf("%s,%s,%s", ts, random, signDigest(body, ts, random)))
	h.Set("Mcloud-Version", appVersion)
	h.Set("Origin", "https://yun.139.com")
	h.Set("Referer", "https://yun.139.com/w/")
	h.Set("x-DeviceInfo", "||9|"+appVersion+"|chrome|120.0.0.0|||windows 10||zh-CN|||")
	h.Set("x-huawei-channelSrc", appChannel)
	h.Set("x-inner-ntwk", "2")
	h.Set("x-m4c-caller", "PC")
	h.Set("x-m4c-src", "10002")
	h.Set("X-SvcType", "1")
	h.Set("X-Yun-Api-Version", "v1")
	h.Set("X-Yun-App-Channel", appChannel)
	// The web client's built-in app key, pasted by the administrator. Sending an
	// empty value is worse than omitting it: the drive rejects it as malformed.
	if c.appKey != "" {
		h.Set("x-yun-app-key", c.appKey)
	}
	h.Set("X-Yun-Channel-Source", appChannel)
	h.Set("X-Yun-Client-Info", c.clientInfo())
	h.Set("X-Yun-Module-Type", "100")
	h.Set("X-Yun-Svc-Type", "1")
	return h
}

// envelope is the response wrapper used by every personal drive endpoint.
type envelope struct {
	Success bool            `json:"success"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// call performs one API request against the resolved personal drive host and
// decodes `data` into out when it is non-nil.
func (c *client) call(ctx context.Context, api string, payload any, out any) error {
	host, err := c.personalCloudHost(ctx)
	if err != nil {
		return err
	}

	return c.post(ctx, host+api, payload, out)
}

// post performs one API request against an arbitrary URL.
func (c *client) post(ctx context.Context, target string, payload any, out any) error {
	var body string
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to encode request for %s: %w", target, err)
		}
		body = string(raw)
	}

	res := c.http.Request(http.MethodPost, target, strings.NewReader(body),
		request.WithContext(ctx),
		request.WithHeader(c.headers(body)),
	)
	if res.Err != nil {
		return fmt.Errorf("failed to request %s: %w", target, res.Err)
	}

	respBody, err := res.GetResponse()
	if err != nil {
		return fmt.Errorf("failed to read response of %s: %w", target, err)
	}

	return c.decode(target, res.Response.StatusCode, res.Response.Header, respBody, out)
}

// decode interprets the drive's response envelope and maps its error codes onto
// the driver's own sentinels.
func (c *client) decode(target string, statusCode int, header http.Header, body string, out any) error {
	if strings.TrimSpace(body) == "" {
		if statusCode >= 200 && statusCode < 300 {
			return ErrEmptyBody
		}
		return fmt.Errorf("cloud drive returned HTTP %d without a body for %s", statusCode, target)
	}

	var env envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		if statusCode >= 200 && statusCode < 300 {
			if out != nil {
				return json.Unmarshal([]byte(body), out)
			}
			return nil
		}
		return fmt.Errorf("failed to decode response of %s (HTTP %d): %w", target, statusCode, err)
	}

	if env.Success || (statusCode >= 200 && statusCode < 300 && env.Code == "") {
		if out == nil || len(env.Data) == 0 {
			return nil
		}
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("failed to decode data of %s: %w", target, err)
		}
		return nil
	}

	err := c.mapError(target, statusCode, env.Code, env.Message)

	// Throttling is retried by the caller's backoff loop.
	if statusCode == http.StatusTooManyRequests || statusCode >= 500 || isThrottleCode(env.Code) {
		return &backoff.RetryableError{Err: err, RetryAfter: retryAfterFromHeader(header)}
	}

	return err
}

// mapError converts a cloud drive error into one of the driver's sentinels or a
// descriptive error carrying the original code.
func (c *client) mapError(target string, statusCode int, code, message string) error {
	msg := strings.TrimSpace(message)
	switch {
	case code == "04000005", statusCode == http.StatusUnauthorized:
		return &APIError{Code: code, Message: msg, Err: ErrAuthExpired}
	case statusCode == http.StatusForbidden, isAuditCode(code):
		return &APIError{Code: code, Message: msg, Err: ErrContentRejected}
	}

	return &APIError{
		Code:    code,
		Message: msg,
		Err:     fmt.Errorf("cloud drive rejected %s (code %q, HTTP %d)", target, code, statusCode),
	}
}

// retryAfterFromHeader parses the standard Retry-After header.
func retryAfterFromHeader(header http.Header) time.Duration {
	if header == nil {
		return 0
	}
	seconds, err := strconv.ParseInt(header.Get("retry-after"), 10, 64)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func isThrottleCode(code string) bool {
	// 02000003 / 02000004 belong to the rate-limit family.
	return code == "02000003" || code == "02000004"
}

func isAuditCode(code string) bool {
	// The 2100xxxx namespace is the auditing family.
	return strings.HasPrefix(code, "2100")
}

// APIError is an error returned by the cloud drive API.
type APIError struct {
	Code    string
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
