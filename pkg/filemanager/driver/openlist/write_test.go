package openlist

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

// writeFixture is a fake OpenList that records every write call, so the drive's
// endpoint contract can be asserted without a live instance.
type writeFixture struct {
	mu sync.Mutex

	requests []recordedCall

	// chunkSize is what /multipart/init reports back.
	chunkSize int64
	// received is what /multipart/status reports.
	status *MultipartSession
	// exists lists paths that /api/fs/get answers for.
	exists map[string]FileInfo
	// failRemove makes /api/fs/remove answer with a business error.
	failRemove *APIError
	// renameConflict makes /api/fs/rename answer like a name collision.
	renameConflict bool
}

type recordedCall struct {
	method string
	path   string
	header http.Header
	query  string
	body   string
}

func newWriteFixture(t *testing.T) (*writeFixture, *client, func()) {
	t.Helper()

	f := &writeFixture{
		chunkSize: minChunkSize,
		exists:    map[string]FileInfo{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc(apiLogin, f.login)
	mux.HandleFunc(apiGet, f.get)
	mux.HandleFunc(apiMkdir, f.ok)
	mux.HandleFunc(apiRename, f.rename)
	mux.HandleFunc(apiMove, f.ok)
	mux.HandleFunc(apiRemove, f.remove)
	mux.HandleFunc(apiPut, f.put)
	mux.HandleFunc(apiMultipartInit, f.init)
	mux.HandleFunc(apiMultipartChunk, f.ok)
	mux.HandleFunc(apiMultipartComplete, f.ok)
	mux.HandleFunc(apiMultipartAbort, f.ok)
	mux.HandleFunc(apiMultipartStatus, f.multipartStatus)

	srv := httptest.NewServer(mux)
	c := &client{
		http:    request.NewClient(nil, request.WithLogger(testLogger{t})),
		l:       testLogger{t},
		baseURL: srv.URL,
		mount:   "local",
		user:    "admin",
		pass:    "secret",
	}

	return f, c, srv.Close
}

func (f *writeFixture) record(r *http.Request, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, recordedCall{
		method: r.Method,
		path:   r.URL.Path,
		header: r.Header.Clone(),
		query:  r.URL.RawQuery,
		body:   body,
	})
}

func (f *writeFixture) calls() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedCall, len(f.requests))
	copy(out, f.requests)
	return out
}

func (f *writeFixture) login(w http.ResponseWriter, _ *http.Request) {
	// Login is not recorded: every assertion below is about the write call
	// itself, and a token renewal is an implementation detail of reaching it.
	exp := time.Now().Add(48 * time.Hour).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(exp, 10) + `}`))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":200,"data":{"token":"h.` + payload + `.s"}}`))
}

func (f *writeFixture) ok(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.record(r, string(body))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
}

func (f *writeFixture) get(w http.ResponseWriter, r *http.Request) {
	f.record(r, "")

	var req getRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	info, found := f.exists[req.Path]
	if !found {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":500,"message":"failed to get obj: object not found"}`))
		return
	}

	payload, _ := json.Marshal(info)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":200,"message":"success","data":` + string(payload) + `}`))
}

func (f *writeFixture) rename(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.record(r, string(body))

	w.Header().Set("Content-Type", "application/json")
	if f.renameConflict {
		_, _ = w.Write([]byte(`{"code":403,"message":"file [taken.txt] exists"}`))
		return
	}
	_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
}

func (f *writeFixture) remove(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.record(r, string(body))

	w.Header().Set("Content-Type", "application/json")
	if f.failRemove != nil {
		payload, _ := json.Marshal(f.failRemove.Message)
		_, _ = w.Write([]byte(`{"code":` + strconv.Itoa(f.failRemove.Code) + `,"message":` + string(payload) + `}`))
		return
	}
	_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
}

func (f *writeFixture) put(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.record(r, string(body))

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
}

