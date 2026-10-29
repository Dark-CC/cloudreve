package y139

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"
)

// FileInfo is one entry returned by /file/list.
type FileInfo struct {
	FileID       string `json:"fileId"`
	ParentFileID string `json:"parentFileId"`
	Name         string `json:"name"`
	Type         string `json:"type"` // "file" or "folder"
	Size         int64  `json:"size"`
	RevisionID   string `json:"revisionId"`
	// The drive sends timestamps as RFC3339 with milliseconds.
	UpdatedAt string `json:"updatedAt"`
	CreatedAt string `json:"createdAt"`
}

// IsDir reports whether this entry is a folder.
func (f *FileInfo) IsDir() bool {
	return f.Type == fileTypeFolder
}

// ModifiedAt parses the entry's modification time, falling back to a zero value
// when the drive sends an unparsable timestamp.
func (f *FileInfo) ModifiedAt() time.Time {
	for _, raw := range []string{f.UpdatedAt, f.CreatedAt} {
		if raw == "" {
			continue
		}
		for _, layout := range []string{
			"2006-01-02T15:04:05.999-07:00",
			"2006-01-02T15:04:05-07:00",
			"2006-01-02 15:04:05",
		} {
			if ts, err := time.Parse(layout, raw); err == nil {
				return ts
			}
		}
	}
	return time.Time{}
}

const (
	fileTypeFile   = "file"
	fileTypeFolder = "folder"
)

// listRequest is the payload of /file/list.
type listRequest struct {
	ImageThumbnailStyleList []string `json:"imageThumbnailStyleList"`
	OrderBy                 string   `json:"orderBy"`
	OrderDirection          string   `json:"orderDirection"`
	PageInfo                pageInfo `json:"pageInfo"`
	ParentFileID            string   `json:"parentFileId"`
}

type pageInfo struct {
	PageCursor string `json:"pageCursor"`
	PageSize   int    `json:"pageSize"`
}

// listResponse is the data of /file/list.
type listResponse struct {
	Items          []FileInfo `json:"items"`
	NextPageCursor string     `json:"nextPageCursor"`
}

// downloadURLRequest is the payload of /file/getDownloadUrl.
type downloadURLRequest struct {
	FileID string `json:"fileId"`
}

// DownloadURL is the response of /file/getDownloadUrl. The drive returns both a
// signed object-storage URL and an optional CDN URL, which is only usable when
// cdnSwitch is on.
type DownloadURL struct {
	URL        string `json:"url"`
	CDNURL     string `json:"cdnUrl"`
	CDNSwitch  bool   `json:"cdnSwitch"`
	Expiration string `json:"expiration"`
}

// Best returns the most suitable download URL.
func (d *DownloadURL) Best() string {
	if d.CDNSwitch && d.CDNURL != "" {
		return d.CDNURL
	}
	return d.URL
}

// normalizePath makes a save path comparable: leading and trailing slashes
// stripped, empty means root.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "." {
		return ""
	}
	return p
}

// ListChildren lists every direct child of parentFileID, following the cursor.
func (c *client) ListChildren(ctx context.Context, parentFileID string) ([]FileInfo, error) {
	if parentFileID == "" {
		parentFileID = c.rootFileID
	}

	var (
		items  []FileInfo
		cursor string
	)

	for page := 0; page < 10000; page++ {
		req := &listRequest{
			ImageThumbnailStyleList: []string{"Small", "Large"},
			OrderBy:                 "updated_at",
			OrderDirection:          "DESC",
			PageInfo: pageInfo{
				PageCursor: cursor,
				PageSize:   listPageSize,
			},
			ParentFileID: parentFileID,
		}

		var res listResponse
		if err := c.call(ctx, apiList, req, &res); err != nil {
			return nil, err
		}

		items = append(items, res.Items...)

		if res.NextPageCursor == "" || res.NextPageCursor == cursor || len(res.Items) == 0 {
			break
		}
		cursor = res.NextPageCursor
	}

	return items, nil
}

// resolvePath walks the given path segment by segment and returns the file ID of
// the last component. An empty path resolves to the configured root.
func (c *client) ResolvePath(ctx context.Context, savePath string) (string, error) {
	savePath = normalizePath(savePath)
	if savePath == "" {
		return c.rootFileID, nil
	}

	segments := strings.Split(savePath, "/")
	// Start from the configured root, which may be a sub folder of the drive.
	current := c.rootFileID

	for i, segment := range segments {
		children, err := c.ListChildren(ctx, current)
		if err != nil {
			return "", err
		}

		var hit *FileInfo
		for j := range children {
			if children[j].Name != segment {
				continue
			}
			// Intermediate segments must be folders, the last may be either.
			if i < len(segments)-1 && !children[j].IsDir() {
				continue
			}
			hit = &children[j]
			break
		}

		if hit == nil {
			return "", fmt.Errorf("%w: %q", ErrFileNotFound, savePath)
		}

		current = hit.FileID
	}

	return current, nil
}

