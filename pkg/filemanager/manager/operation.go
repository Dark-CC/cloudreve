package manager

import (
	"context"
	"encoding/gob"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/driver"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/lock"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/samber/lo"
)

const (
	EntityUrlCacheKeyPrefix     = "entity_url_"
	DownloadSentinelCachePrefix = "download_sentinel_"
)

type (
	ListArgs struct {
		Page           int
		PageSize       int
		PageToken      string
		Order          string
		OrderDirection string
		// StreamResponseCallback is used for streamed list operation, e.g. searching files.
		// Whenever a new item is found, this callback will be called with the current item and the parent item.
		StreamResponseCallback func(fs.File, []fs.File)
	}

	EntityUrlCache struct {
		Url                        string
		BrowserDownloadDisplayName string
		ExpireAt                   *time.Time
		Metered                    bool
	}
)

func init() {
	gob.Register(EntityUrlCache{})
}

func (m *manager) Get(ctx context.Context, path *fs.URI, opts ...fs.Option) (fs.File, error) {
	return m.fs.Get(ctx, path, opts...)
}

func (m *manager) List(ctx context.Context, path *fs.URI, args *ListArgs) (fs.File, *fs.ListFileResult, error) {
	dbfsSetting := m.settings.DBFS(ctx)
	opts := []fs.Option{
		fs.WithPageSize(args.PageSize),
		fs.WithOrderBy(args.Order),
		fs.WithOrderDirection(args.OrderDirection),
		dbfs.WithFilePublicMetadata(),
		dbfs.WithContextHint(),
		dbfs.WithFileShareIfOwned(),
	}

	searchParams := path.SearchParameters()
	if searchParams != nil {
		if dbfsSetting.UseSSEForSearch {
			opts = append(opts, dbfs.WithStreamListResponseCallback(args.StreamResponseCallback))
		}

		if searchParams.Category != "" {
			// Overwrite search query with predefined category
			category := fs.SearchCategoryFromString(searchParams.Category)
			if category == setting.CategoryUnknown {
				return nil, nil, fmt.Errorf("unknown category: %s", searchParams.Category)
			}

			path = path.SetQuery(m.settings.SearchCategoryQuery(ctx, category))
			searchParams = path.SearchParameters()
		}
	}

	if dbfsSetting.UseCursorPagination || searchParams != nil {
		opts = append(opts, dbfs.WithCursorPagination(args.PageToken))
	} else {
		opts = append(opts, fs.WithPage(args.Page))
	}

	return m.fs.List(ctx, path, opts...)
}

func (m *manager) SharedAddressTranslation(ctx context.Context, path *fs.URI, opts ...fs.Option) (fs.File, *fs.URI, error) {
	o := newOption()
	for _, opt := range opts {
		opt.Apply(o)
	}

	return m.fs.SharedAddressTranslation(ctx, path)
}

func (m *manager) Create(ctx context.Context, path *fs.URI, fileType types.FileType, opts ...fs.Option) (fs.File, error) {
	o := newOption()
	for _, opt := range opts {
		opt.Apply(o)
	}

	if m.stateless {
		return nil, o.Node.CreateFile(ctx, &fs.StatelessCreateFileService{
			Path:   path.String(),
			Type:   fileType,
			UserID: o.StatelessUserID,
		})
	}

	isSymbolic := false
	if o.Metadata != nil {
		_, err := m.validateMetadata(ctx, lo.MapToSlice(o.Metadata, func(key string, value string) fs.MetadataPatch {
			if key == shareRedirectMetadataKey {
				isSymbolic = true
			}

			return fs.MetadataPatch{
				Key:   key,
				Value: value,
			}
		})...)
		if err != nil {
			return nil, err
		}
	}

	if isSymbolic {
		opts = append(opts, dbfs.WithSymbolicLink())
	}

	return m.fs.Create(ctx, path, fileType, opts...)
}