func (f *writeFixture) init(w http.ResponseWriter, r *http.Request) {
	f.record(r, "")

	size, _ := strconv.ParseInt(r.Header.Get("X-File-Size"), 10, 64)
	chunk := f.chunkSize
	payload, _ := json.Marshal(MultipartSession{
		UploadID:    "up-1",
		State:       "receiving",
		Path:        r.Header.Get("File-Path"),
		Size:        size,
		ChunkSize:   chunk,
		TotalChunks: int((size + chunk - 1) / chunk),
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":200,"data":` + string(payload) + `}`))
}

func (f *writeFixture) multipartStatus(w http.ResponseWriter, r *http.Request) {
	f.record(r, "")

	if f.status == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"upload_id":""}}`))
		return
	}

	payload, _ := json.Marshal(f.status)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":200,"data":` + string(payload) + `}`))
}

// TestMkdirCreatesEachLevel covers the on-demand folder creation: OpenList's
// mkdir does not create parents, so every missing level is created in turn.
func TestMkdirCreatesEachLevel(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()

	if err := c.Mkdir(context.Background(), "a/b/c"); err != nil {
		t.Fatalf("Mkdir() = %v", err)
	}

	calls := f.calls()
	if len(calls) != 3 {
		t.Fatalf("Mkdir made %d calls, want 3: %+v", len(calls), calls)
	}

	wantPaths := []string{"/local/a", "/local/a/b", "/local/a/b/c"}
	for i, call := range calls {
		if call.path != apiMkdir {
			t.Fatalf("call %d hit %s, want %s", i, call.path, apiMkdir)
		}
		var req mkdirRequest
		if err := json.Unmarshal([]byte(call.body), &req); err != nil {
			t.Fatalf("call %d body is not JSON: %s", i, call.body)
		}
		if req.Path != wantPaths[i] {
			t.Fatalf("call %d created %q, want %q", i, req.Path, wantPaths[i])
		}
	}
}

// TestMkdirTreatsExistingAsSuccess keeps folder creation idempotent.
func TestMkdirTreatsExistingAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiLogin {
			exp := time.Now().Add(48 * time.Hour).Unix()
			payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(exp, 10) + `}`))
			_, _ = w.Write([]byte(`{"code":200,"data":{"token":"h.` + payload + `.s"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":403,"message":"object already exists"}`))
	}))
	defer srv.Close()

	c := &client{
		http:    request.NewClient(nil, request.WithLogger(testLogger{t})),
		l:       testLogger{t},
		baseURL: srv.URL,
		mount:   "local",
		user:    "admin",
		pass:    "secret",
	}

	if err := c.Mkdir(context.Background(), "a"); err != nil {
		t.Fatalf("Mkdir() on an existing folder = %v, want nil", err)
	}
}

// TestRenameSendsNameAndPath asserts the exact payload OpenList expects.
func TestRenameSendsNameAndPath(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()

	if err := c.Rename(context.Background(), "docs/old.txt", "new.txt"); err != nil {
		t.Fatalf("Rename() = %v", err)
	}

	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("Rename made %d calls, want 1", len(calls))
	}

	var req renameRequest
	if err := json.Unmarshal([]byte(calls[0].body), &req); err != nil {
		t.Fatalf("body is not JSON: %s", calls[0].body)
	}
	if req.Path != "/local/docs/old.txt" || req.Name != "new.txt" {
		t.Fatalf("rename payload = %+v", req)
	}
}

// TestRenameRejectsSeparator stops a name from smuggling a path.
func TestRenameRejectsSeparator(t *testing.T) {
	_, c, done := newWriteFixture(t)
	defer done()

	if err := c.Rename(context.Background(), "docs/old.txt", "a/b.txt"); err == nil {
		t.Fatalf("Rename() accepted a name with a path separator")
	}
}

