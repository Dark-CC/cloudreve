package y139

import (
	"context"
	"encoding/base64"
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
)

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

func TestNumAndPartSize(t *testing.T) {
	cases := []struct {
		total, chunk int64
		num          int
	}{
		{0, 100, 1},
		{1, 100, 1},
		{99, 100, 1},
		{100, 100, 1},
		{101, 100, 2},
		{1000, 100, 10},
		{1001, 100, 11},
	}

	for _, c := range cases {
		got := numParts(c.total, c.chunk)
		if got != c.num {
			t.Errorf("numParts(%d, %d) = %d, want %d", c.total, c.chunk, got, c.num)
		}

		// Every part except the last is a full chunk; together they add up.
		var sum int64
		for i := 0; i < got; i++ {
			size := partSize(c.total, c.chunk, i, got)
			if i < got-1 && size != c.chunk {
				t.Errorf("partSize(%d, %d, %d) = %d, want a full chunk %d", c.total, c.chunk, i, size, c.chunk)
			}
			if size <= 0 && c.total > 0 {
				t.Errorf("partSize(%d, %d, %d) = %d, must be positive", c.total, c.chunk, i, size)
			}
			sum += size
		}
		if sum != c.total {
			t.Errorf("parts of %d with chunk %d add up to %d", c.total, c.chunk, sum)
		}
	}
}

func TestBuildPartInfos(t *testing.T) {
	const chunk = 100
	infos := buildPartInfos(250, chunk)

	if len(infos) != 3 {
		t.Fatalf("got %d parts, want 3", len(infos))
	}

	if infos[0].PartNumber != 1 || infos[1].PartNumber != 2 || infos[2].PartNumber != 3 {
		t.Errorf("part numbers are not 1-based and sequential: %+v", infos)
	}

	if infos[2].PartSize != 50 {
		t.Errorf("the last part holds the remainder: got %d, want 50", infos[2].PartSize)
	}

	for i, info := range infos {
		want := int64(i) * chunk
		if info.ParallelHashCtx.PartOffset != want {
			t.Errorf("part %d offset = %d, want %d", i+1, info.ParallelHashCtx.PartOffset, want)
		}
	}
}

func TestFileInfoModifiedAt(t *testing.T) {
	info := &FileInfo{UpdatedAt: "2024-05-06T07:08:09.123+08:00"}
	got := info.ModifiedAt()
	if got.IsZero() {
		t.Fatal("expected the RFC3339 timestamp to parse")
	}
	if got.Year() != 2024 || got.Month() != time.May || got.Day() != 6 {
		t.Errorf("parsed an unexpected date: %s", got)
	}

	empty := &FileInfo{}
	if !empty.ModifiedAt().IsZero() {
		t.Error("an entry without timestamps must report a zero time")
	}
}

func TestDownloadURLBest(t *testing.T) {
	cdn := &DownloadURL{URL: "https://oos.example/a", CDNURL: "https://cdn.example/a", CDNSwitch: true}
	if got := cdn.Best(); got != "https://cdn.example/a" {
		t.Errorf("cdn should win when switched on, got %q", got)
	}

	off := &DownloadURL{URL: "https://oos.example/a", CDNURL: "https://cdn.example/a", CDNSwitch: false}
	if got := off.Best(); got != "https://oos.example/a" {
		t.Errorf("the origin URL wins when the CDN is off, got %q", got)
	}

	empty := &DownloadURL{}
	if got := empty.Best(); got != "" {
		t.Errorf("expected an empty result, got %q", got)
	}
}

func TestAuthorizationParsing(t *testing.T) {
	// pc:<account>:<token>|<...>|<...>|<expiry ms>
	token := "abc|def|ghi|" + itoa(time.Now().Add(60*24*time.Hour).UnixMilli())
	auth := base64.StdEncoding.EncodeToString([]byte("pc:13800138000:" + token))

	c := &client{authorization: auth}
	if got := c.accountName(); got != "13800138000" {
		t.Errorf("accountName() = %q, want %q", got, "13800138000")
	}
}

