package openlist

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/request"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
)

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"/":          "",
		".":          "",
		"a":          "a",
		"/a/":        "a",
		"a/b/c":      "a/b/c",
		" /a/b/ ":    "a/b",
		"//a//b//":   "a//b",
		"/team/docs": "team/docs",
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinMount(t *testing.T) {
	cases := []struct {
		mount, p, want string
	}{
		{"", "", "/"},
		{"", "a", "/a"},
		{"/team", "", "/team"},
		{"/team/", "/docs", "/team/docs"},
		{"team/docs", "a/b.txt", "/team/docs/a/b.txt"},
		{"", "/", "/"},
	}
	for _, c := range cases {
		if got := joinMount(c.mount, c.p); got != c.want {
			t.Errorf("joinMount(%q, %q) = %q, want %q", c.mount, c.p, got, c.want)
		}
	}
}

func TestStripMount(t *testing.T) {
	cases := []struct{ mount, in, want string }{
		{"", "a/b", "a/b"},
		{"docs", "", ""},
		{"docs", "docs", ""},
		{"docs", "docs/sub/x", "sub/x"},
		{"docs", "/docs/", ""},
		{"docs", "other", "other"},
		{"team/docs", "team/docs/a.txt", "a.txt"},
	}
	for _, c := range cases {
		if got := stripMount(c.mount, c.in); got != c.want {
			t.Errorf("stripMount(%q, %q) = %q, want %q", c.mount, c.in, got, c.want)
		}
	}
}

func TestValidateUserPath(t *testing.T) {
	ok := []string{"", "/", "a", "/a/b", "a/b/../c"}
	bad := []string{"..", "../", "a/../b", "/a/../../etc"}

	for _, p := range ok {
		// "a/b/../c" still climbs, so it is expected to be rejected too.
		err := validateUserPath(p)
		if p == "a/b/../c" {
			if err == nil {
				t.Errorf("validateUserPath(%q) accepted a traversal segment", p)
			}
			continue
		}
		if err != nil {
			t.Errorf("validateUserPath(%q) = %v, want nil", p, err)
		}
	}

	for _, p := range bad {
		if err := validateUserPath(p); !errors.Is(err, ErrPathEscaped) {
			t.Errorf("validateUserPath(%q) = %v, want ErrPathEscaped", p, err)
		}
	}
}

func TestDecodeBusinessErrorInsideHTTP200(t *testing.T) {
	// The headline trap: OpenList answers HTTP 200 for "object not found".
	err := decode(apiGet, 200, `{"code":500,"message":"object not found","data":null}`, nil)
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("decode() = %v, want ErrFileNotFound", err)
	}

	err = decode(apiList, 200, `{"code":401,"message":"token is invalidated","data":null}`, nil)
	if !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("decode() = %v, want ErrAuthExpired", err)
	}

	err = decode(apiGet, 200, `{"code":500,"message":"storage not found","data":null}`, nil)
	if !errors.Is(err, ErrStorageNotFound) {
		t.Fatalf("decode() = %v, want ErrStorageNotFound", err)
	}

	err = decode(apiGet, 200, `{"code":403,"message":"failed to get relative path","data":null}`, nil)
	if !errors.Is(err, ErrPathEscaped) {
		t.Fatalf("decode() = %v, want ErrPathEscaped", err)
	}
}

func TestDecodeSuccess(t *testing.T) {
	var out FileInfo
	if err := decode(apiGet, 200, `{"code":200,"message":"success","data":{"name":"a.txt","size":12,"raw_url":"http://x/a.txt"}}`, &out); err != nil {
		t.Fatalf("decode() = %v, want nil", err)
	}
	if out.Name != "a.txt" || out.Size != 12 || out.RawURL != "http://x/a.txt" {
		t.Fatalf("decoded %+v, unexpected", out)
	}

	// A non-2xx status with a success body must still fail.
	if err := decode(apiGet, http.StatusInternalServerError,
		`{"code":200,"message":"success","data":{}}`, nil); err == nil {
		t.Fatal("decode() accepted a 500 with a success body")
	}

	if err := decode(apiGet, 200, "  ", nil); !errors.Is(err, ErrEmptyBody) {
		t.Fatalf("decode() = %v, want ErrEmptyBody", err)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	err := &APIError{Code: 500, Message: "boom", Err: errors.New("openlist rejected")}
	if got := err.Error(); got != "openlist rejected: boom" {
		t.Fatalf("Error() = %q", got)
	}
}

func makeJWT(t *testing.T, exp int64) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{"exp": exp, "sub": "admin"})
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	return header + "." + payload + ".sig"
}

