package openlist

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/driver"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/mime"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

// Driver is the OpenList storage policy driver.
//
// Each site administrator runs their own OpenList instance; this driver only
// calls its HTTP API and hands the browser OpenList's own raw URL, so file
// content travels between the browser and OpenList and never through this site.
// This release is read-only: listing and downloads.
type Driver struct {
	policy   *ent.StoragePolicy
	settings setting.Provider
	config   conf.ConfigProvider
	l        logging.Logger
	mime     mime.MimeDetector
	client   Client
	mount    string
	chunkSize int64
}

var features = &boolset.BooleanSet{}

func init() {
	// HandlerCapabilityProxyRequired is deliberately NOT set: it would force
	// every download through this site and void the 302 to OpenList.
	//
	// HandlerCapabilityUploadSentinelRequired is set because OpenList exposes no
	// compliance callback a storage provider would sign: this site cannot be
	// told by OpenList that an upload finished, so a sentinel task has to clean
	// up a placeholder whose browser never came back.
	boolset.Sets(map[driver.HandlerCapability]bool{
		driver.HandlerCapabilityUploadSentinelRequired: true,
	}, features)
}

// errReadOnly is kept for the paths this release still refuses.
var errReadOnly = errors.New("this storage policy is read-only in this release")

// New creates the driver for an existing storage policy.
func New(ctx context.Context, policy *ent.StoragePolicy, settings setting.Provider,
	config conf.ConfigProvider, l logging.Logger, mime mime.MimeDetector) (*Driver, error) {
	if strings.TrimSpace(policy.Server) == "" {
		return nil, errors.New("missing OpenList address (server) for the OpenList policy")
	}

	if strings.TrimSpace(policy.AccessKey) == "" {
		return nil, errors.New("missing OpenList username (access key) for the OpenList policy")
	}

	if strings.TrimSpace(policy.SecretKey) == "" {
		return nil, errors.New("missing OpenList password (secret key) for the OpenList policy")
	}

	c := NewClient(policy, config, l)

	d := &Driver{
		policy:    policy,
		settings:  settings,
		config:    config,
		l:         l,
		mime:      mime,
		client:    c,
		mount:     c.MountPath(),
		chunkSize: policyChunkSize(policy),
	}

	if d.mount == "" {
		l.Debug("OpenList policy %d is anchored at the instance root.", policy.ID)
	} else {
		l.Debug("OpenList policy %d is anchored at %q.", policy.ID, d.mount)
	}

	return d, nil
}

// SetRefreshErrorReporter installs the callback that surfaces a failed login to
// the administrator dashboard.
func SetRefreshErrorReporter(d *Driver, fn func(ctx context.Context, kind, message string) error) {
	SetRefreshErrorReporterFn(d.client, fn)
}

// Ping verifies the configured mount path resolves on the instance, so saving a
// policy can report a bad mount path right away.
func (handler *Driver) Ping(ctx context.Context) error {
	if err := handler.client.Ping(ctx); err != nil {
		return asSerializerError(err)
	}
	return nil
}

// List walks the folder at base and returns its children, reported relative to
// base so the mount prefix never leaks into the Cloudreve file tree.
//
// base is the path the user sees, which already sits under the policy's mount
// prefix; it is stripped before the driver talks to OpenList so the prefix is
// never applied twice.
func (handler *Driver) List(ctx context.Context, base string, onProgress driver.ListProgressFunc, recursive bool) ([]fs.PhysicalObject, error) {
	base = normalizePath(base)
	if err := validateUserPath(base); err != nil {
		return nil, asSerializerError(err)
	}

	relDir := stripMount(handler.mount, base)
	res := make([]fs.PhysicalObject, 0, 64)
	if err := handler.listInto(ctx, relDir, "", recursive, &res, onProgress); err != nil {
		return nil, asSerializerError(err)
	}

	return res, nil
}

// listInto collects the children of relDir (relative to the mount point), rooted
// at relBase, and descends into folders when asked to.
func (handler *Driver) listInto(ctx context.Context, relDir, relBase string, recursive bool,
	out *[]fs.PhysicalObject, onProgress driver.ListProgressFunc) error {
	children, err := handler.client.ListChildren(ctx, relDir)
	if err != nil {
		return err
	}

	for i := range children {
		item := &children[i]
		if item.Name == "" {
			continue
		}

		rel := item.Name
		if relBase != "" {
			rel = relBase + "/" + item.Name
		}

		entry := fs.PhysicalObject{
			Name:         item.Name,
			Source:       rel,
			RelativePath: rel,
			Size:         item.Size,
			IsDir:        item.IsDir,
			LastModify:   item.ModifiedAt(),
		}
		if entry.LastModify.IsZero() {
			entry.LastModify = time.Now()
		}

		*out = append(*out, entry)

		if item.IsDir && recursive {
			if err := handler.listInto(ctx, rel, rel, recursive, out, onProgress); err != nil {
				return err
			}
		}
	}

	if onProgress != nil {
		onProgress(len(children))
	}

	return nil
}