func (m *manager) Rename(ctx context.Context, path *fs.URI, newName string) (fs.File, error) {
	// A storage addressed by path keeps its own copy of the tree, so the rename
	// has to reach it before the index claims the file moved. Resolve the
	// source first: after Rename the index no longer knows the old path.
	physical, err := m.physicalRenameTarget(ctx, path)
	if err != nil {
		return nil, err
	}

	if physical != nil {
		if err := physical.rename(ctx, newName); err != nil {
			return nil, err
		}
	}

	file, indexDiff, err := m.fs.Rename(ctx, path, newName)
	m.processIndexDiff(ctx, indexDiff)
	if err != nil {
		// The index refused the new name: undo what the storage already did so
		// the two do not drift apart.
		if physical != nil {
			if undoErr := physical.rename(ctx, path.Name()); undoErr != nil {
				m.l.Warning("failed to undo the storage side rename of %q: %s", path.String(), undoErr)
			}
		}
		return file, err
	}

	if physical != nil {
		// The object now lives under its new name on the storage, so every
		// entity below the renamed path has to be re-addressed. Skipping this
		// leaves the index pointing at addresses the storage no longer has,
		// which breaks every later download, move and delete.
		m.relocateEntitySources(ctx, file, physical.policy)
	}

	// Keep WebDAV mounts bound under the renamed path pointing at its new URI.
	// Scope to the file's owner — the URI authority carries the owner's hash,
	// which may differ from the requester for admin-on-behalf operations.
	m.rewriteDavMounts(ctx, file.OwnerID(), path, file.Uri(false))
	return file, nil
}

func (m *manager) MoveOrCopy(ctx context.Context, src []*fs.URI, dst *fs.URI, isCopy bool) error {
	// Copying leaves the source alone, so only a move needs the storage to act.
	// The destination has to be validated before the storage is touched: the
	// index rejects an unknown destination, and by then the objects would
	// already have been moved, with no way back.
	var physical []physicalMoveTarget
	if !isCopy {
		if _, dstErr := m.fs.Get(ctx, dst); dstErr != nil {
			return dstErr
		}

		physical = m.physicalMoveTargets(ctx, src, dst)
	}

	moved := make([]physicalMoveTarget, 0, len(physical))
	// undo puts the objects already moved back where they came from. A move
	// carried the object into the destination folder under its own name, so
	// that is the address to take it from, not the one it started at.
	undo := func(from string) {
		for i := len(moved) - 1; i >= 0; i-- {
			landed := pathJoin(moved[i].dstDir, pathBase(moved[i].from))
			if undoErr := moved[i].move(ctx, landed, strings.Trim(moved[i].parentDir, "/")); undoErr != nil {
				m.l.Warning("failed to undo the storage side move of %q: %s", from, undoErr)
			}
		}
	}

	for _, target := range physical {
		if err := target.move(ctx, target.from, target.dstDir); err != nil {
			undo(target.from)
			return err
		}
		moved = append(moved, target)
	}

	indexDiff, err := m.fs.MoveOrCopy(ctx, src, dst, isCopy)
	if err != nil {
		undo("the destination folder")
		return err
	}

	m.processIndexDiff(ctx, indexDiff)

	// Same reason as for a rename: a moved object sits somewhere else on the
	// storage, so the entities below it must follow.
	for _, target := range moved {
		movedFile, getErr := m.fs.Get(ctx, dst.JoinRaw(target.uri.Name()))
		if getErr != nil {
			m.l.Warning("failed to re-address the entities of %q after the move: %s", target.uri.String(), getErr)
			continue
		}

		m.relocateEntitySources(ctx, movedFile, target.policy)
	}

	// Keep WebDAV mounts bound under moved paths pointing at their new URIs.
	if !isCopy && m.user != nil {
		for _, srcURI := range src {
			m.rewriteDavMounts(ctx, m.user.ID, srcURI, dst.JoinRaw(srcURI.Name()))
		}
	}
	return nil
}

// pathBase and pathJoin address the storage side of a path, which always uses
// forward slashes regardless of the host.
func pathBase(p string) string {
	trimmed := strings.TrimRight(p, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}

	return trimmed
}

func pathJoin(dir, name string) string {
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return name
	}

	return dir + "/" + name
}

// physicalRenameTarget resolves the object a rename must be mirrored onto, or
// nil when the storage does not keep a path based tree of its own.
type physicalRenameTarget struct {
	driver driver.Handler
	from   string
	// policy is the policy the object lives on. It travels with the target
	// because the file the index returns after a rename is rebuilt from the
	// updated row and no longer carries the extended info that held it.
	policy *ent.StoragePolicy
}

