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
}

var features = &boolset.BooleanSet{}

func init() {
	// HandlerCapabilityProxyRequired is deliberately NOT set: it would force
	// every download through this site and void the 302 to OpenList.
	boolset.Sets(map[driver.HandlerCapability]bool{}, features)
}

// errReadOnly is what every unimplemented write path answers with.
var errReadOnly = errors.New("this storage policy is read-only in this release (listing and download only)")

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
		policy:   policy,
		settings: settings,
		config:   config,
		l:        l,
		mime:     mime,
		client:   c,
		mount:    c.MountPath(),
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

// Put is out of scope in this release.
func (handler *Driver) Put(ctx context.Context, file *fs.UploadRequest) error {
	return errReadOnly
}

// Delete is out of scope in this release.
func (handler *Driver) Delete(ctx context.Context, files ...string) ([]string, error) {
	return files, errReadOnly
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

// Token is out of scope in this release.
func (handler *Driver) Token(ctx context.Context, uploadSession *fs.UploadSession, file *fs.UploadRequest) (*fs.UploadCredential, error) {
	return nil, errReadOnly
}

// CancelToken is out of scope in this release.
func (handler *Driver) CancelToken(ctx context.Context, uploadSession *fs.UploadSession) error {
	return errReadOnly
}

// CompleteUpload is out of scope in this release.
func (handler *Driver) CompleteUpload(ctx context.Context, session *fs.UploadSession) error {
	return errReadOnly
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

// policyMountPath reads the anchor prefix out of the policy settings.
func policyMountPath(policy *ent.StoragePolicy) string {
	if policy == nil || policy.Settings == nil {
		return ""
	}
	return policy.Settings.MountPath
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
