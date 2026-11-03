package openlist

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

// MultipartSession is the state of one multipart upload as OpenList reports it.
//
// ChunkSize is authoritative: OpenList caps the value at its own configured
// MultipartChunkSize (10 MB by default) and raises anything below 1 MB, so the
// chunk size this site offers the browser is always the one returned here, never
// the one it asked for.
type MultipartSession struct {
	UploadID     string `json:"upload_id"`
	State        string `json:"state"`
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	ChunkSize    int64  `json:"chunk_size"`
	TotalChunks  int    `json:"total_chunks"`
	ReceivedByte int64  `json:"received_bytes"`
	Frontier     int    `json:"frontier"`
	Resumed      bool   `json:"resumed"`
	// Received lists the inclusive chunk index ranges OpenList already holds,
	// as [first,last] pairs. It is what lets a resumed upload skip work.
	Received [][2]int `json:"received"`
}

// multipartStateCompleted is the state OpenList reports once it has assembled
// the object and dropped the session.
const multipartStateCompleted = "completed"

// Done reports whether the session already holds the whole object.
func (m *MultipartSession) Done() bool {
	return m != nil && (m.State == multipartStateCompleted || (m.Size > 0 && m.ReceivedByte >= m.Size))
}

// HasChunk reports whether the chunk at index was already received.
func (m *MultipartSession) HasChunk(index int) bool {
	if m == nil {
		return false
	}
	for _, span := range m.Received {
		if index >= span[0] && index <= span[1] {
			return true
		}
	}
	return false
}

// mkdirRequest is the payload of /api/fs/mkdir.
type mkdirRequest struct {
	Path string `json:"path"`
}

// renameRequest is the payload of /api/fs/rename.
type renameRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// moveRequest is the payload of /api/fs/move.
type moveRequest struct {
	SrcDir string   `json:"src_dir"`
	DstDir string   `json:"dst_dir"`
	Names  []string `json:"names"`
}

// removeRequest is the payload of /api/fs/remove.
type removeRequest struct {
	Dir   string   `json:"dir"`
	Names []string `json:"names"`
}

// resolved returns the OpenList-side path for a user-facing one, rejecting
// traversal before the mount prefix is applied.
func (c *client) resolved(p string) (string, error) {
	if err := validateUserPath(p); err != nil {
		return "", err
	}
	return joinMount(c.mount, p), nil
}

// Mkdir creates the directory at p together with any missing parent.
//
// OpenList's mkdir refuses to create parents, so every level is created in turn
// from the top down. Creating a level that already exists is a success: the
// caller only wants the folder present.
func (c *client) Mkdir(ctx context.Context, p string) error {
	target, err := c.resolved(p)
	if err != nil {
		return err
	}

	// Walk the path under the mount prefix one segment at a time. The mount
	// point itself is never created: it is the administrator's own storage.
	segments := strings.Split(strings.Trim(target, "/"), "/")
	current := ""
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		if current == "" {
			current = "/" + segment
		} else {
			current += "/" + segment
		}

		// Stop at and below the mount point: only paths the policy did not
		// anchor are candidates for creation, and the mount root always exists.
		if underMount(c.mount, current) {
			continue
		}

		if err := c.post(ctx, apiMkdir, &mkdirRequest{Path: current}, nil, ""); err != nil {
			if isAlreadyExists(err) {
				continue
			}
			return err
		}
	}

	return nil
}

// underMount reports whether the absolute OpenList path p is the mount point or
// one of its ancestors, meaning it already exists on the instance and must not
// be created.
func underMount(mount, p string) bool {
	p = normalizePath(p)
	mount = normalizePath(mount)
	if p == "" {
		return true
	}
	if mount == "" {
		return false
	}

	return p == mount || strings.HasPrefix(mount, p+"/")
}

// Rename renames the entry at p to newName without moving it.
func (c *client) Rename(ctx context.Context, p, newName string) error {
	target, err := c.resolved(p)
	if err != nil {
		return err
	}

	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("%w: empty name", ErrPathEscaped)
	}
	if strings.ContainsAny(newName, "/\\") {
		return fmt.Errorf("%w: a new name may not contain a path separator", ErrPathEscaped)
	}

	return c.post(ctx, apiRename, &renameRequest{Path: target, Name: newName}, nil, "")
}