// TestRenameConflictIsFileExisted maps OpenList's 403 "exists" onto the
// dedicated sentinel a caller can recognise.
func TestRenameConflictIsFileExisted(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()
	f.renameConflict = true

	err := c.Rename(context.Background(), "docs/old.txt", "taken.txt")
	if err == nil {
		t.Fatalf("Rename() = nil, want a collision error")
	}
	if !isAlreadyExists(err) {
		t.Fatalf("isAlreadyExists(%v) = false, want true", err)
	}
}

// TestMoveGroupsByParent covers the shape of /api/fs/move: one call per source
// directory carrying the names inside it.
func TestMoveGroupsByParent(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()

	err := c.Move(context.Background(), []string{"a/one.txt", "a/two.txt", "b/three.txt"}, "dst")
	if err != nil {
		t.Fatalf("Move() = %v", err)
	}

	calls := f.calls()
	if len(calls) != 2 {
		t.Fatalf("Move made %d calls, want 2: %+v", len(calls), calls)
	}

	bySrcDir := map[string][]string{}
	for _, call := range calls {
		var req moveRequest
		if err := json.Unmarshal([]byte(call.body), &req); err != nil {
			t.Fatalf("body is not JSON: %s", call.body)
		}
		if req.DstDir != "/local/dst" {
			t.Fatalf("dst_dir = %q, want /local/dst", req.DstDir)
		}
		bySrcDir[req.SrcDir] = req.Names
	}

	if got := bySrcDir["local/a"]; len(got) != 2 || got[0] != "one.txt" || got[1] != "two.txt" {
		t.Fatalf("names grouped under /local/a = %v", got)
	}
	if got := bySrcDir["local/b"]; len(got) != 1 || got[0] != "three.txt" {
		t.Fatalf("names grouped under /local/b = %v", got)
	}
}

// TestRemoveRefusesMountRoot is the last line of defence before a policy's
// whole tree is deleted.
func TestRemoveRefusesMountRoot(t *testing.T) {
	_, c, done := newWriteFixture(t)
	defer done()

	if err := c.Remove(context.Background(), []string{""}); err == nil {
		t.Fatalf("Remove() accepted the mount root")
	}
	if err := c.Remove(context.Background(), []string{"/"}); err == nil {
		t.Fatalf("Remove() accepted the instance root")
	}
}

// TestRemoveIsIdempotentOnMissing keeps the recycle step safe to retry.
func TestRemoveIsIdempotentOnMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == apiLogin {
			exp := time.Now().Add(48 * time.Hour).Unix()
			payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(exp, 10) + `}`))
			_, _ = w.Write([]byte(`{"code":200,"data":{"token":"h.` + payload + `.s"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":500,"message":"failed to get obj: object not found"}`))
	}))
	defer srv.Close()

	c := &client{
		http:    request.NewClient(nil, request.WithLogger(testLogger{t})),
		l:       testLogger{t},
		baseURL: srv.URL,
		mount:   "local",
		user:    "admin",
		pass:    "secret",
	}

	err := c.Remove(context.Background(), []string{"gone.txt"})
	if err != nil && !isNotFound(err) {
		t.Fatalf("Remove() on a missing object = %v, want nil or a not-found error", err)
	}
}

// TestPutStreamSendsFileHeader covers the streaming endpoint: the destination
// travels in the File-Path header, not in a JSON body.
func TestPutStreamSendsFileHeader(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()

	content := "hello openlist"
	if err := c.PutStream(context.Background(), "docs/a.txt",
		strings.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("PutStream() = %v", err)
	}

	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("PutStream made %d calls, want 1", len(calls))
	}

	call := calls[0]
	if call.method != http.MethodPut {
		t.Fatalf("method = %s, want PUT", call.method)
	}
	if call.body != content {
		t.Fatalf("body = %q, want %q", call.body, content)
	}
	if got := call.header.Get("Authorization"); got == "" {
		t.Fatalf("the streamed write carried no Authorization header")
	}

	decoded, err := url.PathUnescape(call.header.Get("File-Path"))
	if err != nil {
		t.Fatalf("File-Path is not escaped properly: %v", err)
	}
	if decoded != "/local/docs/a.txt" {
		t.Fatalf("File-Path = %q, want /local/docs/a.txt", decoded)
	}
	if got := call.header.Get("Content-Length"); got != strconv.Itoa(len(content)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(content))
	}
}

