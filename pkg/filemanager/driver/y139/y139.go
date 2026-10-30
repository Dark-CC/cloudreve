package y139

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cluster/routes"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/driver"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/mime"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
)

// Driver is the China Mobile cloud drive (139) storage policy driver. Files are
// served to the browser straight from the drive: downloads go through a
// pre-signed URL (302) and uploads PUT every part to a pre-signed URL returned
// by the drive, so Cloudreve never proxies file content.
type Driver struct {
	policy    *ent.StoragePolicy
	settings  setting.Provider
	config    conf.ConfigProvider
	l         logging.Logger
	mime      mime.MimeDetector
	client    Client
	chunkSize int64
}

var (
	features = &boolset.BooleanSet{}
)

func init() {
	// Cloudreve's sentinel cleans up the placeholder file when the browser never
	// reports a finished upload. HandlerCapabilityProxyRequired is deliberately
	// NOT set: it would force every download through Cloudreve and void the 302.
	boolset.Sets(map[driver.HandlerCapability]bool{
		driver.HandlerCapabilityUploadSentinelRequired: true,
	}, features)
}

// New creates the driver for an existing storage policy.
func New(ctx context.Context, policy *ent.StoragePolicy, settings setting.Provider,
	config conf.ConfigProvider, l logging.Logger, mime mime.MimeDetector) (*Driver, error) {
	chunkSize := int64(50 << 20) // 50 MB
	if policy.Settings != nil && policy.Settings.ChunkSize > 0 {
		chunkSize = policy.Settings.ChunkSize
	}

	if strings.TrimSpace(policy.AccessKey) == "" {
		return nil, errors.New("missing Authorization header for the 139 cloud drive policy")
	}

	// The API rejects requests without it (01000004), so fail here with a
	// message that tells the administrator which field to fill in.
	if strings.TrimSpace(policy.SecretKey) == "" {
		return nil, errors.New("missing x-yun-app-key for the 139 cloud drive policy")
	}

	d := &Driver{
		policy:    policy,
		settings:  settings,
		config:    config,
		l:         l,
		mime:      mime,
		client:    NewClient(policy, config, l),
		chunkSize: chunkSize,
	}

	l.Debug("139 cloud drive driver initialized, authorization %s.", redact(policy.AccessKey))

	return d, nil
}

// List walks the folder at base and returns its children.
func (handler *Driver) List(ctx context.Context, base string, onProgress driver.ListProgressFunc, recursive bool) ([]fs.PhysicalObject, error) {
	base = normalizePath(base)

	parentID, err := handler.client.ResolvePath(ctx, base)
	if err != nil {
		return nil, asSerializerError(err)
	}

	res := make([]fs.PhysicalObject, 0, 64)
	if err := handler.listInto(ctx, parentID, base, recursive, &res, onProgress); err != nil {
		return nil, asSerializerError(err)
	}

	return res, nil
}

