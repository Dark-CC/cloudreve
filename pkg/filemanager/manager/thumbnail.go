package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/storagepolicy"
	"github.com/cloudreve/Cloudreve/v4/ent/task"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/driver/local"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager/entitysource"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/queue"
	"github.com/cloudreve/Cloudreve/v4/pkg/thumb"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/samber/lo"
)

// recoverExistingThumbEntity is the second line of defence for the
// ErrFileExisted case: the thumbnail blob is already at its derived path but
// the upload just failed with "object existed", either because another file's
// thumbnail entity already describes that blob or because a stale object with
// no DB record is in the way. Attach what exists rather than failing the task
// and disabling thumbnails for this file.
func (m *manager) recoverExistingThumbEntity(ctx context.Context, uri *fs.URI, latest fs.Entity) (*ent.Entity, error) {
	if latest == nil || latest.ID() == 0 {
		return nil, nil
	}

	file, err := m.fs.Get(ctx, uri, dbfs.WithFileEntities(), dbfs.WithFilePublicMetadata())
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	return m.findReusableThumbEntity(ctx, file, latest)
}

// findReusableThumbEntity looks for a thumbnail entity that already exists for
// the given source entity and attaches it to the current file. Thumbnail blob
// paths are derived from their source entity (ThumbEntitySuffix over
// entity.Source()), so files sharing one source entity — an instant upload
// (hash dedup) or several copies of the same blob — share a single thumbnail
// object. Generating a second one would write the same path again and some
// drivers (y139 among them) answer that with fs.ErrFileExisted, which used to
// fail the whole task and permanently disable thumbnails for the file.
//
// The returned entity is linked to the file (refcount +1) so the existing
// reference-counted sharing model applies, exactly as with instant-upload
// entities. A nil result with a nil error means "no thumbnail entity yet".
func (m *manager) findReusableThumbEntity(ctx context.Context, file fs.File, latest fs.Entity) (*ent.Entity, error) {
	if latest == nil || latest.ID() == 0 || latest.Source() == "" {
		return nil, nil
	}

	// The thumbnail's blob path, as generateThumb would derive it.
	thumbPath := path.Clean(fs.ReplaceMagicVar(m.settings.ThumbEntitySuffix(ctx), fs.MagicVarProps{
		FsSeparator:      fs.Separator,
		PathAvailable:    true,
		BlobAvailable:    true,
		UserID:           m.user.ID,
		OriginName:       file.Uri(false).Name(),
		OriginPath:       file.Uri(false).Path(),
		CompleteBlobPath: latest.Source(),
	}))

	// A dedicated thumbnail storage policy, when configured, owns the blob.
	policyID := latest.PolicyID()
	if srcPolicy, err := m.policyClient.GetPolicyByID(ctx, latest.PolicyID()); err == nil &&
		srcPolicy.Settings != nil && srcPolicy.Settings.ThumbStoragePolicyID != 0 {
		if dst, err := m.policyClient.GetPolicyByID(ctx, srcPolicy.Settings.ThumbStoragePolicyID); err == nil &&
			dst.Status == storagepolicy.StatusActive {
			policyID = dst.ID
		}
	}

	existing, err := m.dep.FileClient().GetThumbEntityBySource(ctx, thumbPath, policyID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to look up existing thumb entity: %w", err)
	}

	// Skip entities already attached to this very file.
	if _, ok := lo.Find(file.Entities(), func(e fs.Entity) bool { return e.ID() == existing.ID }); ok {
		return existing, nil
	}

	model, err := m.dep.FileClient().GetByID(ctx, file.ID())
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	if err := m.dep.FileClient().LinkEntity(ctx, existing, model); err != nil {
		return nil, fmt.Errorf("failed to link existing thumb entity: %w", err)
	}

	m.l.Info("Reusing existing thumb entity %d for file %q (source %q)", existing.ID, file.Name(), latest.Source())
	return existing, nil
}