func (p *physicalRenameTarget) rename(ctx context.Context, newName string) error {
	r, ok := p.driver.(driver.PhysicalRenamer)
	if !ok {
		return nil
	}

	if err := r.RenameFileOnStorage(ctx, p.from, newName); err != nil {
		return fmt.Errorf("failed to rename the object on the storage: %w", err)
	}

	return nil
}

// physicalMoveTarget is one object a move must be mirrored onto.
type physicalMoveTarget struct {
	driver    driver.Handler
	from      string
	parentDir string
	// dstDir is where the object goes on the storage. It is resolved from the
	// destination URI through the policy's directory rule, not from the URI
	// path: the storage's tree is not the index's tree.
	dstDir string
	// policy is the policy the object lives on, kept for the same reason as on
	// physicalRenameTarget.
	policy *ent.StoragePolicy
	// uri is the file this target mirrors, so the re-addressing step can find
	// the file again under its new parent without guessing.
	uri *fs.URI
}

func (p *physicalMoveTarget) move(ctx context.Context, from, dstDir string) error {
	mv, ok := p.driver.(driver.PhysicalMover)
	if !ok {
		return nil
	}

	if err := mv.MoveFileOnStorage(ctx, from, dstDir); err != nil {
		return fmt.Errorf("failed to move the object on the storage: %w", err)
	}

	return nil
}

// physicalRenameTarget returns the storage side rename to mirror, or nil when
// the file does not live on a path addressed storage.
func (m *manager) physicalRenameTarget(ctx context.Context, path *fs.URI) (*physicalRenameTarget, error) {
	file, err := m.fs.Get(ctx, path, dbfs.WithFileEntities(), dbfs.WithExtendedInfo())
	if err != nil {
		// Let Rename report the real problem: the index lookup is the
		// authoritative one and repeats the same call.
		m.l.Debug("not renaming %q on the storage: %s", path.String(), err)
		return nil, nil
	}

	policy := m.storagePolicyOf(ctx, file)
	if policy == nil {
		m.l.Debug("not renaming %q on the storage: no storage policy in its extended info", path.String())
		return nil, nil
	}

	d, err := m.GetStorageDriver(ctx, policy)
	if err != nil {
		return nil, err
	}

	if _, ok := d.(driver.PhysicalRenamer); !ok {
		m.l.Debug("not renaming %q on the storage: driver %T does not mirror renames", path.String(), d)
		return nil, nil
	}

	// A folder has no entity of its own, and the storage keeps the folder under
	// the path the directory rule assigns it, so that path is what gets
	// renamed. Renaming it there carries every child along in one call.
	if file.Type() != types.FileTypeFile {
		if path.Path() == "/" || path.Path() == "" {
			// The root folder is the policy's mount point itself.
			return nil, nil
		}

		rel := strings.Trim(dbfs.PhysicalDirForURI(policy, path, file.OwnerID()), "/")
		if rel == "" || rel == "." {
			m.l.Debug("not renaming folder %q on the storage: it resolves to the mount root", path.String())
			return nil, nil
		}

		return &physicalRenameTarget{driver: d, from: rel, policy: policy}, nil
	}

	entity := file.PrimaryEntity()
	if entity == nil || strings.TrimSpace(entity.Source()) == "" {
		m.l.Debug("not renaming %q on the storage: it has no entity source", path.String())
		return nil, nil
	}

	return &physicalRenameTarget{driver: d, from: entity.Source(), policy: policy}, nil
}

