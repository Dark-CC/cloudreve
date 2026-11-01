package openlist

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/driver"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/mime"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	serializer "github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

// Driver is the OpenList storage policy driver for a self-hosted OpenList
// instance the site owner runs themselves.
//
// The first iteration is read-only: it lists a directory and hands the browser
// OpenList's own download URL as a 302 target, so file content travels between
// the browser and OpenList (and, for cloud-drive backends, straight to the
// underlying storage) and never through Cloudreve.
type Driver struct {
	policy   *ent.StoragePolicy
	settings setting.Provider
	config   conf.ConfigProvider
	l        logging.Logger
	mime     mime.MimeDetector
	client   Client
}

var (
	features = &boolset.BooleanSet{}
)

func init() {
	// HandlerCapabilityProxyRequired is deliberately NOT set: it would force
	// every download through Cloudreve and void the 302.
	boolset.Sets(map[driver.HandlerCapability]bool{}, features)
}

// New creates the driver for an existing storage policy.
func New(ctx context.Context, policy *ent.StoragePolicy, settings setting.Provider,
	config conf.ConfigProvider, l logging.Logger, mime mime.MimeDetector) (*Driver, error) {
	server := strings.TrimSpace(policy.Server)
	if server == "" {
		return nil, errors.New("missing OpenList instance address for the OpenList policy")
	}

	parsed, err := url.Parse(server)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid OpenList instance address %q: expected http(s)://host[:port]", server)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid OpenList instance address %q: only http and https are supported", server)
	}

	// The account is the only way to obtain a token: OpenList has no refresh
	// token, so a pasted JWT would die after 48 hours with nothing to renew it.
	if strings.TrimSpace(policy.AccessKey) == "" {
		return nil, errors.New("missing OpenList username for the OpenList policy")
	}

	if strings.TrimSpace(policy.SecretKey) == "" {
		return nil, errors.New("missing OpenList password for the OpenList policy")
	}

	d := &Driver{
		policy:   policy,
		settings: settings,
		config:   config,
		l:        l,
		mime:     mime,
		client:   NewClient(policy, config, l),
	}

	mount := ""
	if policy.Settings != nil {
		mount = policy.Settings.MountPath
	}

	l.Debug("OpenList driver initialized, instance %s, mount %q, account %s.",
		parsed.Host, mount, redact(policy.AccessKey))

	return d, nil
}

// List walks the folder at base and returns its children.
func (handler *Driver) List(ctx context.Context, base string, onProgress driver.ListProgressFunc, recursive bool) ([]fs.PhysicalObject, error) {
	base = normalizePath(base)

	res := make([]fs.PhysicalObject, 0, 64)
	if err := handler.listInto(ctx, base, base, recursive, &res, onProgress); err != nil {
		return nil, asSerializerError(err)
	}

	return res, nil
}

