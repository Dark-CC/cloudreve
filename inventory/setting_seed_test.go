package inventory

import (
	"context"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

// A settings row is only ever written by the one-shot migration, so a database
// that was already at the current version before a setting was introduced never
// gets that row. The admin settings form then loads an undefined value, which
// blocks the whole form from saving. Gets backfills from the defaults instead.
func TestGetsFillsMissingDefaults(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	// Present row keeps its stored value, untouched by the default.
	client.Setting.Create().SetName("email_active").SetValue("1").SaveX(ctx)

	c := NewSettingClient(client, nil)
	res, err := c.Gets(ctx, []string{"email_active", "sso_display_name", "invitation_code_required", "totally_unknown_key"})
	require.NoError(t, err)

	require.Equal(t, "1", res["email_active"], "stored value must win over the default")
	require.Equal(t, DefaultSettings["sso_display_name"], res["sso_display_name"])
	require.Equal(t, DefaultSettings["invitation_code_required"], res["invitation_code_required"])
	require.NotContains(t, res, "totally_unknown_key", "unknown keys must not be invented")
}

// seedMissingDefaultSettings is what heals an already-upgraded database on
// boot: it inserts absent defaults and leaves existing rows alone.
func TestSeedMissingDefaultSettings(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	l := logging.NewConsoleLogger(logging.LevelWarning)

	client.Setting.Create().SetName("sso_display_name").SetValue("Custom").SaveX(ctx)

	seedMissingDefaultSettings(l, client, ctx)

	stored, err := client.Setting.Query().Where(setting.Name("sso_display_name")).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "Custom", stored.Value, "existing rows must not be overwritten")

	for _, k := range []string{"invitation_code_required", "email_subaddress_chars", "email_filter_mode"} {
		require.True(t, client.Setting.Query().Where(setting.Name(k)).ExistX(ctx), "%s should have been seeded", k)
	}

	// Idempotent: a second run must not create duplicates.
	count := client.Setting.Query().CountX(ctx)
	seedMissingDefaultSettings(l, client, ctx)
	require.Equal(t, count, client.Setting.Query().CountX(ctx), "seeding twice must not add rows")
}