// physicalMoveTargets returns the storage side moves to mirror, skipping any
// file that does not live on a path addressed storage.
func (m *manager) physicalMoveTargets(ctx context.Context, src []*fs.URI, dst *fs.URI) []physicalMoveTarget {
	res := make([]physicalMoveTarget, 0, len(src))
	for _, uri := range src {
		file, err := m.fs.Get(ctx, uri, dbfs.WithFileEntities(), dbfs.WithExtendedInfo())
		if err != nil {
			m.l.Debug("skipping the storage side move of %q: %s", uri.String(), err)
			continue
		}

		policy := m.storagePolicyOf(ctx, file)
		if policy == nil {
			continue
		}

		d, err := m.GetStorageDriver(ctx, policy)
		if err != nil {
			m.l.Debug("skipping the storage side move of %q: %s", uri.String(), err)
			continue
		}

		if _, ok := d.(driver.PhysicalMover); !ok {
			continue
		}

		// A folder is addressed by the path its own directory rule assigns. A
		// file is addressed by the source its primary entity recorded when it
		// was written.
		source := strings.Trim(dbfs.PhysicalDirForURI(policy, uri, file.OwnerID()), "/")
		if file.Type() == types.FileTypeFile {
			entity := file.PrimaryEntity()
			if entity == nil || strings.TrimSpace(entity.Source()) == "" {
				continue
			}
			source = entity.Source()
		} else if source == "" || source == "." {
			continue
		}

		res = append(res, physicalMoveTarget{
			driver:    d,
			from:      source,
			parentDir: pathDir(source),
			dstDir:    dbfs.PhysicalDirForURI(policy, dst, file.OwnerID()),
			policy:    policy,
			uri:       uri,
		})
	}

	return res
}

// extendedStoragePolicy returns the storage policy of a file the index loaded
// with WithExtendedInfo, or nil when the file has no extended info (the option
// was not applied, or the policy id could not be resolved). Callers treat nil
// as "this file is not on a path addressed storage" and skip the storage side
// operation, so the guard must never be removed: ExtendedInfo itself returns a
// nil pointer for files loaded without the option.
// storagePolicyOf returns the policy an object lives on. A file carries it in
// its extended info; a folder has no entity of its own, so the info is empty
// and the policy is read off the first child that has one. A folder that holds
// nothing addressable resolves to nil and its renames are left to the index.
func (m *manager) storagePolicyOf(ctx context.Context, file fs.File) *ent.StoragePolicy {
	if policy := extendedStoragePolicy(file); policy != nil {
		return policy
	}

	if file.Type() == types.FileTypeFile {
		return nil
	}

	_, res, err := m.fs.List(ctx, file.Uri(false), fs.WithPageSize(10), dbfs.WithExtendedInfo())
	if err != nil {
		m.l.Debug("failed to look up the storage policy of folder %q: %s", file.Uri(false).String(), err)
		return nil
	}

	if res == nil {
		return nil
	}

	// The policy the listing reports is the folder's default one, which is not
	// necessarily where its children were written: a child uploaded under a
	// non default policy leaves the folder itself homeless. Resolve a child
	// properly instead of trusting the listing, which carries no extended info.
	for _, child := range res.Files {
		full, err := m.fs.Get(ctx, child.Uri(false), dbfs.WithFileEntities(), dbfs.WithExtendedInfo())
		if err != nil {
			continue
		}

		if policy := extendedStoragePolicy(full); policy != nil {
			return policy
		}
	}

	return res.StoragePolicy
}

func extendedStoragePolicy(file fs.File) *ent.StoragePolicy {
	info := file.ExtendedInfo()
	if info == nil {
		return nil
	}

	return info.StoragePolicy
}

