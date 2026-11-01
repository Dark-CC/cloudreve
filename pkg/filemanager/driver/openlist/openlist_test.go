package openlist

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	serserializer "github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

type nopLogger struct{}

func (nopLogger) Panic(string, ...any)                 {}
func (nopLogger) Error(string, ...any)                 {}
func (nopLogger) Warning(string, ...any)               {}
func (nopLogger) Info(string, ...any)                  {}
func (nopLogger) Debug(string, ...any)                 {}
func (nopLogger) CopyWithPrefix(string) logging.Logger { return nopLogger{} }
func (nopLogger) SupportColor() bool                   { return false }

var _ logging.Logger = nopLogger{}

// fakeClient answers the API routes the driver uses without a network. The
// response for a route is looked up by API path, and every call is recorded so a
// test can assert how often a token was renewed.
type fakeClient struct {
	mu sync.Mutex
	// loginBody is answered for /api/auth/login, listBody for /api/fs/list and
	// getBody for /api/fs/get.
	loginBody string
	listBody  string
	getBody   string
	// loginErr, when set, fails the login request at the transport level.
	loginErr error
	// tokens collects every Authorization header received, in call order.
	tokens []string
	// logins counts /api/auth/login calls.
	logins int
	// paths records every "path" value sent to the list/get routes.
	paths []string
}

func (f *fakeClient) Apply(...request.Option) {}

func (f *fakeClient) Request(method, target string, body io.Reader, _ ...request.Option) *request.Response {
	raw, _ := io.ReadAll(body)

	var payload struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(raw, &payload)

	f.mu.Lock()
	defer f.mu.Unlock()

	if payload.Path != "" {
		f.paths = append(f.paths, payload.Path)
	}

	switch {
	case strings.HasSuffix(target, apiLogin):
		f.logins++
		if f.loginErr != nil {
			return &request.Response{Err: f.loginErr}
		}
		return okResponse(f.loginBody)

	case strings.HasSuffix(target, apiList):
		return okResponse(f.listBody)

	case strings.HasSuffix(target, apiGet):
		return okResponse(f.getBody)
	}

	return &request.Response{Response: &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader(`{"code":404,"message":"no route"}`)),
	}}
}

func okResponse(body string) *request.Response {
	return &request.Response{Response: &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}}
}

func (f *fakeClient) loginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins
}

func (f *fakeClient) sentPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func newTestPolicy(mount string) *ent.StoragePolicy {
	return &ent.StoragePolicy{
		Type:      types.PolicyTypeOpenList,
		Server:    "https://openlist.example",
		AccessKey: "admin",
		SecretKey: "secret",
		Settings:  &types.PolicySetting{MountPath: mount},
	}
}

// loginBody builds a login reply. A real token is a JWT with an exp claim, so
// the fake one carries a decodable one too: renewal timing is driven by it.
func loginBody(t *testing.T, expiresIn time.Duration) string {
	t.Helper()

	token := fakeJWT(time.Now().Add(expiresIn))
	raw, err := json.Marshal(map[string]any{
		"code":    200,
		"data":    map[string]any{"token": token, "token_expires_in": 48},
		"message": "success",
	})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}
	return string(raw)
}