// Thumbnail returns the thumbnail entity of the file.
func (m *manager) Thumbnail(ctx context.Context, uri *fs.URI) (entitysource.EntitySource, error) {
	// retrieve file info
	file, err := m.fs.Get(ctx, uri, dbfs.WithFileEntities(), dbfs.WithFilePublicMetadata())
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	// 0. Check if thumb is disabled in this file.
	if _, ok := file.Metadata()[dbfs.ThumbDisabledKey]; ok || file.Type() != types.FileTypeFile {
		return nil, fs.ErrEntityNotExist
	}

	// 1. If thumbnail entity exist, use it.
	entities := file.Entities()
	thumbEntity, found := lo.Find(entities, func(e fs.Entity) bool {
		return e.Type() == types.EntityTypeThumbnail
	})
	if found {
		thumbSource, err := m.GetEntitySource(ctx, 0, fs.WithEntity(thumbEntity))
		if err != nil {
			return nil, fmt.Errorf("failed to get entity source: %w", err)
		}

		thumbSource.Apply(entitysource.WithDisplayName(file.DisplayName() + ".jpg"))
		return thumbSource, nil
	}

	latest := file.PrimaryEntity()
	// If primary entity not exist, or it's empty
	if latest == nil || latest.ID() == 0 {
		return nil, fs.ErrEntityNotExist
	}

	// 2. Thumb entity not exist, try native policy generator
	policy, handler, err := m.getEntityPolicyDriver(ctx, latest, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get entity policy driver: %w", err)
	}
	capabilities := handler.Capabilities()
	// thumb_force_proxy skips the native generator even when it supports
	// this file — the local pipeline is mandatory.
	forceProxy := policy.Settings != nil && policy.Settings.ThumbForceProxy
	// Check if file extension and size is supported by native policy generator.
	if !forceProxy && (capabilities.ThumbSupportAllExts || util.IsInExtensionList(capabilities.ThumbSupportedExts, file.DisplayName()) &&
		(capabilities.ThumbMaxSize == 0 || latest.Size() <= capabilities.ThumbMaxSize) &&
		!latest.Encrypted()) {
		thumbSource, err := m.GetEntitySource(ctx, 0, fs.WithEntity(latest), fs.WithUseThumb(true))
		if err != nil {
			return nil, fmt.Errorf("failed to get latest entity source: %w", err)
		}

		thumbSource.Apply(entitysource.WithDisplayName(file.DisplayName()))
		return thumbSource, nil
	} else if capabilities.ThumbProxy || forceProxy {
		if err := m.fs.CheckCapability(ctx, uri,
			dbfs.WithRequiredCapabilities(dbfs.NavigatorCapabilityGenerateThumb)); err != nil {
			// Current FS does not support generate new thumb.
			return nil, fs.ErrEntityNotExist
		}

		// Another file sharing this source entity (instant upload, copies) may
		// already have produced the thumbnail blob. Reuse it instead of
		// writing the same path again.
		thumbModel, err := m.findReusableThumbEntity(ctx, file, latest)
		if err != nil {
			m.l.Warning("Failed to look up reusable thumb entity for %q: %s", uri.String(), err)
		}

		var thumbEntity fs.Entity
		if thumbModel != nil {
			thumbEntity = fs.NewEntity(thumbModel)
		} else {
			thumbEntity, err = m.SubmitAndAwaitThumbnailTask(ctx, uri, file.Ext(), latest)
			if err != nil {
				return nil, fmt.Errorf("failed to execute thumb task: %w", err)
			}
		}

		thumbSource, err := m.GetEntitySource(ctx, 0, fs.WithEntity(thumbEntity))
		if err != nil {
			return nil, fmt.Errorf("failed to get entity source: %w", err)
		}

		return thumbSource, nil
	} else {
		// 4. If proxy generator not support, mark thumb as not available.
		_ = disableThumb(ctx, m, uri)
	}

	return nil, fs.ErrEntityNotExist
}