// relocateEntitySources re-addresses the entities below a path whose object
// changed name or location on the storage. The index knows the new tree, the
// entities still carry the address recorded when they were written; the two
// only agree again once each entity's source is recomputed from the policy's
// path rules. Errors are logged rather than returned: the index and the
// storage already agree with each other on disk, and a half-applied rename is
// worse than a stale address the next operation reports cleanly.
func (m *manager) relocateEntitySources(ctx context.Context, root fs.File, policy *ent.StoragePolicy) {
	if root == nil || root.IsNil() || policy == nil {
		return
	}

	files := make([]fs.File, 0, 1)
	err := m.fs.Walk(ctx, root.Uri(false), math.MaxInt, func(file fs.File, level int) error {
		if file.Type() == types.FileTypeFile {
			files = append(files, file)
		}
		return nil
	}, dbfs.WithFileEntities())
	if err != nil {
		m.l.Warning("failed to walk %q while re-addressing its entities: %s", root.Uri(false).String(), err)
		return
	}

	// The walk starts at the root itself, but make sure a single file is never
	// missed when a driver reports it as a leaf without yielding it.
	if root.Type() == types.FileTypeFile {
		files = append(files, root)
	}

	client := m.dep.FileClient()
	for _, file := range files {
		entityID := file.PrimaryEntityID()
		if entityID == 0 {
			continue
		}

		// The walk yields files without their entity edges loaded, so the
		// entity is read back by id instead of taken from PrimaryEntity, which
		// would hand out an empty stand-in with no id and no source.
		entity, getErr := client.GetEntityByID(ctx, entityID)
		if getErr != nil {
			m.l.Warning("failed to read entity %d of %q while re-addressing it: %s",
				entityID, file.Uri(false).String(), getErr)
			continue
		}

		source := dbfs.PhysicalPathForURI(policy, file.Uri(false), file.OwnerID())
		if source == entity.Source {
			continue
		}

		if relocateErr := client.RelocateEntity(ctx, entity, source, policy.ID); relocateErr != nil {
			m.l.Warning("failed to re-address entity %d of %q: %s", entityID, file.Uri(false).String(), relocateErr)
		}
	}
}

// pathDir returns the parent directory of a storage path.
func pathDir(p string) string {
	p = strings.Trim(p, "/")
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return "/"
	}
	return "/" + p[:idx]
}

// rewriteDavMounts repoints the given user's dav account mount URIs that
// reference oldURI (or any path under it) to newURI. Failures are logged but
// never fail the file operation itself.
func (m *manager) rewriteDavMounts(ctx context.Context, ownerID int, oldURI, newURI *fs.URI) {
	if ownerID == 0 || oldURI == nil || newURI == nil {
		return
	}
	if err := m.dep.DavAccountClient().UpdateURIPrefix(ctx, ownerID, oldURI.String(), newURI.String()); err != nil {
		m.l.Warning("failed to update dav account URIs after file operation: %s", err)
	}
}

func (m *manager) SoftDelete(ctx context.Context, path ...*fs.URI) error {
	return m.fs.SoftDelete(ctx, path...)
}

// EmptyTrash hard-deletes every top-level item in the current user's trash bin.
// It iterates through all pages of the trash listing and performs the deletion in
// batches. Returns nil if the trash is already empty.
func (m *manager) EmptyTrash(ctx context.Context) error {
	trashUri, err := fs.NewUriFromString(fmt.Sprintf("%s://%s", constants.CloudreveScheme, constants.FileSystemTrash))
	if err != nil {
		return fmt.Errorf("failed to build trash uri: %w", err)
	}

	dbfsSetting := m.settings.DBFS(ctx)
	pageSize := dbfsSetting.MaxPageSize

	for {
		_, listRes, err := m.fs.List(ctx, trashUri, fs.WithPageSize(pageSize))
		if err != nil {
			return fmt.Errorf("failed to list trash: %w", err)
		}

		if len(listRes.Files) == 0 {
			return nil
		}

		uris := make([]*fs.URI, 0, len(listRes.Files))
		for _, f := range listRes.Files {
			uris = append(uris, f.Uri(false))
		}

		if err := m.Delete(ctx, uris, fs.WithSysSkipSoftDelete(true)); err != nil {
			return fmt.Errorf("failed to delete trash files: %w", err)
		}

		// Stop when there are no more pages.
		if listRes.Pagination == nil ||
			(listRes.Pagination.IsCursor && listRes.Pagination.NextPageToken == "") ||
			(!listRes.Pagination.IsCursor && len(listRes.Files) < pageSize) {
			return nil
		}
	}
}