// fakeJWT builds a decodable (unsigned) JWT whose payload carries exp.
func fakeJWT(exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(map[string]any{"exp": exp.Unix()})
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"/":        "",
		".":        "",
		"a":        "a",
		"/a/":      "a",
		"a/b/c":    "a/b/c",
		"//a//b//": "a//b",
		" /a/b/ ":  "a/b",
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFullPath prefixes the mount point, the whole reason a single join point
// exists: every request has to be anchored under the configured mount.
func TestFullPath(t *testing.T) {
	cases := []struct {
		mount string
		rel   string
		want  string
	}{
		{"", "", "/"},
		{"", "a/b", "/a/b"},
		{"mnt", "", "/mnt"},
		{"mnt", "a/b", "/mnt/a/b"},
		{"/mnt/", "/a/b/", "/mnt/a/b"},
		{"deep/mnt", "x", "/deep/mnt/x"},
	}

	for _, c := range cases {
		cl := &client{mountPath: normalizePath(c.mount)}
		got, err := cl.fullPath(c.rel)
		if err != nil {
			t.Errorf("fullPath(%q, %q) = %v", c.mount, c.rel, err)
			continue
		}
		if got != c.want {
			t.Errorf("fullPath(%q, %q) = %q, want %q", c.mount, c.rel, got, c.want)
		}
	}
}

// TestFullPathRejectsEscape is the boundary guard: a ".." segment must be
// refused here, never forwarded to the instance.
func TestFullPathRejectsEscape(t *testing.T) {
	cl := &client{mountPath: "mnt"}
	for _, rel := range []string{"..", "../etc", "a/../../etc", "a/.."} {
		if _, err := cl.fullPath(rel); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("fullPath(%q) = %v, want ErrInvalidPath", rel, err)
		}
	}
}

// TestListChildrenPrefixesMount proves the list request lands under the mount
// point, which is the actual wire path the instance sees.
func TestListChildrenPrefixesMount(t *testing.T) {
	fake := &fakeClient{
		loginBody: loginBody(t, 47*time.Hour),
		listBody:  `{"code":200,"data":{"content":[{"name":"a.txt","size":3,"is_dir":false,"modified":"2026-01-02T03:04:05Z"}],"total":1}}`,
	}

	c := &client{
		policy:    newTestPolicy("mnt"),
		http:      fake,
		l:         nopLogger{},
		mountPath: "mnt",
		username:  "admin",
		password:  "secret",
	}

	items, err := c.ListChildren(context.Background(), "docs")
	if err != nil {
		t.Fatalf("ListChildren() = %v, want nil", err)
	}

	if len(items) != 1 || items[0].Name != "a.txt" {
		t.Fatalf("ListChildren() = %+v, want one entry named a.txt", items)
	}

	paths := fake.sentPaths()
	if len(paths) != 1 || paths[0] != "/mnt/docs" {
		t.Fatalf("paths sent = %v, want [/mnt/docs]", paths)
	}
}

// TestListChildrenPaginates walks the pagination loop: each page is appended
// until the reported total is reached.
func TestListChildrenPaginates(t *testing.T) {
	page1 := `{"code":200,"data":{"content":[{"name":"a"},{"name":"b"}],"total":3}}`
	page2 := `{"code":200,"data":{"content":[{"name":"c"}],"total":3}}`

	fake := &fakeClient{loginBody: loginBody(t, 47*time.Hour), listBody: page1}
	c := &client{
		policy:    newTestPolicy(""),
		http:      &pagedFake{page1: page1, page2: page2},
		l:         nopLogger{},
		mountPath: "",
		username:  "admin",
		password:  "secret",
	}
	_ = fake

	items, err := c.ListChildren(context.Background(), "")
	if err != nil {
		t.Fatalf("ListChildren() = %v, want nil", err)
	}

	if len(items) != 3 {
		t.Fatalf("ListChildren() returned %d entries, want 3 across two pages", len(items))
	}
	if items[2].Name != "c" {
		t.Errorf("last entry = %q, want c", items[2].Name)
	}
}

// pagedFake answers a first page and then a second, keyed by the requested page
// number.
type pagedFake struct {
	page1, page2 string
	mu           sync.Mutex
	listCalls    int
}

func (p *pagedFake) Apply(...request.Option) {}

func (p *pagedFake) Request(_, target string, body io.Reader, _ ...request.Option) *request.Response {
	raw, _ := io.ReadAll(body)

	if strings.HasSuffix(target, apiLogin) {
		return okResponse(loginBody(&testing.T{}, 47*time.Hour))
	}

	var payload struct {
		Page int `json:"page"`
	}
	_ = json.Unmarshal(raw, &payload)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.listCalls++

	if payload.Page >= 2 {
		return okResponse(p.page2)
	}
	return okResponse(p.page1)
}