// TestMultipartChunkCarriesSessionHeaders covers the chunk endpoint: the session
// and the index travel in headers, the bytes in the body.
func TestMultipartChunkCarriesSessionHeaders(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()

	// Warm the token so the chunk call does not spend its 401 retry on login.
	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login() = %v", err)
	}
	f.requests = nil

	payload := "chunk-bytes"
	if err := c.MultipartChunk(context.Background(), "up-1", 2,
		strings.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("MultipartChunk() = %v", err)
	}

	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("MultipartChunk made %d calls, want 1", len(calls))
	}

	if got := calls[0].header.Get("X-Upload-Id"); got != "up-1" {
		t.Fatalf("X-Upload-Id = %q, want up-1", got)
	}
	if got := calls[0].header.Get("X-Chunk-Index"); got != "2" {
		t.Fatalf("X-Chunk-Index = %q, want 2", got)
	}
	if calls[0].body != payload {
		t.Fatalf("chunk body = %q, want %q", calls[0].body, payload)
	}
}

// TestMultipartInitPassesSizeAndChunk covers the init contract, including the
// chunk size the driver wishes for.
func TestMultipartInitPassesSizeAndChunk(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()

	session, err := c.MultipartInit(context.Background(), "docs/big.bin", 5*minChunkSize, 2*minChunkSize)
	if err != nil {
		t.Fatalf("MultipartInit() = %v", err)
	}

	if session.UploadID != "up-1" {
		t.Fatalf("upload id = %q, want up-1", session.UploadID)
	}
	if session.ChunkSize != f.chunkSize {
		t.Fatalf("chunk size = %d, want the server's %d", session.ChunkSize, f.chunkSize)
	}

	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("MultipartInit made %d calls, want 1", len(calls))
	}
	if got := calls[0].header.Get("X-File-Size"); got != strconv.FormatInt(5*minChunkSize, 10) {
		t.Fatalf("X-File-Size = %q", got)
	}
	if got := calls[0].header.Get("X-Chunk-Size"); got != strconv.FormatInt(2*minChunkSize, 10) {
		t.Fatalf("X-Chunk-Size = %q", got)
	}
}

// TestMultipartInitOmitsTinyChunk covers the lower bound: asking for less than
// 1 MB must not be sent, so OpenList keeps its own default.
func TestMultipartInitOmitsTinyChunk(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()

	if _, err := c.MultipartInit(context.Background(), "docs/a.bin", 4*minChunkSize, 1024); err != nil {
		t.Fatalf("MultipartInit() = %v", err)
	}

	calls := f.calls()
	if got := calls[0].header.Get("X-Chunk-Size"); got != "" {
		t.Fatalf("X-Chunk-Size = %q, want none for a value under 1 MB", got)
	}
}

// TestMultipartStatusFindsSession covers resume: the session is looked up by
// target path and total size, so no id has to survive a restart.
func TestMultipartStatusFindsSession(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()
	f.status = &MultipartSession{UploadID: "up-9", Size: 4096, ChunkSize: minChunkSize, ReceivedByte: 1024}

	session, err := c.MultipartStatus(context.Background(), "docs/a.bin", 4096)
	if err != nil {
		t.Fatalf("MultipartStatus() = %v", err)
	}
	if session.UploadID != "up-9" {
		t.Fatalf("upload id = %q, want up-9", session.UploadID)
	}

	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("MultipartStatus made %d calls, want 1", len(calls))
	}

	query, err := url.ParseQuery(calls[0].query)
	if err != nil {
		t.Fatalf("query is not parseable: %v", err)
	}
	if got := query.Get("path"); got != "/local/docs/a.bin" {
		t.Fatalf("path = %q", got)
	}
	if got := query.Get("size"); got != "4096" {
		t.Fatalf("size = %q", got)
	}
}