func (m *manager) Delete(ctx context.Context, path []*fs.URI, opts ...fs.Option) error {
	o := newOption()
	for _, opt := range opts {
		opt.Apply(o)
	}

	if !o.SkipSoftDelete && !o.SysSkipSoftDelete {
		return m.SoftDelete(ctx, path...)
	}

	staleEntities, indexDiff, err := m.fs.Delete(ctx, path, fs.WithUnlinkOnly(o.UnlinkOnly), fs.WithSysSkipSoftDelete(o.SysSkipSoftDelete))
	if err != nil {
		return err
	}

	m.l.Debug("New stale entities: %v", staleEntities)

	// Delete stale entities
	if len(staleEntities) > 0 {
		t, err := newExplicitEntityRecycleTask(ctx, lo.Map(staleEntities, func(entity fs.Entity, index int) int {
			return entity.ID()
		}))
		if err != nil {
			return fmt.Errorf("failed to create explicit entity recycle task: %w", err)
		}

		if err := m.dep.EntityRecycleQueue(ctx).QueueTask(ctx, t); err != nil {
			return fmt.Errorf("failed to queue explicit entity recycle task: %w", err)
		}
	}

	// Process index diff
	if indexDiff != nil {
		m.processIndexDiff(ctx, indexDiff)
	}

	return nil
}

func (m *manager) Walk(ctx context.Context, path *fs.URI, depth int, f fs.WalkFunc, opts ...fs.Option) error {
	return m.fs.Walk(ctx, path, depth, f, opts...)
}

