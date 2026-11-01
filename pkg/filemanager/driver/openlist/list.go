package openlist

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// FileInfo is one entry returned by /api/fs/list and /api/fs/get.
type FileInfo struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
	Created  string `json:"created"`
	// RawURL is the download address OpenList hands out. On a cloud-drive
	// backend it points straight at the underlying storage, so the browser can
	// fetch it without Cloudreve proxying a single byte.
	RawURL string `json:"raw_url"`
	Sign   string `json:"sign"`
	Type   int    `json:"type"`
}

// ModifiedAt parses the entry's modification time, falling back to a zero value
// when OpenList sends an unparsable timestamp.
func (f *FileInfo) ModifiedAt() time.Time {
	for _, raw := range []string{f.Modified, f.Created} {
		if raw == "" {
			continue
		}
		for _, layout := range []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05.999999999Z07:00",
			"2006-01-02 15:04:05",
		} {
			if ts, err := time.Parse(layout, raw); err == nil {
				return ts
			}
		}
	}
	return time.Time{}
}

// listRequest is the payload of /api/fs/list.
type listRequest struct {
	Path    string `json:"path"`
	Page    int    `json:"page"`
	PerPage int    `json:"per_page"`
	Refresh bool   `json:"refresh"`
}

// listResponse is the data of /api/fs/list. Content holds only this page's
// entries; Total is the size of the whole directory.
type listResponse struct {
	Content []FileInfo `json:"content"`
	Total   int        `json:"total"`
}

// getRequest is the payload of /api/fs/get.
type getRequest struct {
	Path string `json:"path"`
}

// normalizePath makes a path comparable: leading and trailing slashes stripped,
// empty means root.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "." {
		return ""
	}
	return p
}

// fullPath joins the policy's mount path with a site-relative path. This is the
// only place the two are combined, so every request to OpenList goes through
// the same prefixing rule.
//
// A path that escapes the mount point (a ".." segment) is refused here rather
// than sent to OpenList, which would reject it anyway with
// "403 using relative path is not allowed".
func (c *client) fullPath(rel string) (string, error) {
	rel = normalizePath(rel)

	for _, segment := range strings.Split(rel, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%w: %q", ErrInvalidPath, rel)
		}
	}

	joined := path.Join(c.mountPath, rel)
	if joined == "." || joined == "/" {
		return "/", nil
	}

	return "/" + strings.Trim(joined, "/"), nil
}

// ListChildren lists every direct child of the given OpenList virtual path.
func (c *client) ListChildren(ctx context.Context, dirPath string) ([]FileInfo, error) {
	dirPath = normalizePath(dirPath)

	full, err := c.fullPath(dirPath)
	if err != nil {
		return nil, err
	}

	var (
		items []FileInfo
		seen  int
	)

	for page := 1; page <= listMaxPages; page++ {
		req := &listRequest{
			Path:    full,
			Page:    page,
			PerPage: listPageSize,
			Refresh: page == 1,
		}

		var res listResponse
		if err := c.call(ctx, apiList, req, &res); err != nil {
			return nil, err
		}

		items = append(items, res.Content...)
		seen += len(res.Content)

		// An empty page or a full sweep of Total ends the loop. OpenList does
		// not always report the same Total on every page, so both are checked.
		if len(res.Content) == 0 || seen >= res.Total {
			break
		}
	}

	return items, nil
}

// Get returns the entry at the given OpenList virtual path.
func (c *client) Get(ctx context.Context, filePath string) (*FileInfo, error) {
	filePath = normalizePath(filePath)

	full, err := c.fullPath(filePath)
	if err != nil {
		return nil, err
	}

	var res FileInfo
	if err := c.call(ctx, apiGet, &getRequest{Path: full}, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

// Ping verifies the instance answers, the account can log in and the configured
// mount point exists. It is what the policy form runs before saving, so a typo
// in the mount path fails loudly instead of producing a policy that never lists
// a single file.
func (c *client) Ping(ctx context.Context) error {
	if _, err := c.loginToken(ctx); err != nil {
		return err
	}

	full, err := c.fullPath("")
	if err != nil {
		return err
	}

	var res listResponse
	if err := c.call(ctx, apiList, &listRequest{
		Path:    full,
		Page:    1,
		PerPage: 1,
		Refresh: true,
	}, &res); err != nil {
		if errors.Is(err, ErrMountNotFound) {
			return fmt.Errorf("%w: %q", ErrMountNotFound, c.mountPath)
		}
		return err
	}

	return nil
}