// TestMultipartStatusParsesReceivedRanges covers the field a resumed upload
// relies on: without it every retry would resend chunks OpenList already holds.
func TestMultipartStatusParsesReceivedRanges(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()
	f.status = &MultipartSession{
		UploadID:     "up-7",
		Size:         8 * minChunkSize,
		ChunkSize:    minChunkSize,
		ReceivedByte: 2 * minChunkSize,
		Received:     [][2]int{{0, 0}, {2, 2}},
	}

	session, err := c.MultipartStatus(context.Background(), "docs/big.bin", 8*minChunkSize)
	if err != nil {
		t.Fatalf("MultipartStatus() = %v", err)
	}

	for _, index := range []int{0, 2} {
		if !session.HasChunk(index) {
			t.Fatalf("chunk %d should count as received, got ranges %v", index, session.Received)
		}
	}
	for _, index := range []int{1, 3, 7} {
		if session.HasChunk(index) {
			t.Fatalf("chunk %d should be missing, got ranges %v", index, session.Received)
		}
	}
	if session.Done() {
		t.Fatalf("an eight-chunk upload holding two chunks must not count as done")
	}
}

// TestMultipartStatusReportsNoSession lets the caller fall back to a fresh
// upload instead of hanging.
func TestMultipartStatusReportsNoSession(t *testing.T) {
	_, c, done := newWriteFixture(t)
	defer done()

	if _, err := c.MultipartStatus(context.Background(), "docs/a.bin", 4096); err == nil {
		t.Fatalf("MultipartStatus() = nil, want a not-found error")
	}
}

// TestMultipartAbortIsIdempotent keeps a cancelled session from failing twice.
func TestMultipartAbortIsIdempotent(t *testing.T) {
	_, c, done := newWriteFixture(t)
	defer done()

	if err := c.MultipartAbort(context.Background(), ""); err != nil {
		t.Fatalf("MultipartAbort(\"\") = %v, want nil", err)
	}
}

// TestWriteEndpointsRefuseTraversal stops every write path from leaving the
// policy's mount before anything is sent to OpenList.
func TestWriteEndpointsRefuseTraversal(t *testing.T) {
	f, c, done := newWriteFixture(t)
	defer done()

	ctx := context.Background()

	if err := c.Mkdir(ctx, "../escape"); err == nil {
		t.Fatalf("Mkdir() accepted a traversal path")
	}
	if err := c.Rename(ctx, "../escape.txt", "a.txt"); err == nil {
		t.Fatalf("Rename() accepted a traversal path")
	}
	if err := c.Move(ctx, []string{"../escape.txt"}, "dst"); err == nil {
		t.Fatalf("Move() accepted a traversal path")
	}
	if err := c.Remove(ctx, []string{"../escape.txt"}); err == nil {
		t.Fatalf("Remove() accepted a traversal path")
	}
	if err := c.PutStream(ctx, "../escape.txt", strings.NewReader("x"), 1); err == nil {
		t.Fatalf("PutStream() accepted a traversal path")
	}

	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("a traversal path reached OpenList %d times: %+v", len(calls), calls)
	}
}

// TestMultipartSessionDone covers the completion check used to skip work.
func TestMultipartSessionDone(t *testing.T) {
	cases := []struct {
		name    string
		session *MultipartSession
		want    bool
	}{
		{"nil", nil, false},
		{"receiving", &MultipartSession{State: "receiving", Size: 10, ReceivedByte: 4}, false},
		{"completed state", &MultipartSession{State: "completed", Size: 10}, true},
		{"all bytes", &MultipartSession{State: "receiving", Size: 10, ReceivedByte: 10}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.session.Done(); got != tc.want {
				t.Fatalf("Done() = %v, want %v", got, tc.want)
			}
		})
	}
}