// Open is not implemented, this driver always serves files by URL.
func (handler *Driver) Open(ctx context.Context, path string) (*os.File, error) {
	return nil, errors.New("not implemented")
}

// LocalPath is empty because files never live on this site's disk.
func (handler *Driver) LocalPath(ctx context.Context, path string) string {
	return ""
}

// Put writes one object. It is used both by the relay route (chunks streamed
// into this site) and by a single-shot write such as WebDAV PUT.
//
// The content is sent with OpenList's multipart API so a write that dies halfway
// can still finish: the session is found again by path and size, and OpenList
// reassembles it. A file without a declared size cannot be split and is written
// in one request instead.
func (handler *Driver) Put(ctx context.Context, file *fs.UploadRequest) error {
	defer file.Close()

	overwrite := file.Mode&fs.ModeOverwrite == fs.ModeOverwrite
	if !overwrite {
		if _, err := handler.client.GetFileInfo(ctx, normalizePath(file.Props.SavePath)); err == nil {
			return fs.ErrFileExisted
		} else if !isNotFound(err) {
			return asSerializerError(err)
		}
	}

	err := handler.uploadMultipart(ctx, file)
	if errors.Is(err, ErrFileNotFound) || errors.Is(err, ErrStorageNotFound) {
		// A marker for "this upload cannot be resumed here"; fall back to one
		// write rather than failing the upload outright.
		handler.l.Debug("Multipart init failed for %q, falling back to a single write: %s",
			file.Props.SavePath, err)
		err = handler.putStream(ctx, file)
	}

	if err != nil {
		return asSerializerError(err)
	}

	return nil
}

// Delete removes the objects from the storage. A missing object is a success:
// the recycle step must be idempotent.
func (handler *Driver) Delete(ctx context.Context, files ...string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}

	targets := make([]string, 0, len(files))
	for _, f := range files {
		if normalizePath(f) == "" {
			// Never remove the mount root.
			continue
		}
		targets = append(targets, f)
	}

	if err := handler.client.Remove(ctx, targets); err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return files, asSerializerError(err)
	}

	return nil, nil
}

// RenameFileOnStorage renames one object in place.
func (handler *Driver) RenameFileOnStorage(ctx context.Context, from, newName string) error {
	if err := handler.renameFileOnStorage(ctx, from, newName); err != nil {
		return asSerializerError(err)
	}
	return nil
}

// MoveFileOnStorage moves one object into a folder.
func (handler *Driver) MoveFileOnStorage(ctx context.Context, from, dstDir string) error {
	if err := handler.moveFileOnStorage(ctx, from, dstDir); err != nil {
		return asSerializerError(err)
	}
	return nil
}

// Thumb is unsupported: OpenList exposes no thumbnail API this driver uses.
func (handler *Driver) Thumb(ctx context.Context, expire *time.Time, ext string, e fs.Entity) (string, error) {
	return "", errors.New("not supported")
}

// Source returns OpenList's own raw URL for the entity, which Cloudreve hands to
// the browser as a 302. No bytes pass through this site.
func (handler *Driver) Source(ctx context.Context, e fs.Entity, args *driver.GetSourceArgs) (string, error) {
	info, err := handler.client.GetFileInfo(ctx, normalizePath(e.Source()))
	if err != nil {
		return "", asSerializerError(err)
	}

	if info.IsDir {
		return "", serializer.NewError(serializer.CodeParamErr, "cannot download a directory", nil)
	}

	if strings.TrimSpace(info.RawURL) == "" {
		return "", serializer.NewError(serializer.CodeIOFailed,
			"openlist returned no download URL for this object", nil)
	}

	return info.RawURL, nil
}

// Token refuses to hand out credentials for a browser-direct upload.
//
// OpenList's API has one kind of credential: the login token of an account,
// with that account's full reach over the whole instance. Giving it to a
// browser would let any visitor read, overwrite or delete everything the
// policy's account can touch, which is the administrator's entire storage. So
// OpenList uploads always take the relay route: chunks come into this site,
// and Put writes them with the token held in memory here. The manager decides
// that before ever calling this, and this guard only makes sure a future change
// there cannot quietly expose the token.
func (handler *Driver) Token(ctx context.Context, uploadSession *fs.UploadSession, file *fs.UploadRequest) (*fs.UploadCredential, error) {
	handler.l.Warning("Refusing a browser-direct OpenList upload for %q: the policy credential "+
		"grants full access to the instance, so the upload must relay through this site.",
		file.Props.SavePath)
	return nil, fs.ErrNotSupportedAction.WithError(
		errors.New("openlist uploads must be relayed through the server"))
}