// Move moves every entry at src into the directory dstDir, keeping its name.
func (c *client) Move(ctx context.Context, src []string, dstDir string) error {
	if len(src) == 0 {
		return nil
	}

	dst, err := c.resolved(dstDir)
	if err != nil {
		return err
	}

	// /api/fs/move works on one source directory plus the names inside it, so
	// group the sources by their parent and issue one call per parent.
	byParent := make(map[string][]string)
	order := make([]string, 0, len(src))
	for _, s := range src {
		target, err := c.resolved(s)
		if err != nil {
			return err
		}

		parent := parentOf(target)
		if _, seen := byParent[parent]; !seen {
			order = append(order, parent)
		}
		byParent[parent] = append(byParent[parent], pathBase(target))
	}

	for _, parent := range order {
		if err := c.post(ctx, apiMove, &moveRequest{
			SrcDir: parent,
			DstDir: dst,
			Names:  byParent[parent],
		}, nil, ""); err != nil {
			return err
		}
	}

	return nil
}

// Remove deletes every entry at the given paths. OpenList treats a missing entry
// as success, which is the idempotent behaviour a recycle step needs.
func (c *client) Remove(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	byParent := make(map[string][]string)
	order := make([]string, 0, len(paths))
	for _, p := range paths {
		if normalizePath(p) == "" {
			return fmt.Errorf("%w: refusing to remove the mount root", ErrPathEscaped)
		}

		target, err := c.resolved(p)
		if err != nil {
			return err
		}
		if target == "/" {
			return fmt.Errorf("%w: refusing to remove the instance root", ErrPathEscaped)
		}

		parent := parentOf(target)
		if _, seen := byParent[parent]; !seen {
			order = append(order, parent)
		}
		byParent[parent] = append(byParent[parent], pathBase(target))
	}

	for _, parent := range order {
		if err := c.post(ctx, apiRemove, &removeRequest{
			Dir:   parent,
			Names: byParent[parent],
		}, nil, ""); err != nil {
			return err
		}
	}

	return nil
}