func TestJWTExpiry(t *testing.T) {
	want := time.Unix(2000000000, 0)
	if got := jwtExpiry(makeJWT(t, 2000000000)); !got.Equal(want) {
		t.Fatalf("jwtExpiry() = %s, want %s", got, want)
	}

	for _, token := range []string{"", "abc", "a.b", "a.b.c.d", "not.a jwt"} {
		if got := jwtExpiry(token); !got.IsZero() {
			t.Errorf("jwtExpiry(%q) = %s, want zero", token, got)
		}
	}
}

func TestTokenNeedsRenewal(t *testing.T) {
	now := time.Now()

	if tokenNeedsRenewal(now.Add(time.Hour), now) {
		t.Error("a token an hour from expiry should not renew")
	}
	if !tokenNeedsRenewal(now.Add(10*time.Minute), now) {
		t.Error("a token inside the refresh margin should renew")
	}
	if !tokenNeedsRenewal(now.Add(-time.Minute), now) {
		t.Error("an expired token should renew")
	}
	if tokenNeedsRenewal(time.Time{}, now) {
		t.Error("a token without a readable expiry should be left to the server")
	}
}

func TestRedact(t *testing.T) {
	if got := redact(""); got != "<empty>" {
		t.Errorf("redact(\"\") = %q", got)
	}
	if got := redact("abc"); got != "***" {
		t.Errorf("redact(short) = %q", got)
	}
	if got := redact("abcdefgh"); !strings.HasPrefix(got, "abcd") || !strings.HasSuffix(got, "***") {
		t.Errorf("redact(long) = %q", got)
	}
	if strings.Contains(redact("supersecretvalue"), "secret") {
		t.Error("redact leaked the secret body")
	}
}

func TestNewValidatesSettings(t *testing.T) {
	cases := []struct {
		name   string
		policy *ent.StoragePolicy
		want   string
	}{
		{
			name:   "no server",
			policy: &ent.StoragePolicy{AccessKey: "u", SecretKey: "p"},
			want:   "missing OpenList address",
		},
		{
			name:   "no user",
			policy: &ent.StoragePolicy{Server: "http://x", SecretKey: "p"},
			want:   "missing OpenList username",
		},
		{
			name:   "no password",
			policy: &ent.StoragePolicy{Server: "http://x", AccessKey: "u"},
			want:   "missing OpenList password",
		},
	}

	l := logging.NewConsoleLogger(logging.LevelWarning)
	for _, c := range cases {
		_, err := New(context.Background(), c.policy, nil, nil, l, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: New() = %v, want error containing %q", c.name, err, c.want)
		}
	}
}

func TestPolicyMountPath(t *testing.T) {
	if got := policyMountPath(&ent.StoragePolicy{Settings: &types.PolicySetting{MountPath: "/team"}}); got != "/team" {
		t.Errorf("policyMountPath() = %q", got)
	}
	if got := policyMountPath(&ent.StoragePolicy{}); got != "" {
		t.Errorf("policyMountPath(nil settings) = %q", got)
	}
}