// stubClient records which client calls uploadMultipart makes, so the resume
// and completion decisions can be asserted without a live instance.
type stubClient struct {
	Client

	initSession *MultipartSession
	statusAfter *MultipartSession

	chunks   []int
	skipped  []byte
	complete int
}

func (s *stubClient) Mkdir(context.Context, string) error { return nil }

func (s *stubClient) MultipartInit(_ context.Context, _ string, size, chunkSize int64) (*MultipartSession, error) {
	session := *s.initSession
	session.Size = size
	if session.ChunkSize == 0 {
		session.ChunkSize = chunkSize
	}
	return &session, nil
}

func (s *stubClient) MultipartChunk(_ context.Context, _ string, index int, body io.Reader, _ int64) error {
	s.chunks = append(s.chunks, index)
	_, _ = io.Copy(io.Discard, body)
	return nil
}

func (s *stubClient) MultipartComplete(context.Context, string) error {
	s.complete++
	return nil
}

func (s *stubClient) MultipartStatus(context.Context, string, int64) (*MultipartSession, error) {
	if s.statusAfter == nil {
		return nil, errors.New("multipart upload session not found")
	}
	return s.statusAfter, nil
}

// newUploadRequest builds the minimal request uploadMultipart inspects.
func newUploadRequest(savePath, content string) *fs.UploadRequest {
	return &fs.UploadRequest{
		Props: &fs.UploadProps{
			SavePath: savePath,
			Size:     int64(len(content)),
		},
		File: io.NopCloser(strings.NewReader(content)),
	}
}

// TestUploadMultipartSkipsReceivedChunks is the point of resuming: chunks
// OpenList already holds must not be sent again after an interruption.
func TestUploadMultipartSkipsReceivedChunks(t *testing.T) {
	stub := &stubClient{
		initSession: &MultipartSession{
			UploadID:     "up-3",
			State:        "receiving",
			ChunkSize:    minChunkSize,
			ReceivedByte: 2 * minChunkSize,
			Received:     [][2]int{{0, 1}},
		},
		statusAfter: &MultipartSession{State: "completed", Size: 4 * minChunkSize, ReceivedByte: 4 * minChunkSize},
	}

	handler := &Driver{client: stub, l: testLogger{t}, chunkSize: minChunkSize}
	content := strings.Repeat("x", 4*int(minChunkSize))

	if err := handler.uploadMultipart(context.Background(), newUploadRequest("docs/big.bin", content)); err != nil {
		t.Fatalf("uploadMultipart() = %v", err)
	}

	if want := []int{2, 3}; !equalInts(stub.chunks, want) {
		t.Fatalf("chunks sent = %v, want %v (0-1 were already on OpenList)", stub.chunks, want)
	}
	if stub.complete != 0 {
		t.Fatalf("uploadMultipart called complete %d times although the object assembled itself", stub.complete)
	}
}

// TestUploadMultipartCompletesWhenStillPending keeps the explicit call for the
// case where OpenList has every chunk but has not promoted the object yet.
func TestUploadMultipartCompletesWhenStillPending(t *testing.T) {
	stub := &stubClient{
		initSession: &MultipartSession{
			UploadID:  "up-4",
			State:     "receiving",
			ChunkSize: minChunkSize,
		},
		statusAfter: &MultipartSession{State: "receiving", Size: 2 * minChunkSize, ReceivedByte: 2 * minChunkSize},
	}

	handler := &Driver{client: stub, l: testLogger{t}, chunkSize: minChunkSize}
	content := strings.Repeat("y", 2*int(minChunkSize))

	if err := handler.uploadMultipart(context.Background(), newUploadRequest("docs/big.bin", content)); err != nil {
		t.Fatalf("uploadMultipart() = %v", err)
	}

	if want := []int{0, 1}; !equalInts(stub.chunks, want) {
		t.Fatalf("chunks sent = %v, want %v", stub.chunks, want)
	}
	if stub.complete != 1 {
		t.Fatalf("complete called %d times, want 1", stub.complete)
	}
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