// TestBodyCodeDecidesFailure is the researched pitfall: OpenList answers errors
// with HTTP 200, so only the body's code can be trusted.
func TestBodyCodeDecidesFailure(t *testing.T) {
	fake := &fakeClient{
		loginBody: loginBody(t, 47*time.Hour),
		listBody:  `{"code":500,"message":"storage not found","data":null}`,
	}

	c := &client{
		policy:    newTestPolicy("mnt"),
		http:      fake,
		l:         nopLogger{},
		mountPath: "mnt",
		username:  "admin",
		password:  "secret",
	}

	_, err := c.ListChildren(context.Background(), "")
	if !errors.Is(err, ErrMountNotFound) {
		t.Fatalf("ListChildren() = %v, want ErrMountNotFound from a 200 response", err)
	}
}

// TestUnauthorizedTriggersRelogin exercises the renewal path: a request rejected
// with code 401 logs in again and retries exactly once.
func TestUnauthorizedTriggersRelogin(t *testing.T) {
	fake := &retryFake{
		loginBody:  loginBody(t, 47*time.Hour),
		rejectOnce: true,
		listBody:   `{"code":200,"data":{"content":[],"total":0}}`,
	}

	c := &client{
		policy:    newTestPolicy(""),
		http:      fake,
		l:         nopLogger{},
		mountPath: "",
		username:  "admin",
		password:  "secret",
	}

	if _, err := c.ListChildren(context.Background(), ""); err != nil {
		t.Fatalf("ListChildren() = %v, want nil after a successful re-login", err)
	}

	if fake.logins != 2 {
		t.Errorf("logins = %d, want 2: one initial login and one on the 401", fake.logins)
	}
	if fake.listAttempts != 2 {
		t.Errorf("list attempts = %d, want 2: the rejected one and its retry", fake.listAttempts)
	}
}

// retryFake rejects the first list call with code 401 like an expired token.
type retryFake struct {
	loginBody    string
	listBody     string
	rejectOnce   bool
	logins       int
	listAttempts int
	mu           sync.Mutex
}

func (f *retryFake) Apply(...request.Option) {}

func (f *retryFake) Request(_, target string, _ io.Reader, _ ...request.Option) *request.Response {
	f.mu.Lock()
	defer f.mu.Unlock()

	if strings.HasSuffix(target, apiLogin) {
		f.logins++
		return okResponse(f.loginBody)
	}

	f.listAttempts++
	if f.rejectOnce && f.listAttempts == 1 {
		return okResponse(`{"code":401,"message":"token is invalidated","data":null}`)
	}
	return okResponse(f.listBody)
}

// TestTokenIsCachedAcrossCalls proves the token is reused: many calls, one
// login.
func TestTokenIsCachedAcrossCalls(t *testing.T) {
	fake := &fakeClient{
		loginBody: loginBody(t, 47*time.Hour),
		listBody:  `{"code":200,"data":{"content":[],"total":0}}`,
	}

	c := &client{
		policy:    newTestPolicy(""),
		http:      fake,
		l:         nopLogger{},
		mountPath: "",
		username:  "admin",
		password:  "secret",
	}

	for i := 0; i < 5; i++ {
		if _, err := c.ListChildren(context.Background(), ""); err != nil {
			t.Fatalf("ListChildren() call %d = %v", i, err)
		}
	}

	if got := fake.loginCount(); got != 1 {
		t.Errorf("logins = %d, want 1: the token is valid for 48 hours", got)
	}
}

// TestTokenNearExpiryRenews is the 48 hour path: a token inside the renewal
// margin is replaced before it is used.
func TestTokenNearExpiryRenews(t *testing.T) {
	fake := &fakeClient{
		loginBody: loginBody(t, 47*time.Hour),
		listBody:  `{"code":200,"data":{"content":[],"total":0}}`,
	}

	c := &client{
		policy:    newTestPolicy(""),
		http:      fake,
		l:         nopLogger{},
		mountPath: "",
		username:  "admin",
		password:  "secret",
		// A token one minute from expiry: inside refreshMargin.
		jwt:      fakeJWT(time.Now().Add(time.Minute)),
		expireAt: time.Now().Add(time.Minute),
	}

	if _, err := c.ListChildren(context.Background(), ""); err != nil {
		t.Fatalf("ListChildren() = %v", err)
	}

	if got := fake.loginCount(); got != 1 {
		t.Errorf("logins = %d, want 1: a token inside the renewal margin must be renewed", got)
	}
}