// listInto recursively collects the children of parentID rooted at relBase.
func (handler *Driver) listInto(ctx context.Context, parentID, relBase string, recursive bool,
	out *[]fs.PhysicalObject, onProgress driver.ListProgressFunc) error {
	children, err := handler.client.ListChildren(ctx, parentID)
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
			IsDir:        item.IsDir(),
			LastModify:   item.ModifiedAt(),
		}
		if entry.LastModify.IsZero() {
			entry.LastModify = time.Now()
		}

		*out = append(*out, entry)

		if item.IsDir() && recursive {
			if err := handler.listInto(ctx, item.FileID, rel, recursive, out, onProgress); err != nil {
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

// LocalPath is empty because files never live on Cloudreve's disk.
func (handler *Driver) LocalPath(ctx context.Context, path string) string {
	return ""
}

// Put uploads a file through Cloudreve (relayed uploads, WebDAV, imports).
func (handler *Driver) Put(ctx context.Context, file *fs.UploadRequest) error {
	defer file.Close()

	overwrite := file.Mode&fs.ModeOverwrite == fs.ModeOverwrite
	if !overwrite {
		if _, err := handler.client.Meta(ctx, file.Props.SavePath); err == nil {
			return fs.ErrFileExisted
		} else if !errors.Is(err, ErrFileNotFound) {
			return asSerializerError(err)
		}
	}

	hash, err := handler.contentHash(file)
	if err != nil {
		return err
	}

	parentID, err := handler.client.EnsureFolder(ctx, parentOf(file.Props.SavePath))
	if err != nil {
		return asSerializerError(err)
	}

	created, err := handler.client.Create(ctx, &CreateRequest{
		ParentFileID: parentID,
		Name:         path.Base(normalizePath(file.Props.SavePath)),
		Size:         file.Props.Size,
		ChunkSize:    handler.chunkSize,
		ContentHash:  hash,
		ContentType:  handler.mimeType(file),
		Account:      handler.accountName(ctx),
	})
	if err != nil {
		return asSerializerError(err)
	}

	if err := handler.uploadAllParts(ctx, file, created, hash); err != nil {
		return err
	}

	return nil
}

// uploadAllParts pushes every part, fetching further batches of part URLs from
// the drive when the file needs more than one hundred of them.
func (handler *Driver) uploadAllParts(ctx context.Context, file *fs.UploadRequest,
	created *CreateResponse, hash string) error {
	if created.Exist || created.RapidUpload && len(created.Parts) == 0 {
		// The drive already holds identical content, nothing to transfer.
		handler.l.Debug("139 cloud drive reported identical content for %q, upload skipped.", file.Props.SavePath)
		return nil
	}

	parts, err := handler.signAllParts(ctx, created, file.Props.Size, handler.chunkSize)
	if err != nil {
		return err
	}

	if err := handler.client.UploadParts(ctx, file, parts, handler.chunkSize); err != nil {
		return err
	}

	if err := handler.client.Complete(ctx, &CompleteRequest{
		FileID:      created.FileID,
		UploadID:    created.UploadID,
		ContentHash: hash,
	}); err != nil {
		return asSerializerError(err)
	}

	return nil
}

// signAllParts returns every part URL of an upload, asking the drive for the
// remaining batches when the file has more than one hundred parts.
func (handler *Driver) signAllParts(ctx context.Context, created *CreateResponse, size, chunkSize int64) ([]uploadedPart, error) {
	parts := created.Parts
	total := numParts(size, chunkSize)

	for len(parts) < total {
		infos := buildPartInfos(size, chunkSize)[len(parts):]
		if len(infos) > partBatchSize {
			infos = infos[:partBatchSize]
		}

		more, err := handler.client.GetUploadURL(ctx, created.FileID, created.UploadID, infos)
		if err != nil {
			return nil, asSerializerError(err)
		}
		if len(more) == 0 {
			return nil, fmt.Errorf("cloud drive returned no upload URL for parts %d-%d",
				len(parts)+1, len(parts)+len(infos))
		}

		parts = append(parts, more...)
	}

	return parts, nil
}

// SetTokenPersister installs the callback used to store a refreshed login token
// back into the storage policy.
func SetTokenPersister(d *Driver, fn func(ctx context.Context, authorization string) error) {
	SetTokenPersisterFn(d.client, fn)
}

// SetRefreshErrorReporter installs the callback that surfaces a failed
// credential renewal to the administrator.
func SetRefreshErrorReporter(d *Driver, fn func(ctx context.Context, kind, message string) error) {
	SetRefreshErrorReporterFn(d.client, fn)
}

// accountName returns the account extracted from the login token.
func (handler *Driver) accountName(ctx context.Context) string {
	if c, ok := handler.client.(*client); ok {
		return c.accountName()
	}
	return ""
}

// mimeType returns the content type to declare for the upload.
func (handler *Driver) mimeType(file *fs.UploadRequest) string {
	if file.Props.MimeType != "" {
		return file.Props.MimeType
	}
	if handler.mime != nil {
		return handler.mime.TypeByName(file.Props.Uri.Name())
	}
	return "application/octet-stream"
}

// contentHash hashes the upload stream, which must be rewindable so the parts
// can be read afterwards.
func (handler *Driver) contentHash(file *fs.UploadRequest) (string, error) {
	if hash := file.Props.Hash; hash != "" {
		return hash, nil
	}

	if !file.Seekable() {
		return "", errors.New("139 cloud drive uploads need a rewindable stream to compute the content hash")
	}

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", fmt.Errorf("failed to hash the upload content: %w", err)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("failed to rewind the upload stream: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// Delete moves the given paths to the cloud drive's recycle bin.
func (handler *Driver) Delete(ctx context.Context, files ...string) ([]string, error) {
	failed := make([]string, 0, len(files))
	var lastErr error

	ids := make([]string, 0, len(files))
	owners := make([]string, 0, len(files))

	for _, f := range files {
		id, err := handler.client.ResolvePath(ctx, f)
		if err != nil {
			if errors.Is(err, ErrFileNotFound) {
				// Already gone, deleting it again is a success.
				continue
			}

			failed = append(failed, f)
			lastErr = asSerializerError(err)
			continue
		}

		ids = append(ids, id)
		owners = append(owners, f)
	}

	if len(ids) == 0 {
		return failed, lastErr
	}

	if err := handler.client.Trash(ctx, ids); err != nil {
		handler.l.Debug("Failed to move %d objects to the 139 recycle bin: %s", len(ids), err)
		failed = append(failed, owners...)
		lastErr = asSerializerError(err)
	}

	return failed, lastErr
}

// Thumb is unsupported, the drive exposes no thumbnail API for third parties.
func (handler *Driver) Thumb(ctx context.Context, expire *time.Time, ext string, e fs.Entity) (string, error) {
	return "", errors.New("not supported")
}

// Source returns a pre-signed download URL so the browser downloads straight
// from the cloud drive's object storage.
func (handler *Driver) Source(ctx context.Context, e fs.Entity, args *driver.GetSourceArgs) (string, error) {
	fileID, err := handler.client.ResolvePath(ctx, e.Source())
	if err != nil {
		return "", asSerializerError(err)
	}

	res, err := handler.client.GetDownloadURL(ctx, fileID)
	if err != nil {
		return "", asSerializerError(err)
	}

	// The signature covers only the host: appending any query (for example a
	// content-disposition override) would invalidate it.
	return res.Best(), nil
}

// Token creates the drive placeholder and signs one upload URL per part so the
// browser uploads directly.
func (handler *Driver) Token(ctx context.Context, uploadSession *fs.UploadSession, file *fs.UploadRequest) (*fs.UploadCredential, error) {
	if _, err := handler.client.Meta(ctx, file.Props.SavePath); err == nil &&
		file.Mode&fs.ModeOverwrite != fs.ModeOverwrite {
		return nil, fs.ErrFileExisted
	}

	parentID, err := handler.client.EnsureFolder(ctx, parentOf(file.Props.SavePath))
	if err != nil {
		return nil, asSerializerError(err)
	}

	hash := file.Props.Hash
	if hash == "" {
		// Without a client asserted hash the drive cannot deduplicate, which is
		// fine: it just uploads the bytes.
		handler.l.Debug("No client side content hash for %q, deduplication is skipped.", file.Props.SavePath)
	}

	created, err := handler.client.Create(ctx, &CreateRequest{
		ParentFileID: parentID,
		Name:         path.Base(normalizePath(file.Props.SavePath)),
		Size:         file.Props.Size,
		ChunkSize:    handler.chunkSize,
		ContentHash:  hash,
		ContentType:  handler.mimeType(file),
		Account:      handler.accountName(ctx),
	})
	if err != nil {
		return nil, asSerializerError(err)
	}

	siteURL := handler.settings.SiteURL(setting.UseFirstSiteUrl(ctx))
	uploadSession.ChunkSize = handler.chunkSize
	uploadSession.UploadID = created.UploadID
	// The placeholder the drive just created has no name to resolve yet: it
	// only becomes visible once /file/complete has run. Carry the drive's own
	// file ID to the completion step, otherwise it cannot address the upload
	// it must finalize.
	uploadSession.Props.UploadFileID = created.FileID
	uploadSession.Callback = routes.MasterSlaveCallbackUrl(siteURL, types.PolicyTypeY139,
		uploadSession.Props.UploadSessionID, uploadSession.CallbackSecret).String()

	credential := &fs.UploadCredential{
		UploadID:   created.UploadID,
		SessionID:  uploadSession.Props.UploadSessionID,
		ChunkSize:  handler.chunkSize,
		Expires:    time.Now().Add(uploadURLTTL).Unix(),
		Uri:        file.Props.SavePath,
		Callback:   uploadSession.Callback,
		MimeType:   handler.mimeType(file),
		UploadURLs: make([]string, 0, len(created.Parts)),
	}

	if created.Exist || (created.RapidUpload && len(created.Parts) == 0) {
		// Identical content already exists on the drive, skip the transfer.
		//
		// The drive in this case hands back the file it already holds
		// complete: there is no upload to finalize (UploadID is empty) and no
		// part to PUT. The upload still has to be *completed* on our side, so
		// the browser must call the callback exactly like a real transfer
		// does — hence RapidUploaded stays false (it would make the client
		// skip its callback and leave the file stuck in "uploading") and the
		// callback URL is kept. CompleteUpload() recognizes the empty
		// UploadID as this case and finalizes without talking to the drive.
		handler.l.Debug("139 cloud drive already holds the content for %q, upload transfer skipped.",
			file.Props.SavePath)
		credential.UploadURLs = nil
		credential.NoPartsUploaded = true
		return credential, nil
	}

	// The drive signs at most 100 part URLs per call, so larger files need the
	// rest signed here before the browser can be handed a complete list.
	signed, err := handler.signAllParts(ctx, created, file.Props.Size, handler.chunkSize)
	if err != nil {
		return nil, err
	}

	// The client PUTs upload_urls[i] for the chunk with the same index, so the
	// parts must be ordered by their 1-based part number.
	total := numParts(file.Props.Size, handler.chunkSize)
	urls := make([]string, total)

	for _, part := range signed {
		if part.PartNumber <= 0 || part.PartNumber > total {
			return nil, fmt.Errorf("cloud drive signed an unexpected part number %d", part.PartNumber)
		}
		urls[part.PartNumber-1] = part.UploadUrl
	}

	for i, u := range urls {
		if u == "" {
			return nil, fmt.Errorf("cloud drive did not sign part %d of %d", i+1, total)
		}
	}

	credential.UploadURLs = urls

	return credential, nil
}

// CancelToken removes the placeholder the drive created for an abandoned upload.
func (handler *Driver) CancelToken(ctx context.Context, uploadSession *fs.UploadSession) error {
	fileID, err := handler.client.ResolvePath(ctx, uploadSession.Props.SavePath)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return nil
		}
		return asSerializerError(err)
	}

	if err := handler.client.Trash(ctx, []string{fileID}); err != nil {
		handler.l.Warning("Failed to remove placeholder %q of an abandoned 139 upload session: %s",
			uploadSession.Props.SavePath, err)
		return asSerializerError(err)
	}

	return nil
}

// CompleteUpload finalizes the multipart upload and verifies the stored size.
//
// An empty UploadID means the drive answered /file/create with `exist` or
// `rapidUpload`: it already holds this exact content, so there is nothing to
// sign, transfer or finalize there and this is a no-op. The Cloudreve side of
// the upload is still completed by the caller, which is what makes such an
// upload finish instead of hanging.
func (handler *Driver) CompleteUpload(ctx context.Context, session *fs.UploadSession) error {
	if session.UploadID == "" {
		handler.l.Debug("139 cloud drive reported the content of %q as already stored, nothing to finalize.",
			session.Props.SavePath)
		return nil
	}

	// Prefer the ID recorded when the placeholder was created. Resolving the
	// path instead would fail: the drive does not list an unfinished upload.
	fileID := session.Props.UploadFileID
	if fileID == "" {
		resolved, err := handler.client.ResolvePath(ctx, session.Props.SavePath)
		if err != nil {
			return asSerializerError(err)
		}
		fileID = resolved
	}

	if err := handler.client.Complete(ctx, &CompleteRequest{
		FileID:      fileID,
		UploadID:    session.UploadID,
		ContentHash: session.Props.Hash,
	}); err != nil {
		return asSerializerError(err)
	}

	if session.SentinelTaskID == 0 {
		return nil
	}

	meta, err := handler.client.Meta(ctx, session.Props.SavePath)
	if err != nil {
		return fmt.Errorf("failed to read the uploaded file back: %w", err)
	}

	if meta.Size != session.Props.Size {
		return serializer.NewError(
			serializer.CodeMetaMismatch,
			fmt.Sprintf("File size not match, expected: %d, actual: %d", session.Props.Size, meta.Size),
			nil,
		)
	}

	return nil
}

// Capabilities declares what this policy supports. MaxSourceExpire stays below
// the drive's 15 minute URL lifetime so cached URLs always remain valid.
func (handler *Driver) Capabilities() *driver.Capabilities {
	return &driver.Capabilities{
		StaticFeatures:  features,
		MaxSourceExpire: downloadURLTTL - 2*time.Minute,
		MinSourceExpire: time.Minute,
	}
}

// MediaMeta is unsupported.
func (handler *Driver) MediaMeta(ctx context.Context, path, ext, language string) ([]driver.MediaMeta, error) {
	return nil, errors.New("not implemented")
}

// parentOf returns the parent directory of a save path.
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
	case errors.Is(err, ErrAuthExpired):
		return serializer.NewError(serializer.CodeStorageAuthExpired,
			"139 cloud drive login state expired, please paste a fresh Authorization header", err)
	case errors.Is(err, ErrContentRejected):
		return serializer.NewError(serializer.CodeContentAuditRejected,
			"Object was rejected by the 139 cloud drive content audit", err)
	}

	return err
}