// PutStream writes a whole object in one request.
//
// /api/fs/put takes the destination in the File-Path header and the content as
// the raw body. It is the only endpoint that can write without a prior metadata
// exchange, which is what the WebDAV and relay paths need.
func (c *client) PutStream(ctx context.Context, p string, body io.Reader, size int64) error {
	target, err := c.resolved(p)
	if err != nil {
		return err
	}

	header := http.Header{}
	header.Set("File-Path", url.PathEscape(target))
	header.Set("Content-Type", "application/octet-stream")
	if size > 0 {
		header.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	// OpenList refreshes an existing object rather than refusing the write,
	// which matches fs.ModeOverwrite for the callers that use this path.
	header.Set("Overwrite", "true")

	// The body is streamed, so the call gets no overall timeout beyond the
	// request context: a large upload legitimately runs for minutes.
	return c.attemptWithHeaders(ctx, http.MethodPut, apiPut, "", body, size, header, nil, true)
}

// MultipartInit opens a session for p, resuming a live one when OpenList still
// holds it.
func (c *client) MultipartInit(ctx context.Context, p string, size, chunkSize int64) (*MultipartSession, error) {
	target, err := c.resolved(p)
	if err != nil {
		return nil, err
	}

	header := http.Header{}
	header.Set("File-Path", url.PathEscape(target))
	header.Set("X-File-Size", strconv.FormatInt(size, 10))
	if chunkSize >= minChunkSize {
		header.Set("X-Chunk-Size", strconv.FormatInt(chunkSize, 10))
	}
	header.Set("Overwrite", "true")

	var out MultipartSession
	if err := c.attemptWithHeaders(ctx, http.MethodPost, apiMultipartInit, "",
		http.NoBody, 0, header, &out, true); err != nil {
		return nil, err
	}

	if out.UploadID == "" {
		return nil, fmt.Errorf("openlist opened no multipart session for %q", target)
	}

	return &out, nil
}

// MultipartChunk uploads one chunk. Re-sending a chunk is harmless: OpenList
// records it as already received and does not count it twice.
func (c *client) MultipartChunk(ctx context.Context, uploadID string, index int, body io.Reader, size int64) error {
	if uploadID == "" {
		return fmt.Errorf("no multipart upload id")
	}
	if index < 0 {
		return fmt.Errorf("invalid chunk index %d", index)
	}

	header := http.Header{}
	header.Set("X-Upload-Id", uploadID)
	header.Set("X-Chunk-Index", strconv.Itoa(index))
	header.Set("Content-Type", "application/octet-stream")

	return c.attemptWithHeaders(ctx, http.MethodPut, apiMultipartChunk, "", body, size, header, nil, true)
}

// MultipartComplete assembles the received chunks into the final object.
func (c *client) MultipartComplete(ctx context.Context, uploadID string) error {
	if uploadID == "" {
		return fmt.Errorf("no multipart upload id")
	}

	header := http.Header{}
	header.Set("X-Upload-Id", uploadID)

	return c.attemptWithHeaders(ctx, http.MethodPost, apiMultipartComplete, "",
		http.NoBody, 0, header, nil, true)
}

// MultipartAbort discards the session and everything received so far.
func (c *client) MultipartAbort(ctx context.Context, uploadID string) error {
	if uploadID == "" {
		return nil
	}

	header := http.Header{}
	header.Set("X-Upload-Id", uploadID)

	err := c.attemptWithHeaders(ctx, http.MethodPost, apiMultipartAbort, "",
		http.NoBody, 0, header, nil, true)
	if err != nil && isNotFound(err) {
		// Already gone, discarding it again is a success.
		return nil
	}

	return err
}

// MultipartStatus looks a live session up by target path and total size.
func (c *client) MultipartStatus(ctx context.Context, p string, size int64) (*MultipartSession, error) {
	target, err := c.resolved(p)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf("path=%s&size=%d", url.QueryEscape(target), size)

	var out MultipartSession
	if err := c.do(ctx, http.MethodGet, apiMultipartStatus, query, nil, &out, true); err != nil {
		return nil, err
	}

	if out.UploadID == "" {
		return nil, fmt.Errorf("%w: no live multipart session", ErrFileNotFound)
	}

	return &out, nil
}

// attemptWithHeaders is the raw-body counterpart of attempt: it sends body with
// explicit method-specific headers, applying the same login-on-401 retry as the
// JSON endpoints so a 48 hour token expiry stays invisible mid-upload.
func (c *client) attemptWithHeaders(ctx context.Context, method, api, query string,
	body io.Reader, size int64, header http.Header, out any, authenticated bool) error {
	err := c.rawAttempt(ctx, method, api, query, body, size, header, out, authenticated)
	if err == nil || !authenticated || !isAuthExpired(err) {
		return err
	}

	c.invalidate()
	if loginErr := c.Login(ctx); loginErr != nil {
		c.report(ctx, RefreshErrorKindLoginFailed, describeLoginError(loginErr))
		return loginErr
	}

	c.report(ctx, "", "")

	return c.rawAttempt(ctx, method, api, query, body, size, header, out, authenticated)
}

// rawAttempt performs one round trip with a streamed body.
func (c *client) rawAttempt(ctx context.Context, method, api, query string,
	body io.Reader, size int64, header http.Header, out any, authenticated bool) error {
	target := c.baseURL + api
	if query != "" {
		target += "?" + query
	}

	reqHeader := http.Header{}
	for k, v := range header {
		reqHeader[k] = v
	}
	reqHeader.Set("Accept", "application/json")

	if authenticated {
		token, err := c.tokenFor(ctx)
		if err != nil {
			return err
		}
		reqHeader.Set("Authorization", token)
	}

	// Only the JSON calls are bounded by the short timeout; a body transfer
	// runs as long as the caller's context allows.
	callCtx := ctx
	if body == nil || body == http.NoBody {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, httpCallTimeout)
		defer cancel()
	}

	opts := []request.Option{
		request.WithContext(callCtx),
		request.WithHeader(reqHeader),
	}
	if size > 0 {
		opts = append(opts, request.WithContentLength(size))
	}

	res := c.http.Request(method, target, body, opts...)
	if res.Err != nil {
		return fmt.Errorf("failed to request %s: %w", api, res.Err)
	}

	raw, err := res.GetResponse()
	if err != nil {
		return fmt.Errorf("failed to read response of %s: %w", api, err)
	}

	return decode(api, res.Response.StatusCode, raw, out)
}

// minChunkSize is OpenList's lower bound for a multipart chunk.
const minChunkSize = 1024 * 1024

// pathBase returns the last segment of a normalized OpenList path.
func pathBase(p string) string {
	p = strings.TrimSuffix(p, "/")
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

// isAuthExpired reports whether err means the token was not accepted.
func isAuthExpired(err error) bool {
	return err != nil && errors.Is(err, ErrAuthExpired)
}

// isNotFound reports whether err means the object does not exist.
func isNotFound(err error) bool {
	return err != nil && errors.Is(err, ErrFileNotFound)
}

// isAlreadyExists reports whether err means the object is already there, which
// every create-style call treats as success.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	msg := strings.ToLower(apiErr.Message)
	return strings.Contains(msg, "exist")
}

// uploadTimeout bounds a whole multipart or streaming upload session.
const uploadTimeout = 12 * time.Hour
