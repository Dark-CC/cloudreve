package inventory

import (
	"context"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent/entity"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/stretchr/testify/require"
)

// Instant upload (hash dedup) makes several files share one source entity, and
// a thumbnail's blob path is derived from that source entity — so those files
// share one thumbnail entity too. These tests pin the lookup and the link
// semantics the thumbnail generator relies on to reuse it instead of writing
// the same object a second time (which y139 rejects with "object existed").
func TestGetThumbEntityBySource(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()

	group := client.Group.Create().SetName("g").SetPermissions(&boolset.BooleanSet{}).SaveX(ctx)
	owner := client.User.Create().SetEmail("o@example.com").SetNick("o").SetGroup(group).SaveX(ctx)
	root := client.File.Create().SetName(RootFolderName).SetType(int(types.FileTypeFolder)).SetOwner(owner).SaveX(ctx)
	policy := client.StoragePolicy.Create().SetName("p").SetType("local").SetServer("").SaveX(ctx)

	source := "uploads/1/blob.mp4"
	thumbSource := source + "._thumb"

	hasher, err := hashid.New("thumb-dedup-test-salt")
	require.NoError(t, err)
	fc := NewFileClient(client, conf.SQLiteDB, hasher)

	// Nothing stored yet.
	_, err = fc.GetThumbEntityBySource(ctx, thumbSource, policy.ID)
	require.Error(t, err)

	// The first file generates the thumbnail onto its own entity list.
	first := client.File.Create().
		SetName("a.mp4").SetType(int(types.FileTypeFile)).SetOwner(owner).SetParent(root).
		SetStoragePoliciesID(policy.ID).SaveX(ctx)
	srcEntity := client.Entity.Create().
		SetType(int(types.EntityTypeVersion)).SetSource(source).SetSize(10).
		SetStoragePolicyEntities(policy.ID).AddFile(first).SaveX(ctx)

	thumbEntity := client.Entity.Create().
		SetType(int(types.EntityTypeThumbnail)).SetSource(thumbSource).SetSize(3).
		SetStoragePolicyEntities(policy.ID).AddFile(first).SaveX(ctx)
	// The entity is shared by two files, so its refcount is 2 from the start.
	client.Entity.UpdateOne(thumbEntity).SetReferenceCount(1).ExecX(ctx)

	found, err := fc.GetThumbEntityBySource(ctx, thumbSource, policy.ID)
	require.NoError(t, err)
	require.Equal(t, thumbEntity.ID, found.ID)

	// A thumbnail entity under a different policy must not be handed back —
	// its blob lives on another storage.
	otherPolicy := client.StoragePolicy.Create().SetName("p2").SetType("local").SetServer("x").SaveX(ctx)
	client.Entity.Create().
		SetType(int(types.EntityTypeThumbnail)).SetSource(thumbSource).SetSize(3).
		SetStoragePolicyEntities(otherPolicy.ID).SaveX(ctx)

	_, err = fc.GetThumbEntityBySource(ctx, thumbSource, otherPolicy.ID)
	require.NoError(t, err)

	found2, err := fc.GetThumbEntityBySource(ctx, thumbSource, policy.ID)
	require.NoError(t, err)
	require.Equal(t, thumbEntity.ID, found2.ID)

	// A version entity at the same path is not a thumbnail and must not match.
	_, err = fc.GetThumbEntityBySource(ctx, source, policy.ID)
	require.Error(t, err, "the source entity itself is a version, not a thumbnail")

	require.NotZero(t, srcEntity.ID)
}

// Linking an existing thumbnail entity to a second file has to bump the
// reference count, exactly like instant upload attaches a shared source
// entity; otherwise the entity would be collected while still in use.
func TestLinkEntitySharesThumbnail(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()

	group := client.Group.Create().SetName("g").SetPermissions(&boolset.BooleanSet{}).SaveX(ctx)
	owner := client.User.Create().SetEmail("o@example.com").SetNick("o").SetGroup(group).SaveX(ctx)
	root := client.File.Create().SetName(RootFolderName).SetType(int(types.FileTypeFolder)).SetOwner(owner).SaveX(ctx)
	policy := client.StoragePolicy.Create().SetName("p").SetType("local").SetServer("").SaveX(ctx)

	first := client.File.Create().
		SetName("a.mp4").SetType(int(types.FileTypeFile)).SetOwner(owner).SetParent(root).
		SetStoragePoliciesID(policy.ID).SaveX(ctx)
	second := client.File.Create().
		SetName("a-copy.mp4").SetType(int(types.FileTypeFile)).SetOwner(owner).SetParent(root).
		SetStoragePoliciesID(policy.ID).SaveX(ctx)

	thumb := client.Entity.Create().
		SetType(int(types.EntityTypeThumbnail)).SetSource("uploads/1/blob.mp4._thumb").SetSize(3).
		SetStoragePolicyEntities(policy.ID).AddFile(first).SaveX(ctx)

	hasher, err := hashid.New("thumb-dedup-test-salt-2")
	require.NoError(t, err)
	fc := NewFileClient(client, conf.SQLiteDB, hasher)

	secondModel, err := fc.GetByID(ctx, second.ID)
	require.NoError(t, err)
	require.NoError(t, fc.LinkEntity(ctx, thumb, secondModel))

	reloaded, err := client.Entity.Get(ctx, thumb.ID)
	require.NoError(t, err)
	require.Equal(t, 2, reloaded.ReferenceCount)

	linked, err := client.Entity.Query().Where(entity.ID(thumb.ID)).QueryFile().All(ctx)
	require.NoError(t, err)
	require.Len(t, linked, 2)
}
