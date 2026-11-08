package inventory

import (
	"context"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

// TestMigrateDefaultSettingsSoftDeleted covers the crash reported when a
// database carries soft-deleted default settings. Their names still occupy the
// settings_name_key unique index while being invisible to ordinary queries, so
// a naive "insert what is missing" loop hits a duplicate key error and takes
// the whole process down on startup (B site, v4.23.9 upgrade).
func TestMigrateDefaultSettingsSoftDeleted(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	l := logging.NewConsoleLogger("debug")

	kv := cache.NewMemoStore("", logging.NewConsoleLogger("warning"))

	// Pick a real default setting and mark it soft-deleted, simulating an admin
	// having removed it.
	var name, value string
	for k, v := range DefaultSettings {
		name, value = k, v
		break
	}
	require.NotEmpty(t, name)

	client.Setting.Create().
		SetName(name).
		SetValue(value).
		SetDeletedAt(time.Now()).
		SaveX(ctx)

	// Before the fix this panicked with
	// `pq: duplicate key value violates unique constraint "settings_name_key"`.
	require.NotPanics(t, func() {
		migrateDefaultSettings(l, client, ctx, kv)
	})

	// The soft-deleted row must still be soft-deleted: resurrecting a setting
	// an admin deliberately removed would change behaviour silently.
	delRows, err := client.QueryContext(ctx, "SELECT COUNT(*) FROM settings WHERE name = ? AND deleted_at IS NOT NULL", name)
	require.NoError(t, err)
	defer delRows.Close()
	var deleted int
	require.True(t, delRows.Next())
	require.NoError(t, delRows.Scan(&deleted))
	require.Equal(t, 1, deleted)

	live, err := client.Setting.Query().Where(setting.NameEQ(name)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, live, "soft-deleted setting must not be re-created as a live row")
}

// TestMigrateDefaultSettingsIdempotent guards the ordinary path: running the
// default-settings step twice must not fail or duplicate rows.
func TestMigrateDefaultSettingsIdempotent(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	l := logging.NewConsoleLogger("warning")
	kv := cache.NewMemoStore("", logging.NewConsoleLogger("warning"))

	require.NotPanics(t, func() { migrateDefaultSettings(l, client, ctx, kv) })
	first, err := client.Setting.Query().Count(ctx)
	require.NoError(t, err)

	require.NotPanics(t, func() { migrateDefaultSettings(l, client, ctx, kv) })
	second, err := client.Setting.Query().Count(ctx)
	require.NoError(t, err)

	require.Equal(t, first, second, "re-running must not create duplicate settings")
}
