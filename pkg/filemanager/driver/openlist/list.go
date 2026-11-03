package openlist

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// FileInfo is one entry of an OpenList listing or a single object's metadata.
//
// OpenList returns both shapes from /api/fs/list (entries) and /api/fs/get
// (one object, plus a raw URL); the unused fields stay empty.
type FileInfo struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
	RawURL   string `json:"raw_url"`
	Sign     string `json:"sign"`
}

// ModifiedAt parses the entry's modification time, falling back to a zero value
// when OpenList sends an unparsable timestamp.
func (f *FileInfo) ModifiedAt() time.Time {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
	} {
		if ts, err := time.Parse(layout, f.Modified); err == nil {
			return ts
		}
	}
	return time.Time{}
}

// listRequest is the payload of /api/fs/list.
type listRequest struct {
	Path     string `json:"path"`
	Password string `json:"password"`
	Page     int    `json:"page"`
	PerPage  int    `json:"per_page"`
	Refresh  bool   `json:"refresh"`
}

// listResponse is the data of /api/fs/list.
type listResponse struct {
	Content []FileInfo `json:"content"`
	Total   int        `json:"total"`
}

// getRequest is the payload of /api/fs/get.
type getRequest struct {
	Path     string `json:"path"`
	Password string `json:"password"`
}

// normalizePath strips the surrounding slashes of a virtual path.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "." {
		return ""
	}
	return p
}

// hasTraversalSegment reports whether any segment of p climbs out of its
// directory. OpenList would reject such a path too, but the check has to happen
// before the mount prefix is applied: a request must never be able to leave the
// storage this policy is anchored to.
func hasTraversalSegment(p string) bool {
	for _, segment := range strings.Split(p, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

// joinMount resolves a user-facing path under the policy's mount prefix. Both
// arguments may carry any number of surrounding slashes; the result always has
// exactly one slash in front and none at the end, and "" means the mount point
// itself (OpenList addresses the root as "/").
func joinMount(mount, p string) string {
	mount = normalizePath(mount)
	p = normalizePath(p)

	joined := p
	if mount != "" {
		if joined == "" {
			joined = mount
		} else {
			joined = mount + "/" + joined
		}
	}

	return "/" + joined
}

// stripMount turns a user-facing path into the path OpenList knows: the part
// under the policy's mount prefix. A path outside the prefix is returned as-is;
// the prefix check happens elsewhere, this only removes a duplicate.
func stripMount(mount, p string) string {
	mount = normalizePath(mount)
	p = normalizePath(p)

	if mount == "" {
		return p
	}
	if p == mount {
		return ""
	}
	if strings.HasPrefix(p, mount+"/") {
		return strings.TrimPrefix(p, mount+"/")
	}

	return p
}

// validateUserPath rejects paths that must never reach OpenList.
func validateUserPath(p string) error {
	if hasTraversalSegment(p) {
		return fmt.Errorf("%w: %q", ErrPathEscaped, p)
	}
	return nil
}

// ListChildren lists every direct child of dir, following pagination until the
// reported total is covered.
func (c *client) ListChildren(ctx context.Context, dir string) ([]FileInfo, error) {
	if err := validateUserPath(dir); err != nil {
		return nil, err
	}

	target := joinMount(c.mount, dir)

	items := make([]FileInfo, 0, 64)
	for page := 0; page < 10000; page++ {
		var res listResponse
		if err := c.post(ctx, apiList, &listRequest{
			Path:    target,
			Page:    page + 1,
			PerPage: listPageSize,
		}, &res, ""); err != nil {
			return nil, err
		}

		items = append(items, res.Content...)

		// A short page is the last one; `total` is the directory size, not a
		// page counter, so it is only used to stop early once everything
		// announced has been collected.
		if len(res.Content) < listPageSize || (res.Total > 0 && len(items) >= res.Total) {
			break
		}
	}

	return items, nil
}

// GetFileInfo fetches one object's metadata, including the raw URL the browser
// is redirected to for downloads.
func (c *client) GetFileInfo(ctx context.Context, p string) (*FileInfo, error) {
	if err := validateUserPath(p); err != nil {
		return nil, err
	}

	target := joinMount(c.mount, p)

	var res FileInfo
	if err := c.post(ctx, apiGet, &getRequest{Path: target}, &res, ""); err != nil {
		return nil, err
	}

	return &res, nil
}