func (m *manager) Capacity(ctx context.Context) (*fs.Capacity, error) {
	res, err := m.fs.Capacity(ctx, m.user)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (m *manager) CheckIfCapacityExceeded(ctx context.Context) error {
	ctx = context.WithValue(ctx, inventory.LoadUserGroup{}, true)
	capacity, err := m.Capacity(ctx)
	if err != nil {
		return fmt.Errorf("failed to get user capacity: %w", err)
	}

	if capacity.Used <= capacity.Total {
		return nil
	}

	return nil
}

func (l *manager) ConfirmLock(ctx context.Context, ancestor fs.File, uri *fs.URI, token ...string) (func(), fs.LockSession, error) {
	return l.fs.ConfirmLock(ctx, ancestor, uri, token...)
}

func (l *manager) Lock(ctx context.Context, d time.Duration, requester *ent.User, zeroDepth bool,
	application lock.Application, uri *fs.URI, token string) (fs.LockSession, error) {
	return l.fs.Lock(ctx, d, requester, zeroDepth, application, uri, token)
}

func (l *manager) Unlock(ctx context.Context, tokens ...string) error {
	return l.fs.Unlock(ctx, tokens...)
}

func (l *manager) Refresh(ctx context.Context, d time.Duration, token string) (lock.LockDetails, error) {
	return l.fs.Refresh(ctx, d, token)
}

func (l *manager) Restore(ctx context.Context, path ...*fs.URI) error {
	return l.fs.Restore(ctx, path...)
}

func (l *manager) CreateOrUpdateShare(ctx context.Context, paths []*fs.URI, args *CreateShareArgs) (*ent.Share, error) {
	if len(paths) == 0 {
		return nil, serializer.NewError(serializer.CodeParamErr, "src file not found", nil)
	}

	files := make([]fs.File, 0, len(paths))
	seen := make(map[int]struct{}, len(paths))
	for _, path := range paths {
		file, err := l.fs.Get(ctx, path, dbfs.WithRequiredCapabilities(dbfs.NavigatorCapabilityShare), dbfs.WithNotRoot(), dbfs.WithFileEntities())
		if err != nil {
			return nil, serializer.NewError(serializer.CodeNotFound, "src file not found", err)
		}

		// Only file owner can share file
		if file.OwnerID() != l.user.ID {
			return nil, serializer.NewError(serializer.CodeNoPermissionErr, "permission denied", nil)
		}

		if file.IsSymbolic() {
			return nil, serializer.NewError(serializer.CodeNoPermissionErr, "cannot share symbolic file", nil)
		}

		// Files in the owner's private space are never shareable, even while
		// the vault is unlocked.
		if inside, err := l.fs.IsInPrivateSpace(ctx, file); err != nil {
			return nil, serializer.NewError(serializer.CodeDBError, "failed to check private space", err)
		} else if inside {
			return nil, serializer.NewError(serializer.CodeNoPermissionErr, "files in private space cannot be shared", nil)
		}

		if _, ok := seen[file.ID()]; !ok {
			seen[file.ID()] = struct{}{}
			files = append(files, file)
		}
	}

	file := files[0]
	var (
		existed *ent.Share
		err     error
	)
	shareClient := l.dep.ShareClient()
	if args.ExistedShareID != 0 {
		loadShareCtx := context.WithValue(ctx, inventory.LoadShareFile{}, true)
		loadShareCtx = context.WithValue(loadShareCtx, inventory.LoadShareFiles{}, true)
		existed, err = shareClient.GetByID(loadShareCtx, args.ExistedShareID)
		if err != nil {
			return nil, serializer.NewError(serializer.CodeNotFound, "failed to get existed share", err)
		}

		if existed.Edges.File.ID != file.ID() {
			return nil, serializer.NewError(serializer.CodeNotFound, "share link not found", nil)
		}

		if len(existed.Edges.Files) > 0 && args.UploadOnly {
			return nil, serializer.NewError(serializer.CodeParamErr, "upload-only shares cannot cover multiple files", nil)
		}
	}

	password := ""
	if args.IsPrivate {
		password = args.Password
		if strings.TrimSpace(password) == "" {
			password = util.RandString(8, util.RandomLowerCases)
		}
	}

	props := &types.ShareProps{
		ShareView:   args.ShareView,
		ShowReadMe:  args.ShowReadMe,
		HideReadMe:  args.HideReadMe && args.ShowReadMe,
		AllowUpload: args.AllowUpload || args.AllowEdit,
		AllowEdit:   args.AllowEdit,
		PreviewOnly: args.PreviewOnly,
		UploadOnly:  args.UploadOnly,
		Note:        args.Note,
	}

	// Content audit: entities on policies with an audit endpoint are checked
	// before the share record is created.
	if err := l.auditShareEntities(ctx, files); err != nil {
		return nil, err
	}

	fileIDs := lo.Map(files, func(f fs.File, _ int) int { return f.ID() })
	share, err := shareClient.Upsert(ctx, &inventory.CreateShareParams{
		OwnerID:         file.OwnerID(),
		FileID:          file.ID(),
		FileIDs:         fileIDs,
		Password:        password,
		Expires:         args.Expire,
		RemainDownloads: args.RemainDownloads,
		Existed:         existed,
		Props:           props,
		PricePoints:     args.PricePoints,
		ListedPublicly:  args.ListedPublicly && password == "",
		Slug:            args.Slug,
	})

	if err != nil {
		return nil, serializer.NewError(serializer.CodeDBError, "failed to create share", err)
	}

	return share, nil
}

func (m *manager) TraverseFile(ctx context.Context, fileID int) (fs.File, error) {
	return m.fs.TraverseFile(ctx, fileID)
}

func (m *manager) PatchView(ctx context.Context, uri *fs.URI, view *types.ExplorerView) error {
	if uri.PathTrimmed() == "" && uri.FileSystem() != constants.FileSystemMy && uri.FileSystem() != constants.FileSystemShare {
		if m.user.Settings.FsViewMap == nil {
			m.user.Settings.FsViewMap = make(map[string]types.ExplorerView)
		}

		if view == nil {
			delete(m.user.Settings.FsViewMap, string(uri.FileSystem()))
		} else {
			m.user.Settings.FsViewMap[string(uri.FileSystem())] = *view
		}

		if err := m.dep.UserClient().SaveSettings(ctx, m.user); err != nil {
			return serializer.NewError(serializer.CodeDBError, "failed to save user settings", err)
		}

		return nil
	}

	patch := &types.FileProps{
		View: view,
	}
	isDelete := view == nil
	if isDelete {
		patch.View = &types.ExplorerView{}
	}
	if err := m.fs.PatchProps(ctx, uri, patch, isDelete); err != nil {
		return err
	}

	return nil
}

func getEntityDisplayName(f fs.File, e fs.Entity) string {
	switch e.Type() {
	case types.EntityTypeThumbnail:
		return fmt.Sprintf("%s_thumbnail", f.DisplayName())
	case types.EntityTypeLivePhoto:
		return fmt.Sprintf("%s_live_photo.mov", f.DisplayName())
	default:
		return f.Name()
	}
}

func expireTimeToTTL(expireAt *time.Time) int {
	if expireAt == nil {
		return -1
	}

	return int(time.Until(*expireAt).Seconds())
}