func TestRedact(t *testing.T) {
	if got := redact(""); got != "<empty>" {
		t.Errorf("redact of an empty secret = %q, want the <empty> marker", got)
	}

	long := "YWJjZGVmZ2hpams="
	got := redact(long)
	if got == long {
		t.Error("redact must not return the secret verbatim")
	}
	if want := long[:redactedPrefixLen] + "***"; got != want {
		t.Errorf("redact(%q) = %q, want %q", long, got, want)
	}

	if got := redact("abc"); got != "***" {
		t.Errorf("a secret shorter than the prefix must not be revealed, got %q", got)
	}
}

// TestMissingAuthorizationIsAuthExpired pins the sentinel mapped to the UI code
// shown to administrators when the pasted login state is unusable.
func TestMissingAuthorizationIsAuthExpired(t *testing.T) {
	c := &client{}
	err := c.refreshTokenLocked(context.Background())
	if !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("expected ErrAuthExpired, got %v", err)
	}
}

// TestPolicyWithoutAuthorizationReturnsErrAuthExpired makes sure a policy that
// was saved without credentials fails fast instead of issuing a request.
func TestPolicyWithoutAuthorizationReturnsErrAuthExpired(t *testing.T) {
	policy := &ent.StoragePolicy{Type: types.PolicyTypeY139, Settings: &types.PolicySetting{}}
	c := NewClient(policy, nil, nil)
	if err := c.(*client).refreshToken(context.Background()); !errors.Is(err, ErrAuthExpired) {
		t.Fatalf("expected ErrAuthExpired, got %v", err)
	}
}

// itoa keeps the test independent from strconv's error handling.
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// TestHeadersDoNotRelock guards the self-deadlock that froze every upload: the
// first request of a process resolves the drive host under c.mu and, from
// inside that call, builds request headers. If headers reach back for the lock
// (as authHeader once did) the request blocks forever.
func TestHeadersDoNotRelock(t *testing.T) {
	token := "abc|def|ghi|" + itoa(time.Now().Add(60*24*time.Hour).UnixMilli())
	auth := base64.StdEncoding.EncodeToString([]byte("pc:13800138000:" + token))
	c := &client{authorization: auth, policy: &ent.StoragePolicy{}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.mu.Lock()
		defer c.mu.Unlock()
		// The exact call sequence personalCloudHost performs while locked.
		if got := c.authHeader(); got != auth {
			t.Errorf("authHeader() = %q, want the stored authorization", got)
		}
		if got := c.headers("{}").Get("Authorization"); got != "Basic "+auth {
			t.Errorf("Authorization header = %q", got)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("building headers while holding c.mu deadlocked")
	}
}

// fakeClient answers every request with one canned response and remembers what
// it was asked for, so the renewal path can be exercised without the network.
// nopLogger silences driver logging inside unit tests.
type nopLogger struct{}

func (nopLogger) Panic(string, ...any) {}
func (nopLogger) Error(string, ...any) {}
func (nopLogger) Warning(string, ...any) {}
func (nopLogger) Info(string, ...any) {}
func (nopLogger) Debug(string, ...any) {}
func (nopLogger) CopyWithPrefix(string) logging.Logger { return nopLogger{} }
func (nopLogger) SupportColor() bool { return false }

type fakeClient struct {
	body  string
	calls int
}

func (f *fakeClient) Apply(...request.Option) {}

func (f *fakeClient) Request(_, _ string, _ io.Reader, _ ...request.Option) *request.Response {
	f.calls++
	return &request.Response{Response: &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(f.body)),
	}}
}

// decodeAuth returns the plain credential behind an Authorization value.
func decodeAuth(t *testing.T, auth string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		t.Fatalf("stored credential is not valid base64: %v", err)
	}
	return string(raw)
}