// CompleteUpload finalizes the multipart upload on OpenList.
//
// The client has already sent every chunk; this call is what makes the object
// appear at its final path, so it must run before Cloudreve promotes the
// placeholder.
func (handler *Driver) CompleteUpload(ctx context.Context, session *fs.UploadSession) error {
	uploadID := session.UploadID
	if uploadID == "" {
		uploadID = session.Props.UploadFileID
	}
	if uploadID == "" {
		handler.l.Debug("No OpenList upload id on the session of %q, nothing to finalize.", session.Props.SavePath)
		return nil
	}

	if err := handler.client.MultipartComplete(ctx, uploadID); err != nil {
		// A session OpenList no longer holds may still have produced the
		// object (a retried completion). Confirm the file is there before
		// reporting a failure, so a duplicate callback stays harmless.
		if _, metaErr := handler.client.GetFileInfo(ctx, normalizePath(session.Props.SavePath)); metaErr == nil {
			handler.l.Debug("OpenList already holds %q, treating the completion as done.",
				session.Props.SavePath)
			return nil
		}

		return asSerializerError(err)
	}

	if session.SentinelTaskID == 0 {
		return nil
	}

	// The sentinel exists to catch a storage that silently dropped the bytes:
	// read the object back and compare its size.
	info, err := handler.client.GetFileInfo(ctx, normalizePath(session.Props.SavePath))
	if err != nil {
		return fmt.Errorf("failed to read the uploaded file back: %w", err)
	}

	if session.Props.Size > 0 && info.Size != session.Props.Size {
		return fmt.Errorf("the uploaded file is %d bytes on the storage but %d were declared",
			info.Size, session.Props.Size)
	}

	return nil
}

// CancelToken discards an abandoned multipart session so OpenList stops holding
// partial chunks.
func (handler *Driver) CancelToken(ctx context.Context, uploadSession *fs.UploadSession) error {
	uploadID := uploadSession.UploadID
	if uploadID == "" {
		uploadID = uploadSession.Props.UploadFileID
	}
	if uploadID == "" {
		return nil
	}

	if err := handler.client.MultipartAbort(ctx, uploadID); err != nil {
		handler.l.Warning("Failed to abort the OpenList upload session of %q: %s",
			uploadSession.Props.SavePath, err)
		return asSerializerError(err)
	}

	return nil
}

// Capabilities declares what this policy supports. The raw URL OpenList hands
// out is short lived, so the cached entity URL is kept well below it.
func (handler *Driver) Capabilities() *driver.Capabilities {
	return &driver.Capabilities{
		StaticFeatures:  features,
		MaxSourceExpire: 2 * time.Hour,
		MinSourceExpire: time.Minute,
	}
}

// MediaMeta is unsupported.
func (handler *Driver) MediaMeta(ctx context.Context, path, ext, language string) ([]driver.MediaMeta, error) {
	return nil, errors.New("not implemented")
}

// mimeType returns the content type to declare for an upload.
func (handler *Driver) mimeType(file *fs.UploadRequest) string {
	if file.Props.MimeType != "" {
		return file.Props.MimeType
	}
	if handler.mime != nil {
		return handler.mime.TypeByName(file.Props.Uri.Name())
	}
	return "application/octet-stream"
}

// policyMountPath reads the anchor prefix out of the policy settings.
func policyMountPath(policy *ent.StoragePolicy) string {
	if policy == nil || policy.Settings == nil {
		return ""
	}
	return policy.Settings.MountPath
}

// policyChunkSize is the chunk size the policy asks OpenList for. OpenList caps
// it at its own MultipartChunkSize and raises anything below 1 MB, so the value
// returned by MultipartInit is what the browser is told; this is only a wish.
func policyChunkSize(policy *ent.StoragePolicy) int64 {
	if policy == nil || policy.Settings == nil {
		return 0
	}
	return policy.Settings.ChunkSize
}

// parentOf returns the parent directory of a path.
func parentOf(savePath string) string {
	parent := path.Dir(normalizePath(savePath))
	if parent == "." || parent == "/" {
		return ""
	}
	return parent
}

// asSerializerError decorates an error with a user facing code so the frontend
// can show a localized message.
func asSerializerError(err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, ErrRateLimited):
		return serializer.NewError(serializer.CodeIOFailed,
			"OpenList is throttling sign-in attempts after too many failures, wait a few minutes and retry", err)
	case errors.Is(err, ErrAuthExpired):
		return serializer.NewError(serializer.CodeStorageAuthExpired,
			"OpenList rejected the stored username or password, fix them on the storage policy", err)
	case errors.Is(err, ErrStorageNotFound):
		return serializer.NewError(serializer.CodeStorageAuthExpired,
			"the mount path of this OpenList policy does not exist on that instance", err)
	case errors.Is(err, ErrPathEscaped):
		return serializer.NewError(serializer.CodeParamErr, "illegal path", err)
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return serializer.NewError(serializer.CodeIOFailed,
			fmt.Sprintf("OpenList API error (code %d): %s", apiErr.Code, apiErr.Message), err)
	}

	return err
}