func (m *manager) SubmitAndAwaitThumbnailTask(ctx context.Context, uri *fs.URI, ext string, entity fs.Entity) (fs.Entity, error) {
	// Reuse a thumbnail another file already generated from the same source
	// entity — instant-upload copies share their source, hence its thumbnail
	// blob path too.
	if file, err := m.fs.Get(ctx, uri, dbfs.WithFileEntities()); err == nil {
		if reusable, ferr := m.findReusableThumbEntity(ctx, file, entity); ferr == nil && reusable != nil {
			if found, ok := lo.Find(file.Entities(), func(e fs.Entity) bool { return e.ID() == reusable.ID }); ok {
				return found, nil
			}

			return fs.NewEntity(reusable), nil
		}
	}

	es, err := m.GetEntitySource(ctx, 0, fs.WithEntity(entity))
	if err != nil {
		return nil, fmt.Errorf("failed to get entity source: %w", err)
	}

	defer es.Close()
	t := newGenerateThumbTask(ctx, m, uri, ext, es)
	if err := m.dep.ThumbQueue(ctx).QueueTask(ctx, t); err != nil {
		return nil, fmt.Errorf("failed to queue task: %w", err)
	}

	// Wait for task to finish
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-t.sig:
		if res.err != nil {
			return nil, fmt.Errorf("failed to generate thumb: %w", res.err)
		}

		return res.thumbEntity, nil
	}

}

func (m *manager) generateThumb(ctx context.Context, uri *fs.URI, ext string, es entitysource.EntitySource) (fs.Entity, error) {
	// Generate thumb
	pipeline := m.dep.ThumbPipeline()
	res, err := pipeline.Generate(ctx, es, ext, nil)
	if err != nil {
		if res != nil && res.Path != "" {
			_ = os.Remove(res.Path)
		}

		if !errors.Is(err, context.Canceled) && !m.stateless {
			if err := disableThumb(ctx, m, uri); err != nil {
				m.l.Warning("Failed to disable thumb: %v", err)
			}
		}

		return nil, fmt.Errorf("failed to generate thumb: %w", err)
	}

	defer os.Remove(res.Path)

	// Upload thumb entity
	thumbFile, err := os.Open(res.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to open temp thumb %q: %w", res.Path, err)
	}

	defer thumbFile.Close()
	fileInfo, err := thumbFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat temp thumb %q: %w", res.Path, err)
	}

	var (
		thumbEntity fs.Entity
	)
	if m.stateless {
		_, d, err := m.getEntityPolicyDriver(ctx, es.Entity(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get storage driver: %w", err)
		}

		savePath := es.Entity().Source() + m.settings.ThumbSlaveSidecarSuffix(ctx)
		if err := d.Put(ctx, &fs.UploadRequest{
			File:   thumbFile,
			Seeker: thumbFile,
			Props:  &fs.UploadProps{SavePath: savePath},
		}); err != nil {
			return nil, fmt.Errorf("failed to save thumb sidecar: %w", err)
		}

		thumbEntity, err = local.NewLocalFileEntity(types.EntityTypeThumbnail, savePath)
		if err != nil {
			return nil, fmt.Errorf("failed to create local thumb entity: %w", err)
		}
	} else {
		entityType := types.EntityTypeThumbnail
		req := &fs.UploadRequest{
			Props: &fs.UploadProps{
				Uri:  uri,
				Size: fileInfo.Size(),
				SavePath: path.Clean(fs.ReplaceMagicVar(m.settings.ThumbEntitySuffix(ctx), fs.MagicVarProps{
					FsSeparator:      fs.Separator,
					PathAvailable:    true,
					BlobAvailable:    true,
					Time:             time.Now(),
					UserID:           m.user.ID,
					OriginName:       uri.Name(),
					OriginPath:       uri.Path(),
					CompleteBlobPath: es.Entity().Source(),
				})),
				MimeType:   m.dep.MimeDetector(ctx).TypeByName("thumb.jpg"),
				EntityType: &entityType,
			},
			File:   thumbFile,
			Seeker: thumbFile,
		}

		// The source policy may designate a dedicated thumbnail storage
		// policy — the entity then ignores the file's own policy.
		if srcPolicy, err := m.policyClient.GetPolicyByID(ctx, es.Entity().PolicyID()); err == nil &&
			srcPolicy.Settings != nil && srcPolicy.Settings.ThumbStoragePolicyID != 0 {
			if dst, err := m.policyClient.GetPolicyByID(ctx, srcPolicy.Settings.ThumbStoragePolicyID); err == nil &&
				dst.Status == storagepolicy.StatusActive {
				req.Props.PreferredStoragePolicy = dst.ID
			} else {
				m.l.Warning("designated thumbnail policy %d unavailable, storing with source policy", srcPolicy.Settings.ThumbStoragePolicyID)
			}
		}

		// Generating thumb can be triggered by users with read-only permission. We can bypass update permission check.
		ctx = dbfs.WithBypassOwnerCheck(ctx)

		file, err := m.Update(ctx, req, fs.WithEntityType(types.EntityTypeThumbnail))
		if err != nil {
			// The blob is already there but no thumbnail entity points at it
			// (a leftover from an earlier upload, or a race between two
			// files sharing one source entity). Recover by attaching that
			// existing entity instead of failing the task, which would leave
			// the file marked thumb:disabled for good.
			if recovered, recErr := m.recoverExistingThumbEntity(ctx, uri, es.Entity()); recErr == nil && recovered != nil {
				m.l.Info("Thumb object already exists for %q, reusing it (%d)", uri.String(), recovered.ID)
				thumbEntity = fs.NewEntity(recovered)
				if m.settings.ThumbGCAfterGen(ctx) {
					m.l.Debug("GC after thumb generation")
					runtime.GC()
				}

				return thumbEntity, nil
			}

			return nil, fmt.Errorf("failed to upload thumb entity: %w", err)
		}

		entities := file.Entities()
		found := false
		thumbEntity, found = lo.Find(entities, func(e fs.Entity) bool {
			return e.Type() == types.EntityTypeThumbnail
		})
		if !found {
			return nil, fmt.Errorf("failed to find thumb entity")
		}

		activity.Record(ctx, m.settings, m.dep.ActivityClient(), types.EventThumbGenerated,
			activity.File(file.ID()), activity.Extra(map[string]any{"uri": uri.String()}))
	}

	if m.settings.ThumbGCAfterGen(ctx) {
		m.l.Debug("GC after thumb generation")
		runtime.GC()
	}

	return thumbEntity, nil
}