// TestRenewalRewritesCredential covers the renewal path end to end: a
// credential close to expiry is refreshed, the refreshed token is used
// afterwards and the persister is told about it, so the policy keeps working
// across restarts instead of dying with the pasted token.
func TestRenewalRewritesCredential(t *testing.T) {
	fake := &fakeClient{body: `<root><return>0</return><token>NEWTOKEN</token><desc>ok</desc></root>`}
	old := "pc:13800138000:a|b|c|" + itoa(time.Now().Add(time.Minute).UnixMilli())
	auth := base64.StdEncoding.EncodeToString([]byte(old))

	var saved string
	c := &client{
		authorization: auth,
		policy:        &ent.StoragePolicy{},
		http:          fake,
		l:             nopLogger{},
		persistToken:  func(_ context.Context, v string) error { saved = v; return nil },
	}

	if err := c.refreshToken(context.Background()); err != nil {
		t.Fatalf("refreshToken() = %v, want nil", err)
	}

	if fake.calls != 1 {
		t.Fatalf("refresh endpoint calls = %d, want 1 for a credential about to expire", fake.calls)
	}

	// The stored value is base64, so compare the decoded credential.
	renewed := decodeAuth(t, c.authorization)
	if !strings.Contains(renewed, "NEWTOKEN") {
		t.Errorf("stored credential was not renewed with the new token: %q", renewed)
	}
	if strings.Contains(renewed, "OLDTOKEN") {
		t.Error("the expired token is still in the credential")
	}
	if saved != c.authorization {
		t.Errorf("persisted credential = %q, want the renewed one %q", redact(saved), redact(c.authorization))
	}
}

// TestRefreshSkippedWhenTokenIsFresh keeps the refresh call off the hot path.
func TestRefreshSkippedWhenTokenIsFresh(t *testing.T) {
	fake := &fakeClient{body: "unused"}
	old := "pc:13800138000:a|b|c|" + itoa(time.Now().Add(60*24*time.Hour).UnixMilli())
	c := &client{
		authorization: base64.StdEncoding.EncodeToString([]byte(old)),
		policy:        &ent.StoragePolicy{},
		http:          fake,
		l:             nopLogger{},
	}

	if err := c.refreshToken(context.Background()); err != nil {
		t.Fatalf("refreshToken() = %v, want nil", err)
	}
	if fake.calls != 0 {
		t.Errorf("refresh endpoint calls = %d, want 0 while the token is still fresh", fake.calls)
	}
	if got := decodeAuth(t, c.authorization); got != old {
		t.Errorf("a credential outside the renewal window must be left alone, got %q", got)
	}
}

// countingFake is a fakeClient that answers the route policy lookup and the
// token refresh separately, so the request path can be exercised without the
// network. It counts refresh calls only, and can fail on demand.
type countingFake struct {
	body  string
	err   error
	calls int
	mu    sync.Mutex
}

// fakeRouteBody is the envelope the drive really answers with: decode only
// fills the caller's struct from "data".
const fakeRouteBody = `{"code":"0","success":true,"data":{"routePolicyList":[{"siteID":"s","modName":"personal","httpsUrl":"https://personal.example/"}]}}`

func (f *countingFake) Apply(...request.Option) {}

func (f *countingFake) Request(_, target string, _ io.Reader, _ ...request.Option) *request.Response {
	if strings.Contains(target, apiRoutePolicy) {
		return &request.Response{Response: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(fakeRouteBody)),
		}}
	}

	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	if f.err != nil {
		return &request.Response{Err: f.err}
	}

	return &request.Response{Response: &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(f.body)),
	}}
}