// ParentOf resolves the file ID of the parent folder of savePath.
func (c *client) ParentOf(ctx context.Context, savePath string) (string, error) {
	parent := path.Dir(normalizePath(savePath))
	if parent == "." || parent == "/" {
		return c.rootFileID, nil
	}
	return c.ResolvePath(ctx, parent)
}

// EnsureFolder resolves a folder path, creating every missing segment.
func (c *client) EnsureFolder(ctx context.Context, savePath string) (string, error) {
	savePath = normalizePath(savePath)
	if savePath == "" {
		return c.rootFileID, nil
	}

	segments := strings.Split(savePath, "/")
	current := c.rootFileID

	for _, segment := range segments {
		if segment == "" {
			continue
		}

		children, err := c.ListChildren(ctx, current)
		if err != nil {
			return "", err
		}

		var existing *FileInfo
		for j := range children {
			if children[j].Name == segment && children[j].IsDir() {
				existing = &children[j]
				break
			}
		}

		if existing != nil {
			current = existing.FileID
			continue
		}

		created, err := c.createFolder(ctx, current, segment)
		if err != nil {
			return "", err
		}
		current = created
	}

	return current, nil
}

// createFolderRequest is the payload of a folder /file/create call.
type createFolderRequest struct {
	ParentFileID   string `json:"parentFileId"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Type           string `json:"type"`
	FileRenameMode string `json:"fileRenameMode"`
}

// createFolder creates one folder and returns its file ID.
func (c *client) createFolder(ctx context.Context, parentFileID, name string) (string, error) {
	var res struct {
		FileID string `json:"fileId"`
	}

	if err := c.call(ctx, apiCreate, &createFolderRequest{
		ParentFileID:   parentFileID,
		Name:           name,
		Type:           fileTypeFolder,
		FileRenameMode: "force_rename",
	}, &res); err != nil {
		return "", err
	}

	if res.FileID == "" {
		return "", fmt.Errorf("cloud drive did not return the new folder ID for %q", name)
	}

	return res.FileID, nil
}

// Meta looks a path up in its parent's listing. The drive has no HEAD request,
// the parent listing is the only way to read an entry's size.
func (c *client) Meta(ctx context.Context, savePath string) (*FileInfo, error) {
	savePath = normalizePath(savePath)
	if savePath == "" {
		return nil, fmt.Errorf("%w: empty path", ErrFileNotFound)
	}

	parentID, err := c.ParentOf(ctx, savePath)
	if err != nil {
		return nil, err
	}

	name := path.Base(savePath)
	children, err := c.ListChildren(ctx, parentID)
	if err != nil {
		return nil, err
	}

	for i := range children {
		if children[i].Name == name {
			return &children[i], nil
		}
	}

	return nil, fmt.Errorf("%w: %q", ErrFileNotFound, savePath)
}

// GetDownloadURL returns a pre-signed download URL for the given file ID.
func (c *client) GetDownloadURL(ctx context.Context, fileID string) (*DownloadURL, error) {
	if fileID == "" {
		return nil, fmt.Errorf("%w: empty file id", ErrFileNotFound)
	}

	var res DownloadURL
	if err := c.call(ctx, apiDownloadURL, &downloadURLRequest{FileID: fileID}, &res); err != nil {
		return nil, err
	}

	if res.Best() == "" {
		return nil, fmt.Errorf("cloud drive returned an empty download URL")
	}

	return &res, nil
}

// trashRequest is the payload of /recyclebin/batchTrash.
type trashRequest struct {
	FileIDs []string `json:"fileIds"`
}

// Trash moves the given file IDs to the drive's recycle bin.
func (c *client) Trash(ctx context.Context, fileIDs []string) error {
	if len(fileIDs) == 0 {
		return nil
	}

	for start := 0; start < len(fileIDs); start += deleteBatchSize {
		end := start + deleteBatchSize
		if end > len(fileIDs) {
			end = len(fileIDs)
		}

		if err := c.call(ctx, apiBatchTrash, &trashRequest{FileIDs: fileIDs[start:end]}, nil); err != nil {
			return err
		}
	}

	return nil
}

// renameRequest is the payload of /file/update.
type renameRequest struct {
	FileID      string `json:"fileId"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Rename changes the name of one entry.
func (c *client) Rename(ctx context.Context, fileID, name string) error {
	if fileID == "" || name == "" {
		return fmt.Errorf("file id and name are required to rename an object")
	}

	return c.call(ctx, apiUpdate, &renameRequest{FileID: fileID, Name: name}, nil)
}