type (
	GenerateThumbTask struct {
		*queue.InMemoryTask
		es  entitysource.EntitySource
		ext string
		m   *manager
		uri *fs.URI
		sig chan *generateRes
	}
	generateRes struct {
		thumbEntity fs.Entity
		err         error
	}
)

func newGenerateThumbTask(ctx context.Context, m *manager, uri *fs.URI, ext string, es entitysource.EntitySource) *GenerateThumbTask {
	t := &GenerateThumbTask{
		InMemoryTask: &queue.InMemoryTask{
			DBTask: &queue.DBTask{
				Task: &ent.Task{
					CorrelationID: logging.CorrelationID(ctx),
					PublicState:   &types.TaskPublicState{},
				},
			},
		},
		es:  es,
		ext: ext,
		m:   m,
		uri: uri,
		sig: make(chan *generateRes, 2),
	}

	t.InMemoryTask.DBTask.Task.SetUser(m.user)
	return t
}

func (m *GenerateThumbTask) Do(ctx context.Context) (task.Status, error) {
	// Make sure user does not cancel request before we start generating thumb.
	select {
	case <-ctx.Done():
		err := ctx.Err()
		return task.StatusError, err
	default:
	}

	res, err := m.m.generateThumb(ctx, m.uri, m.ext, m.es)
	if err != nil {
		if errors.Is(err, thumb.ErrNotAvailable) {
			m.sig <- &generateRes{nil, err}
			return task.StatusCompleted, nil
		}

		return task.StatusError, err
	}

	m.sig <- &generateRes{res, nil}
	return task.StatusCompleted, nil
}

func (m *GenerateThumbTask) OnError(err error, d time.Duration) {
	m.InMemoryTask.OnError(err, d)
	m.sig <- &generateRes{nil, err}
}

func disableThumb(ctx context.Context, m *manager, uri *fs.URI) error {
	return m.fs.PatchMetadata(
		dbfs.WithBypassOwnerCheck(ctx),
		[]*fs.URI{uri}, fs.MetadataPatch{
			Key:     dbfs.ThumbDisabledKey,
			Value:   "",
			Private: false,
		})
}
