package dbfs

import (
	"context"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent/user"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	entfile "github.com/cloudreve/Cloudreve/v4/ent/file"
	entmetadata "github.com/cloudreve/Cloudreve/v4/ent/metadata"
	"github.com/cloudreve/Cloudreve/v4/ent/schema"
	"github.com/cloudreve/Cloudreve/v4/ent/storagepolicy"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

// pickPolicyFixture builds an owner in a group whose allowed set is
// [a, b] with `def` as the legacy default, and a dir chain root/dir/target.
func pickPolicyFixture(t *testing.T, client *ent.Client) (*ent.Group, *ent.StoragePolicy, *ent.StoragePolicy, *ent.StoragePolicy, *ent.User, *ent.File, *ent.File) {
	t.Helper()
	ctx := context.Background()

	mk := func(name string) *ent.StoragePolicy {
		return client.StoragePolicy.Create().SetName(name).SetType("local").
			SetStatus(storagepolicy.StatusActive).SaveX(ctx)
	}
	def := mk("def")
	a := mk("a")
	b := mk("b")

	group := client.Group.Create().SetName("g").SetPermissions(&boolset.BooleanSet{}).
		SetStoragePolicies(def).AddAllowedPolicies(a, b).SaveX(ctx)
	owner := client.User.Create().SetEmail("o@example.com").SetNick("o").
		SetGroup(group).SetSettings(&types.UserSetting{PreferredPolicy: b.ID}).SaveX(ctx)

	root := client.File.Create().SetName(inventory.RootFolderName).
		SetType(int(types.FileTypeFolder)).SetOwner(owner).SaveX(ctx)
	dir := client.File.Create().SetName("dir").SetType(int(types.FileTypeFolder)).
		SetOwner(owner).SetParent(root).SaveX(ctx)
	return group, def, a, b, owner, root, dir
}

func pickPolicyDBFS(t *testing.T, client *ent.Client, user *ent.User) *DBFS {
	t.Helper()
	hasher, err := hashid.New("pick-test-salt")
	require.NoError(t, err)
	return &DBFS{
		user:                user,
		fileClient:          inventory.NewFileClient(client, conf.SQLiteDB, hasher),
		storagePolicyClient: inventory.NewStoragePolicyClient(client, nil),
		hasher:              hasher,
		l:                   logging.NewConsoleLogger(logging.LevelError),
	}
}

func wrapChain(models ...*ent.File) *File {
	var parent *File
	for _, m := range models {
		parent = &File{Model: m, Parent: parent}
	}
	return parent
}

func setPreferredMarker(t *testing.T, client *ent.Client, f *ent.File, hasher hashid.Encoder, policyID int) {
	t.Helper()
	ctx := schema.SkipSoftDelete(context.Background())
	client.Metadata.Delete().Where(entmetadata.Name(MetadataPreferredPolicy),
		entmetadata.HasFileWith(entfile.ID(f.ID))).ExecX(ctx)
	client.Metadata.Create().SetFile(f).SetName(MetadataPreferredPolicy).
		SetValue(hashid.EncodePolicyID(hasher, policyID)).SaveX(ctx)
}

// freshChain reloads the file rows so previously lazy-loaded metadata edges
// do not leak between subtests.
func freshChain(t *testing.T, client *ent.Client, ids ...int) *File {
	t.Helper()
	models := make([]*ent.File, len(ids))
	for i, id := range ids {
		models[i] = client.File.GetX(context.Background(), id)
	}
	return wrapChain(models...)
}

func TestPickPolicyPrecedence(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()

	group, def, a, b, owner, root, dir := pickPolicyFixture(t, client)
	owner.SetGroup(group)
	allowed := []*ent.StoragePolicy{a, b, def}

	f := pickPolicyDBFS(t, client, owner)

	t.Run("directory marker wins over user preference", func(t *testing.T) {
		setPreferredMarker(t, client, dir, f.hasher, a.ID)
		got := f.pickPolicy(ctx, freshChain(t, client, root.ID, dir.ID), owner, allowed, 0)
		require.Equal(t, a.ID, got.ID)
	})

	t.Run("user preference wins over group default", func(t *testing.T) {
		client.Metadata.Delete().ExecX(schema.SkipSoftDelete(ctx))
		got := f.pickPolicy(ctx, freshChain(t, client, root.ID, dir.ID), owner, allowed, 0)
		require.Equal(t, b.ID, got.ID)
	})

	t.Run("invalid marker stops ancestor inheritance", func(t *testing.T) {
		// Root carries a valid marker, but the nearer dir marker points at a
		// policy outside the allowed set — must not fall through to root's.
		outside := client.StoragePolicy.Create().SetName("outside").SetType("local").
			SetStatus(storagepolicy.StatusActive).SaveX(ctx)
		setPreferredMarker(t, client, root, f.hasher, a.ID)
		setPreferredMarker(t, client, dir, f.hasher, outside.ID)
		got := f.pickPolicy(ctx, freshChain(t, client, root.ID, dir.ID), owner, allowed, 0)
		require.Equal(t, b.ID, got.ID)
	})

	t.Run("other user's tree ignores user preference", func(t *testing.T) {
		other := client.User.Create().SetEmail("x@example.com").SetNick("x").SetGroup(group).SaveX(ctx)
		otherFs := pickPolicyDBFS(t, client, other)
		got := otherFs.pickPolicy(ctx, freshChain(t, client, root.ID, dir.ID), owner, allowed, 0)
		require.Equal(t, def.ID, got.ID)
	})

	t.Run("group default applies without preferences", func(t *testing.T) {
		ownerNoPref := client.User.Create().SetEmail("np@example.com").SetNick("np").
			SetGroup(group).SetSettings(&types.UserSetting{}).SaveX(ctx)
		ownerNoPref.SetGroup(group)
		fsNoPref := pickPolicyDBFS(t, client, ownerNoPref)
		got := fsNoPref.pickPolicy(ctx, freshChain(t, client, root.ID, dir.ID), ownerNoPref, allowed, 0)
		require.Equal(t, def.ID, got.ID)
	})
}

func TestPickPolicyWeightedCapacity(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()

	mk := func(name string, cap int64) *ent.StoragePolicy {
		return client.StoragePolicy.Create().SetName(name).SetType("local").
			SetStatus(storagepolicy.StatusActive).
			SetSettings(&types.PolicySetting{MaxTotalSize: cap}).SaveX(ctx)
	}
	def := mk("def", 0)
	big := mk("big", 1000)
	small := mk("small", 100)
	uncapped := client.StoragePolicy.Create().SetName("uncapped").SetType("local").
		SetStatus(storagepolicy.StatusActive).
		SetSettings(&types.PolicySetting{}).SaveX(ctx)
	suspended := client.StoragePolicy.Create().SetName("sus").SetType("local").
		SetStatus(storagepolicy.StatusSuspended).
		SetSettings(&types.PolicySetting{MaxTotalSize: 99999}).SaveX(ctx)

	group := client.Group.Create().SetName("g").SetPermissions(&boolset.BooleanSet{}).
		SetStoragePolicies(def).AddAllowedPolicies(big, small, uncapped, suspended).
		SetSettings(&types.GroupSetting{WeightedPolicies: true}).SaveX(ctx)
	owner := client.User.Create().SetEmail("o@example.com").SetNick("o").
		SetGroup(group).SetSettings(&types.UserSetting{}).SaveX(ctx)
	root := client.File.Create().SetName(inventory.RootFolderName).
		SetType(int(types.FileTypeFolder)).SetOwner(owner).SaveX(ctx)

	allowed := []*ent.StoragePolicy{big, small, uncapped, suspended, def}
	f := pickPolicyDBFS(t, client, owner)
	owner.SetGroup(group)

	t.Run("most free capacity wins", func(t *testing.T) {
		got := f.pickPolicy(ctx, wrapChain(root), owner, allowed, 10)
		require.Equal(t, big.ID, got.ID)
	})

	t.Run("skips policy without headroom for the file", func(t *testing.T) {
		got := f.pickPolicy(ctx, wrapChain(root), owner, allowed, 500)
		require.Equal(t, big.ID, got.ID) // small only has 100 free
		got = f.pickPolicy(ctx, wrapChain(root), owner, allowed, 2000)
		require.Equal(t, def.ID, got.ID) // nothing fits -> group default
	})

	t.Run("usage fills shift the pick", func(t *testing.T) {
		client.Entity.Create().SetType(int(types.EntityTypeVersion)).
			SetSource("cloudreve/data/big/" + t.Name()).SetSize(950).
			SetReferenceCount(1).SetCreatedBy(owner.ID).
			SetStoragePolicyEntities(big.ID).SaveX(ctx)
		got := f.pickPolicy(ctx, wrapChain(root), owner, allowed, 10)
		require.Equal(t, small.ID, got.ID) // big now has only 50 free
	})

	t.Run("flag off keeps group default", func(t *testing.T) {
		group.Update().SetSettings(&types.GroupSetting{}).ExecX(ctx)
		owner.SetGroup(client.Group.GetX(ctx, group.ID))
		got := f.pickPolicy(ctx, wrapChain(root), owner, allowed, 10)
		require.Equal(t, def.ID, got.ID)
	})
}

// TestGetRequestedUploadPolicy guards the upload path: a client that names the
// policy its target directory sits on (a cloud-drive policy, say) must land the
// file on that policy rather than the group default, while a policy the owner
// is not allowed to use must be ignored instead of honoured.
func TestGetRequestedUploadPolicy(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	ctx := context.Background()
	_, def, a, b, owner, root, dir := pickPolicyFixture(t, client)

	// A policy that exists but is not in the group's allowed set.
	outsider := client.StoragePolicy.Create().SetName("outsider").SetType("local").
		SetStatus(storagepolicy.StatusActive).SaveX(ctx)

	// The fixture's owner row carries no group edge; reload it so the allowed
	// policy set can be resolved the way it is at runtime.
	owner = client.User.Query().Where(user.ID(owner.ID)).WithGroup().OnlyX(ctx)
	f := pickPolicyDBFS(t, client, owner)

	t.Run("allowed policy is honoured", func(t *testing.T) {
		got, err := f.getRequestedUploadPolicy(ctx, ownedChain(ctx, client, owner, root, dir), a.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, a.ID, got.ID)
	})

	t.Run("policy outside the allowed set falls back", func(t *testing.T) {
		got, err := f.getRequestedUploadPolicy(ctx, ownedChain(ctx, client, owner, root, dir), outsider.ID)
		require.NoError(t, err)
		require.Nil(t, got, "an unallowed policy must not be returned")
	})

	t.Run("suspended allowed policy falls back", func(t *testing.T) {
		suspended := client.StoragePolicy.UpdateOneID(b.ID).
			SetStatus(storagepolicy.StatusSuspended).SaveX(ctx)
		require.Equal(t, storagepolicy.StatusSuspended, suspended.Status)
		got, err := f.getRequestedUploadPolicy(ctx, ownedChain(ctx, client, owner, root, dir), b.ID)
		require.NoError(t, err)
		require.Nil(t, got, "a suspended policy must not be returned")
	})

	t.Run("unknown id falls back", func(t *testing.T) {
		got, err := f.getRequestedUploadPolicy(ctx, ownedChain(ctx, client, owner, root, dir), 99999)
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("requested policy is not the group default", func(t *testing.T) {
		// The premise of the bug: the group default is `def`, so an upload
		// that never consults the request lands on `def`. Naming `a` — an
		// allowed policy — must yield `a` instead.
		require.NotEqual(t, def.ID, a.ID)
		got, err := f.getRequestedUploadPolicy(ctx, ownedChain(ctx, client, owner, root, dir), a.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, a.ID, got.ID)
		require.NotEqual(t, def.ID, got.ID)
	})
}

// ownedChain rebuilds root -> dir with the owner attached so DBFS.Owner()
// resolution works the way it does for a real request.
func ownedChain(ctx context.Context, client *ent.Client, owner *ent.User, root, dir *ent.File) *File {
	parent := &File{Model: client.File.GetX(ctx, root.ID), OwnerModel: owner}
	return &File{Model: client.File.GetX(ctx, dir.ID), OwnerModel: owner, Parent: parent}
}