// TestLoginFailureIsReported proves a failed login reaches the administrator
// notice channel instead of vanishing.
func TestLoginFailureIsReported(t *testing.T) {
	fake := &fakeClient{
		loginBody: `{"code":401,"message":"incorrect username or password","data":null}`,
		getBody:   `{"code":200,"data":{}}`,
	}

	var kinds, messages []string
	c := &client{
		policy:    newTestPolicy(""),
		http:      fake,
		l:         nopLogger{},
		mountPath: "",
		username:  "admin",
		password:  "wrong",
		reportRefreshError: func(_ context.Context, kind, message string) error {
			kinds = append(kinds, kind)
			messages = append(messages, message)
			return nil
		},
	}

	_, err := c.Get(context.Background(), "a.txt")
	if !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("Get() = %v, want ErrAuthExpired", err)
	}

	if len(kinds) != 1 || kinds[0] != RefreshErrorKindExpired {
		t.Fatalf("reported kinds = %v, want one %q", kinds, RefreshErrorKindExpired)
	}
	if len(messages) != 1 || messages[0] == "" {
		t.Errorf("reported messages = %v, want one non-empty message", messages)
	}
}

// TestGetReturnsRawURL covers the 302 source: the driver hands back OpenList's
// own URL instead of proxying.
func TestGetReturnsRawURL(t *testing.T) {
	fake := &fakeClient{
		loginBody: loginBody(t, 47*time.Hour),
		getBody: `{"code":200,"data":{"name":"a.txt","size":3,"is_dir":false,` +
			`"raw_url":"https://storage.example/a.txt?sign=x"}}`,
	}

	c := &client{
		policy:    newTestPolicy("mnt"),
		http:      fake,
		l:         nopLogger{},
		mountPath: "mnt",
		username:  "admin",
		password:  "secret",
	}

	info, err := c.Get(context.Background(), "a.txt")
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}

	if info.RawURL != "https://storage.example/a.txt?sign=x" {
		t.Errorf("RawURL = %q, want the instance's own URL", info.RawURL)
	}

	paths := fake.sentPaths()
	if len(paths) != 1 || paths[0] != "/mnt/a.txt" {
		t.Errorf("paths sent = %v, want [/mnt/a.txt]", paths)
	}
}

// TestListMapsToPhysicalObjects checks the conversion the sync path consumes.
func TestListMapsToPhysicalObjects(t *testing.T) {
	fake := &fakeClient{
		loginBody: loginBody(t, 47*time.Hour),
		listBody: `{"code":200,"data":{"content":[` +
			`{"name":"dir","is_dir":true,"modified":"2026-01-02T03:04:05Z"},` +
			`{"name":"b.bin","size":42,"is_dir":false,"modified":"2026-02-03T04:05:06Z"}` +
			`],"total":2}}`,
	}

	c := &client{
		policy:    newTestPolicy(""),
		http:      fake,
		l:         nopLogger{},
		mountPath: "",
		username:  "admin",
		password:  "secret",
	}

	d := &Driver{policy: newTestPolicy(""), client: c, l: nopLogger{}}
	objs, err := d.List(context.Background(), "", nil, false)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	if len(objs) != 2 {
		t.Fatalf("List() returned %d objects, want 2", len(objs))
	}

	if !objs[0].IsDir || objs[0].RelativePath != "dir" {
		t.Errorf("first object = %+v, want the directory", objs[0])
	}
	if objs[1].Size != 42 || objs[1].RelativePath != "b.bin" {
		t.Errorf("second object = %+v, want b.bin of 42 bytes", objs[1])
	}
}