func TestParentOf(t *testing.T) {
	cases := map[string]string{
		"a/b/c.txt": "a/b",
		"c.txt":     "",
		"/a":        "",
		"":          "",
	}
	for in, want := range cases {
		if got := parentOf(in); got != want {
			t.Errorf("parentOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAsSerializerError(t *testing.T) {
	if err := asSerializerError(nil); err != nil {
		t.Fatalf("asSerializerError(nil) = %v", err)
	}

	// Every sentinel must map to a typed application error, never a raw one.
	for _, sentinel := range []error{ErrAuthExpired, ErrStorageNotFound, ErrPathEscaped} {
		if got := asSerializerError(sentinel); got == sentinel {
			t.Errorf("asSerializerError(%v) passed the raw error through", sentinel)
		}
	}
}

// TestDecodeRealInstanceBodies replays the exact bodies the test instance
// returned, so the mapping stays pinned to reality and not to an assumption.
func TestDecodeRealInstanceBodies(t *testing.T) {
	cases := []struct {
		api, body string
		want      error
	}{
		{apiGet, `{"code":500,"message":"failed to get obj: object not found","data":null}`, ErrFileNotFound},
		{apiList, `{"code":500,"message":"failed get storage: storage not found; rawPath: /nosuchmount","data":null}`, ErrStorageNotFound},
		{apiList, `{"code":401,"message":"Guest user is disabled, login please","data":null}`, ErrAuthExpired},
		{apiList, `{"code":401,"message":"token is invalidated","data":null}`, ErrAuthExpired},
		{apiList, `{"code":403,"message":"using relative path is not allowed","data":null}`, ErrPathEscaped},
		{apiLogin, `{"code":429,"message":"Too many unsuccessful sign-in attempts have been made using an incorrect username or password, Try again later.","data":null}`, ErrRateLimited},
	}

	for _, c := range cases {
		// Every one of these arrives with HTTP 200 on a real instance.
		if err := decode(c.api, 200, c.body, nil); !errors.Is(err, c.want) {
			t.Errorf("decode(%s, %q) = %v, want %v", c.api, c.body, err, c.want)
		}
	}

	var out FileInfo
	body := `{"code":200,"message":"success","data":{"name":"readme.txt","size":21,"is_dir":false,"modified":"2026-11-03T14:11:19.986397235Z","raw_url":"https://ol.example.com/p/docs/readme.txt?sign=abc=:0"}}`
	if err := decode(apiGet, 200, body, &out); err != nil {
		t.Fatalf("decode(success) = %v", err)
	}
	if out.Size != 21 || out.IsDir || out.RawURL == "" {
		t.Fatalf("decoded %+v", out)
	}
	if out.ModifiedAt().IsZero() {
		t.Fatal("failed to parse the real modified timestamp")
	}
}

// TestLoginDoesNotRecurse pins the regression that cost a process: the login call
// used to go through post(), which asks tokenFor() for a token, which logs in
// again, until the stack overflowed. Login must never carry an Authorization
// header and must return after exactly one request.
func TestLoginDoesNotRecurse(t *testing.T) {
	var loginCalls int
	var sawAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != apiLogin {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		loginCalls++
		sawAuth = r.Header.Get("Authorization")

		exp := time.Now().Add(48 * time.Hour).Unix()
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(exp, 10) + `}`))
		token := "eyJhbGciOiJIUzI1NiJ9." + payload + ".sig"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"success","data":{"token":"` + token + `"}}`))
	}))
	defer srv.Close()

	c := &client{
		http:    request.NewClient(nil, request.WithLogger(testLogger{t})),
		l:       testLogger{t},
		baseURL: srv.URL,
		user:    "admin",
		pass:    "secret",
	}

	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login() = %v", err)
	}
	if loginCalls != 1 {
		t.Fatalf("login was called %d times, want 1", loginCalls)
	}
	if sawAuth != "" {
		t.Fatalf("login request carried Authorization %q, want none", sawAuth)
	}
	if c.token == "" || c.tokenExp.IsZero() {
		t.Fatalf("token not cached: %q exp=%v", c.token, c.tokenExp)
	}
}

type testLogger struct{ t *testing.T }

func (l testLogger) Info(f string, a ...any)              { l.t.Logf(f, a...) }
func (l testLogger) Warning(f string, a ...any)           { l.t.Logf(f, a...) }
func (l testLogger) Error(f string, a ...any)             { l.t.Logf(f, a...) }
func (l testLogger) Debug(f string, a ...any)             { l.t.Logf(f, a...) }
func (l testLogger) Panic(f string, a ...any)             { l.t.Logf(f, a...) }
func (l testLogger) CopyWithPrefix(string) logging.Logger { return l }
func (l testLogger) SupportColor() bool                   { return false }

// TestExpiredTokenRelogsins covers the 48 hour JWT with no refresh token: once
// the cached token is inside the renewal margin the driver logs in again on its
// own, and a token the server has already invalidated is replaced and the call
// retried exactly once.
func TestExpiredTokenRelogsins(t *testing.T) {
	var logins int
	var lists int
	var lastAuth string

	mux := http.NewServeMux()
	mux.HandleFunc(apiLogin, func(w http.ResponseWriter, r *http.Request) {
		logins++
		exp := time.Now().Add(48 * time.Hour).Unix()
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(exp, 10) + `}`))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"token":"h.` + payload + `.s"}}`))
	})
	mux.HandleFunc(apiList, func(w http.ResponseWriter, r *http.Request) {
		lists++
		lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"content":[],"total":0}}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &client{
		http:    request.NewClient(nil, request.WithLogger(testLogger{t})),
		l:       testLogger{t},
		baseURL: srv.URL,
		user:    "admin",
		pass:    "secret",
	}

	// A token already past its renewal margin must be replaced before use.
	c.token = "stale.token.here"
	c.tokenExp = time.Now().Add(time.Minute)

	if _, err := c.ListChildren(context.Background(), ""); err != nil {
		t.Fatalf("ListChildren() = %v", err)
	}
	if logins != 1 {
		t.Fatalf("logins = %d, want 1 (the stale token must be renewed)", logins)
	}
	if lastAuth == "stale.token.here" {
		t.Fatal("the stale token was sent instead of a fresh one")
	}

	// A token the server rejects mid-life is renewed and the call retried once.
	rejects := 1
	mux.HandleFunc(apiGet, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if rejects > 0 {
			rejects--
			_, _ = w.Write([]byte(`{"code":401,"message":"token is invalidated","data":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"name":"a","size":1}}`))
	})

	logins = 0
	if _, err := c.GetFileInfo(context.Background(), "a"); err != nil {
		t.Fatalf("GetFileInfo() after a 401 = %v", err)
	}
	if logins != 1 {
		t.Fatalf("logins after the 401 = %d, want 1", logins)
	}
}