func (f *countingFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// expiringCredential builds a credential whose token is the given duration from
// expiry, the shape the drive actually issues ("pc:<account>:a|b|c|<ms>").
func expiringCredential(t *testing.T, remaining time.Duration) (string, string) {
	t.Helper()
	token := "a|b|c|" + itoa(time.Now().Add(remaining).UnixMilli())
	plain := "pc:13800138000:" + token
	return base64.StdEncoding.EncodeToString([]byte(plain)), plain
}

// TestPersonalCloudHostPersistsRenewedToken is the regression test for the
// defect this fix addresses: the request path used to renew the token in memory
// only, so a restart fell back to the stale credential in the policy and the
// drive eventually rejected it.
func TestPersonalCloudHostPersistsRenewedToken(t *testing.T) {
	fake := &countingFake{body: `<root><return>0</return><token>NEWTOKEN</token><desc>ok</desc></root>`}
	auth, _ := expiringCredential(t, time.Minute)

	var saved []string
	c := &client{
		authorization: auth,
		policy:        &ent.StoragePolicy{Server: "https://pinned.example"},
		http:          fake,
		l:             nopLogger{},
		persistToken: func(_ context.Context, v string) error {
			saved = append(saved, v)
			return nil
		},
	}

	if _, err := c.personalCloudHost(context.Background()); err != nil {
		t.Fatalf("personalCloudHost() = %v, want nil", err)
	}

	if len(saved) != 1 {
		t.Fatalf("policy writes = %d, want 1 once the token was renewed", len(saved))
	}
	if !strings.Contains(decodeAuth(t, saved[0]), "NEWTOKEN") {
		t.Errorf("persisted credential %q does not carry the renewed token", redact(saved[0]))
	}
	if saved[0] != c.authorization {
		t.Errorf("persisted credential %q differs from the one in use %q",
			redact(saved[0]), redact(c.authorization))
	}
}

// TestPersonalCloudHostDoesNotPersistFreshToken keeps the policy write off the
// hot path when nothing was renewed.
func TestPersonalCloudHostDoesNotPersistFreshToken(t *testing.T) {
	fake := &countingFake{body: "unused"}
	auth, _ := expiringCredential(t, 60*24*time.Hour)

	writes := 0
	c := &client{
		authorization: auth,
		policy:        &ent.StoragePolicy{Server: "https://pinned.example"},
		http:          fake,
		l:             nopLogger{},
		persistToken:  func(context.Context, string) error { writes++; return nil },
	}

	if _, err := c.personalCloudHost(context.Background()); err != nil {
		t.Fatalf("personalCloudHost() = %v, want nil", err)
	}

	if writes != 0 {
		t.Errorf("policy writes = %d, want 0 while the token is still fresh", writes)
	}
	if got := decodeAuth(t, c.authorization); got != decodeAuth(t, auth) {
		t.Errorf("a credential outside the renewal window must be left alone, got %q", got)
	}
}

// TestConcurrentRenewalPersistsOnce guards the burst case: requests that all
// cross the renewal window together must not each call the endpoint and
// overwrite one another's token.
func TestConcurrentRenewalPersistsOnce(t *testing.T) {
	fake := &countingFake{body: `<root><return>0</return><token>NEWTOKEN</token><desc>ok</desc></root>`}
	auth, _ := expiringCredential(t, time.Minute)

	var (
		mu     sync.Mutex
		writes int
	)
	c := &client{
		authorization: auth,
		policy:        &ent.StoragePolicy{Server: "https://pinned.example"},
		http:          fake,
		l:             nopLogger{},
		persistToken: func(context.Context, string) error {
			mu.Lock()
			writes++
			mu.Unlock()
			return nil
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.personalCloudHost(context.Background()); err != nil {
				t.Errorf("personalCloudHost() = %v, want nil", err)
			}
		}()
	}
	wg.Wait()

	if got := fake.count(); got != 1 {
		t.Errorf("refresh endpoint calls = %d, want exactly 1 for concurrent renewal", got)
	}
	if writes != 1 {
		t.Errorf("policy writes = %d, want exactly 1 for concurrent renewal", writes)
	}
}

// TestRenewalFailureDoesNotBreakRequest makes sure an unreachable refresh
// endpoint fails the request with the mapped error instead of panicking, and
// that nothing is written when the endpoint refused.
func TestRenewalFailureDoesNotBreakRequest(t *testing.T) {
	fake := &countingFake{err: errors.New("network is unreachable")}
	auth, _ := expiringCredential(t, time.Minute)

	writes := 0
	c := &client{
		authorization: auth,
		policy:        &ent.StoragePolicy{Server: "https://pinned.example"},
		http:          fake,
		l:             nopLogger{},
		persistToken:  func(context.Context, string) error { writes++; return nil },
	}

	_, err := c.personalCloudHost(context.Background())
	if err == nil {
		t.Fatal("personalCloudHost() = nil, want the refresh failure to surface")
	}
	if writes != 0 {
		t.Errorf("policy writes = %d, want 0 after a failed refresh", writes)
	}
	if got := c.authorization; got != auth {
		t.Errorf("a failed refresh must leave the stored credential intact, got %q", redact(got))
	}
}

// TestPersistFailureDoesNotFailRequest pins the degradation rule: the drive
// stays usable when the policy store rejects the write, because the token in
// memory is already the renewed one.
func TestPersistFailureDoesNotFailRequest(t *testing.T) {
	fake := &countingFake{body: `<root><return>0</return><token>NEWTOKEN</token><desc>ok</desc></root>`}
	auth, _ := expiringCredential(t, time.Minute)

	c := &client{
		authorization: auth,
		policy:        &ent.StoragePolicy{Server: "https://pinned.example"},
		http:          fake,
		l:             nopLogger{},
		persistToken:  func(context.Context, string) error { return errors.New("database is locked") },
	}

	host, err := c.personalCloudHost(context.Background())
	if err != nil {
		t.Fatalf("personalCloudHost() = %v, want nil when only the write back fails", err)
	}
	if host == "" {
		t.Error("personalCloudHost() returned an empty host")
	}
	if !strings.Contains(decodeAuth(t, c.authorization), "NEWTOKEN") {
		t.Error("the renewed token must still be used in memory after a failed write back")
	}
}

// TestPoliciesRenewIndependently pins the isolation rule: two 139 policies in
// one installation each own their client, so a renewal (and its throttle) on
// one must not touch the other.
func TestPoliciesRenewIndependently(t *testing.T) {
	newFake := func() *countingFake {
		return &countingFake{body: `<root><return>0</return><token>NEWTOKEN</token><desc>ok</desc></root>`}
	}

	type policyUnderTest struct {
		c     *client
		fake  *countingFake
		wrote []string
	}

	build := func() *policyUnderTest {
		fake := newFake()
		auth, _ := expiringCredential(t, time.Minute)
		p := &policyUnderTest{fake: fake}
		p.c = &client{
			authorization: auth,
			policy:        &ent.StoragePolicy{Server: "https://pinned.example"},
			http:          fake,
			l:             nopLogger{},
			persistToken: func(_ context.Context, v string) error {
				p.wrote = append(p.wrote, v)
				return nil
			},
		}
		return p
	}

	a, b := build(), build()

	if _, err := a.c.personalCloudHost(context.Background()); err != nil {
		t.Fatalf("policy A renewal = %v, want nil", err)
	}

	if a.fake.count() != 1 {
		t.Errorf("policy A refresh calls = %d, want 1", a.fake.count())
	}
	if b.fake.count() != 0 {
		t.Errorf("policy B refresh calls = %d, want 0, policy A must not renew it", b.fake.count())
	}
	if len(a.wrote) != 1 {
		t.Errorf("policy A policy writes = %d, want 1", len(a.wrote))
	}
	if len(b.wrote) != 0 {
		t.Errorf("policy B policy writes = %d, want 0", len(b.wrote))
	}

	// B still renews on its own, and its own throttle is independent of A's.
	if _, err := b.c.personalCloudHost(context.Background()); err != nil {
		t.Fatalf("policy B renewal = %v, want nil", err)
	}
	if b.fake.count() != 1 {
		t.Errorf("policy B refresh calls after its own request = %d, want 1", b.fake.count())
	}
}

// TestExpiredCredentialIsReported makes sure an unrenewable credential is not
// only an error on the wire: the administrator is told which kind of failure
// happened.
func TestExpiredCredentialIsReported(t *testing.T) {
	var kind, message string
	c := &client{
		authorization: "not-base64-at-all",
		policy:        &ent.StoragePolicy{},
		l:             nopLogger{},
		reportRefreshError: func(_ context.Context, k, m string) error {
			kind, message = k, m
			return nil
		},
	}

	if _, err := c.personalCloudHost(context.Background()); err == nil {
		t.Fatal("personalCloudHost() = nil, want a failure for an unusable credential")
	}
	if kind != RefreshErrorKindExpired {
		t.Errorf("reported kind = %q, want %q", kind, RefreshErrorKindExpired)
	}
	if message == "" {
		t.Error("reported message is empty")
	}
}

// TestPersistFailureIsReported covers the second failure kind: the token was
// renewed but the write back did not stick.
func TestPersistFailureIsReported(t *testing.T) {
	fake := &countingFake{body: `<root><return>0</return><token>NEWTOKEN</token><desc>ok</desc></root>`}
	auth, _ := expiringCredential(t, time.Minute)

	var kind string
	c := &client{
		authorization: auth,
		policy:        &ent.StoragePolicy{Server: "https://pinned.example"},
		http:          fake,
		l:             nopLogger{},
		persistToken:  func(context.Context, string) error { return errors.New("database is locked") },
		reportRefreshError: func(_ context.Context, k, _ string) error {
			kind = k
			return nil
		},
	}

	if _, err := c.personalCloudHost(context.Background()); err != nil {
		t.Fatalf("personalCloudHost() = %v, want nil when only the write back fails", err)
	}
	if kind != RefreshErrorKindPersistFailed {
		t.Errorf("reported kind = %q, want %q", kind, RefreshErrorKindPersistFailed)
	}
}

// TestRenewedTokenKeepsHeaderShape guards the storage format: a policy whose
// Authorization starts with "Basic " must be written back with that prefix, so
// the admin edit form still shows a complete, paste-ready header.
func TestRenewedTokenKeepsHeaderShape(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pasted string
		want   string
	}{
		{"header with prefix", "Basic ", "Basic "},
		{"bare payload", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth, _ := expiringCredential(t, time.Minute)
			fake := &countingFake{body: `<root><return>0</return><token>ROTATED</token><desc>ok</desc></root>`}

			// Parse the pasted value exactly as NewClient does, so the shape
			// under test is the one production derives.
			payload, hadScheme := splitAuthorization(tc.pasted + auth)
			var wrote string
			c := &client{
				authorization:  payload,
				policy:         &ent.StoragePolicy{Server: "https://pinned.example"},
				http:           fake,
				l:              nopLogger{},
				hadBasicPrefix: hadScheme,
				persistToken: func(_ context.Context, v string) error {
					wrote = v
					return nil
				},
			}

			if _, err := c.personalCloudHost(context.Background()); err != nil {
				t.Fatalf("personalCloudHost() = %v, want nil", err)
			}

			if !strings.HasPrefix(wrote, tc.want) {
				t.Errorf("persisted credential = %q, want prefix %q", wrote, tc.want)
			}

			encoded := strings.TrimPrefix(wrote, tc.want)
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatalf("persisted credential is not valid base64: %v", err)
			}
			if !strings.HasSuffix(string(decoded), ":ROTATED") {
				t.Errorf("persisted credential = %q, want it to carry the rotated token", string(decoded))
			}
		})
	}
}