// TestSerializerErrorCodes maps the driver's sentinels onto the codes the
// frontend localizes.
func TestSerializerErrorCodes(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{ErrAuthExpired, serserializer.CodeStorageAuthExpired},
		{ErrMountNotFound, serserializer.CodeParamErr},
		{ErrInvalidPath, serserializer.CodeParamErr},
		{ErrFileNotFound, serserializer.CodeParamErr},
		{ErrNotSupported, serserializer.CodePolicyNotAllowed},
	}

	for _, c := range cases {
		err := asSerializerError(c.err)
		if err == nil {
			t.Errorf("asSerializerError(%v) = nil", c.err)
			continue
		}

		ser, ok := err.(serserializer.AppError)
		if !ok {
			t.Errorf("asSerializerError(%v) is %T, want a serializer error", c.err, err)
			continue
		}
		if ser.Code != c.code {
			t.Errorf("asSerializerError(%v).Code = %d, want %d", c.err, ser.Code, c.code)
		}
	}
}

// TestWriteOperationsUnsupported documents the read-only scope: every write
// entry point refuses instead of silently succeeding.
func TestWriteOperationsUnsupported(t *testing.T) {
	d := &Driver{policy: newTestPolicy(""), l: nopLogger{}}
	ctx := context.Background()

	if err := d.Put(ctx, nil); !errors.Is(err, ErrNotSupported) && err == nil {
		t.Errorf("Put() = %v, want an unsupported error", err)
	}

	if _, err := d.Delete(ctx, "a"); err == nil {
		t.Error("Delete() = nil, want an unsupported error")
	}

	if _, err := d.Token(ctx, nil, nil); err == nil {
		t.Error("Token() = nil, want an unsupported error")
	}
}

// TestCapabilitiesDoNotForceProxy is the "no byte proxying" guard: a proxy
// capability would route downloads through this site and void the 302.
func TestCapabilitiesDoNotForceProxy(t *testing.T) {
	d := &Driver{policy: newTestPolicy(""), l: nopLogger{}}
	caps := d.Capabilities()

	if caps == nil {
		t.Fatal("Capabilities() = nil")
	}

	if caps.StaticFeatures != nil && caps.StaticFeatures.Enabled(0) {
		t.Error("the proxy capability must stay off so downloads are not proxied")
	}

	if caps.MaxSourceExpire <= 0 || caps.MinSourceExpire < 0 {
		t.Errorf("source expiry window = %v..%v, want a positive window",
			caps.MinSourceExpire, caps.MaxSourceExpire)
	}
}

// TestNewRejectsIncompletePolicy keeps a half-configured policy from reaching
// the instance with an empty field.
func TestNewRejectsIncompletePolicy(t *testing.T) {
	cases := []*ent.StoragePolicy{
		{Type: types.PolicyTypeOpenList, AccessKey: "a", SecretKey: "b"},
		{Type: types.PolicyTypeOpenList, Server: "https://x.example", SecretKey: "b"},
		{Type: types.PolicyTypeOpenList, Server: "https://x.example", AccessKey: "a"},
		{Type: types.PolicyTypeOpenList, Server: "ftp://x.example", AccessKey: "a", SecretKey: "b"},
		{Type: types.PolicyTypeOpenList, Server: "not a url", AccessKey: "a", SecretKey: "b"},
	}

	for i, policy := range cases {
		if _, err := New(context.Background(), policy, nil, nil, nopLogger{}, nil); err == nil {
			t.Errorf("case %d: New() = nil, want a diagnosable error", i)
		}
	}
}

// TestRedactKeepsSecretsOutOfLogs guards the log format: no secret is written
// whole.
func TestRedactKeepsSecretsOutOfLogs(t *testing.T) {
	if got := redact("password-1234"); strings.Contains(got, "1234") {
		t.Errorf("redact() = %q, leaks the secret", got)
	}
	if got := redact(""); got != "<empty>" {
		t.Errorf("redact(\"\") = %q", got)
	}
	if got := redact("ab"); got != "***" {
		t.Errorf("redact(\"ab\") = %q, want ***", got)
	}
}