// listInto collects the children of dirPath, which is relative to the listed
// root, and recurses into sub folders when asked.
func (handler *Driver) listInto(ctx context.Context, dirPath, relBase string, recursive bool,
	out *[]fs.PhysicalObject, onProgress driver.ListProgressFunc) error {
	children, err := handler.client.ListChildren(ctx, dirPath)
	if err != nil {
		return err
	}

	for i := range children {
		item := &children[i]
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
			// OpenList signs the download URL per call, so no URL is cached
			// here: Source() asks for a fresh one every time.
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

// Ping verifies the instance, the account and the mount path. The policy form
// calls it before saving so a bad mount path is rejected up front.
func (handler *Driver) Ping(ctx context.Context) error {
	if err := handler.client.Ping(ctx); err != nil {
		return asSerializerError(err)
	}
	return nil
}

// Open is not implemented, this driver always serves files by URL.
func (handler *Driver) Open(ctx context.Context, path string) (*os.File, error) {
	return nil, ErrNotSupported
}

// LocalPath is empty because files never live on Cloudreve's disk.
func (handler *Driver) LocalPath(ctx context.Context, path string) string {
	return ""
}

// Put is not implemented in the read-only iteration.
func (handler *Driver) Put(ctx context.Context, file *fs.UploadRequest) error {
	return asSerializerError(ErrNotSupported)
}

// Delete is not implemented in the read-only iteration.
func (handler *Driver) Delete(ctx context.Context, files ...string) ([]string, error) {
	return files, asSerializerError(ErrNotSupported)
}

// Thumb is unsupported, OpenList is asked for no thumbnails.
func (handler *Driver) Thumb(ctx context.Context, expire *time.Time, ext string, e fs.Entity) (string, error) {
	return "", ErrNotSupported
}

// Source returns OpenList's own download URL so the browser downloads straight
// from the instance (or from the storage behind it) and Cloudreve never proxies
// a byte.
func (handler *Driver) Source(ctx context.Context, e fs.Entity, args *driver.GetSourceArgs) (string, error) {
	info, err := handler.client.Get(ctx, e.Source())
	if err != nil {
		return "", asSerializerError(err)
	}

	raw := strings.TrimSpace(info.RawURL)
	if raw == "" {
		return "", serializer.NewError(serializer.CodePolicyNotAllowed,
			"OpenList returned no download URL for this file, check that its storage backend allows direct links", nil)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("failed to parse the OpenList download URL: %w", err)
	}

	// A relative raw_url is resolved against the instance address, so the
	// browser always receives an absolute target.
	if !parsed.IsAbs() {
		base, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(handler.policy.Server), "/"))
		if err != nil {
			return "", fmt.Errorf("failed to parse the OpenList instance address: %w", err)
		}
		parsed = base.ResolveReference(parsed)
	}

	proxied, err := driver.ApplyProxyIfNeeded(handler.policy, parsed)
	if err != nil {
		return "", err
	}

	return proxied.String(), nil
}

// Token is not implemented in the read-only iteration.
func (handler *Driver) Token(ctx context.Context, uploadSession *fs.UploadSession, file *fs.UploadRequest) (*fs.UploadCredential, error) {
	return nil, asSerializerError(ErrNotSupported)
}

// CancelToken is a no-op: no upload state is ever created.
func (handler *Driver) CancelToken(ctx context.Context, uploadSession *fs.UploadSession) error {
	return nil
}

// CompleteUpload is a no-op: no upload state is ever created.
func (handler *Driver) CompleteUpload(ctx context.Context, session *fs.UploadSession) error {
	return nil
}

// Capabilities declares what this policy supports. MaxSourceExpire is generous
// because OpenList signs a URL per request rather than a stored credential; the
// short-lived ones are the underlying storage's, and those are fetched live.
func (handler *Driver) Capabilities() *driver.Capabilities {
	return &driver.Capabilities{
		StaticFeatures:  features,
		MaxSourceExpire: 24 * time.Hour,
		MinSourceExpire: time.Minute,
	}
}

// MediaMeta is unsupported.
func (handler *Driver) MediaMeta(ctx context.Context, path, ext, language string) ([]driver.MediaMeta, error) {
	return nil, ErrNotSupported
}

// SetRefreshErrorReporter installs the callback that surfaces a failed
// credential renewal to the administrator.
func SetRefreshErrorReporter(d *Driver, fn func(ctx context.Context, kind, message string) error) {
	SetRefreshErrorReporterFn(d.client, fn)
}

// asSerializerError decorates an error with a user facing code so the frontend
// can show a localized message.
func asSerializerError(err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, ErrAuthExpired):
		return serializer.NewError(serializer.CodeStorageAuthExpired,
			"OpenList login failed, please check the account and password", err)
	case errors.Is(err, ErrMountNotFound):
		return serializer.NewError(serializer.CodeParamErr,
			"The configured OpenList mount path does not exist", err)
	case errors.Is(err, ErrInvalidPath):
		return serializer.NewError(serializer.CodeParamErr,
			"Invalid path: it escapes the configured OpenList mount path", err)
	case errors.Is(err, ErrFileNotFound):
		return serializer.NewError(serializer.CodeParamErr,
			"The requested path does not exist on the OpenList instance", err)
	case errors.Is(err, ErrNotSupported):
		return serializer.NewError(serializer.CodePolicyNotAllowed,
			"This operation is not supported by the OpenList policy yet", err)
	}

	return err
}

// PolicyType is the storage policy type this driver serves, exported so callers
// can build the right policy without importing the inventory types.
func PolicyType() types.PolicyType {
	return types.PolicyTypeOpenList
}
