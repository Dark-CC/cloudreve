package inventory

import (
	"context"
	rawsql "database/sql"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

// proJoinDB builds the four tables repairProGroupPolicyBindings touches:
// fork-shaped groups/group_allowed_policies/storage_policies plus the legacy
// Pro join table. The data mirrors the real Pro v4.18 copy used for the
// migration report: groups 1 and 2 bound to policies 1 and 2, a dangling
// (3, 99) pair, and group 3 without any binding.
func proJoinDB(t *testing.T, dbPath string) {
	t.Helper()
	raw, err := rawsql.Open("sqlite3", "file:"+dbPath)
	require.NoError(t, err)
	defer raw.Close()

	stmts := []string{
		`CREATE TABLE groups (id INTEGER PRIMARY KEY, name TEXT, storage_policy_id INTEGER NULL)`,
		`CREATE TABLE storage_policies (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE group_allowed_policies (group_id INTEGER NOT NULL, storage_policy_id INTEGER NOT NULL, PRIMARY KEY (group_id, storage_policy_id))`,
		`CREATE TABLE group_storage_policies (group_id INTEGER NOT NULL, storage_policy_id INTEGER NOT NULL, PRIMARY KEY (group_id, storage_policy_id))`,
		`INSERT INTO groups (id, name) VALUES (1, 'Admin'), (2, 'User'), (3, 'Anonymous')`,
		`INSERT INTO storage_policies (id, name) VALUES (1, 'local'), (2, 's3')`,
		`INSERT INTO group_storage_policies VALUES (1, 2), (1, 1), (2, 1), (2, 2), (3, 99)`,
	}
	for _, q := range stmts {
		_, err := raw.Exec(q)
		require.NoError(t, err, q)
	}
}

type binding struct {
	GroupID  int
	PolicyID int
}

func queryBindings(t *testing.T, client *ent.Client) []binding {
	t.Helper()
	rows, err := client.QueryContext(context.Background(),
		`SELECT group_id, storage_policy_id FROM group_allowed_policies ORDER BY group_id, storage_policy_id`)
	require.NoError(t, err)
	defer rows.Close()
	var res []binding
	for rows.Next() {
		var b binding
		require.NoError(t, rows.Scan(&b.GroupID, &b.PolicyID))
		res = append(res, b)
	}
	return res
}

func queryGroupDefaults(t *testing.T, client *ent.Client) map[int]*int {
	t.Helper()
	rows, err := client.QueryContext(context.Background(),
		`SELECT id, storage_policy_id FROM groups ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	res := make(map[int]*int)
	for rows.Next() {
		var id int
		var policy rawsql.NullInt64
		require.NoError(t, rows.Scan(&id, &policy))
		if policy.Valid {
			v := int(policy.Int64)
			res[id] = &v
		} else {
			res[id] = nil
		}
	}
	return res
}

func openJoinDB(t *testing.T) *ent.Client {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pro-join.db")
	proJoinDB(t, dbPath)
	client, err := ent.Open("sqlite3", "file:"+dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

// TestRepairProGroupPolicyBindings ports the legacy Pro join table into the
// fork schema: every valid pair is copied, dangling references are skipped,
// and each group's default policy is set to its lowest allowed policy.
func TestRepairProGroupPolicyBindings(t *testing.T) {
	ctx := context.Background()
	l := logging.NewConsoleLogger(logging.LevelError)
	client := openJoinDB(t)

	repairProGroupPolicyBindings(l, client, ctx)

	require.Equal(t, []binding{
		{1, 1}, {1, 2}, {2, 1}, {2, 2},
	}, queryBindings(t, client))

	defaults := queryGroupDefaults(t, client)
	require.Equal(t, 1, *defaults[1])
	require.Equal(t, 1, *defaults[2])
	require.Nil(t, defaults[3], "group without usable policies keeps no default")
}

// TestRepairProGroupPolicyBindingsIdempotent asserts a second run is a no-op:
// already-ported pairs are skipped and set defaults are preserved.
func TestRepairProGroupPolicyBindingsIdempotent(t *testing.T) {
	ctx := context.Background()
	l := logging.NewConsoleLogger(logging.LevelError)
	client := openJoinDB(t)

	repairProGroupPolicyBindings(l, client, ctx)
	require.Equal(t, []binding{{1, 1}, {1, 2}, {2, 1}, {2, 2}}, queryBindings(t, client))
	before := queryGroupDefaults(t, client)

	repairProGroupPolicyBindings(l, client, ctx)
	require.Equal(t, []binding{{1, 1}, {1, 2}, {2, 1}, {2, 2}}, queryBindings(t, client))
	require.Equal(t, before, queryGroupDefaults(t, client))
}

// TestRepairProGroupPolicyBindingsPartial asserts the repair merges with a
// partially-fixed database: a pair already present (manually or from an
// earlier interrupted run) is not duplicated, and an admin-set default is
// never overwritten.
func TestRepairProGroupPolicyBindingsPartial(t *testing.T) {
	ctx := context.Background()
	l := logging.NewConsoleLogger(logging.LevelError)
	client := openJoinDB(t)

	// Group 1 was already ported by hand, with a non-default policy picked.
	_, err := client.ExecContext(ctx,
		`INSERT INTO group_allowed_policies VALUES (1, 1)`)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `UPDATE groups SET storage_policy_id = 2 WHERE id = 1`)
	require.NoError(t, err)

	repairProGroupPolicyBindings(l, client, ctx)

	require.Equal(t, []binding{
		{1, 1}, {1, 2}, {2, 1}, {2, 2},
	}, queryBindings(t, client))

	defaults := queryGroupDefaults(t, client)
	require.Equal(t, 2, *defaults[1], "admin-set default must be preserved")
	require.Equal(t, 1, *defaults[2])
	require.Nil(t, defaults[3])
}

// TestRepairProGroupPolicyBindingsNoLegacyTable asserts fork-native databases
// (no Pro heritage, the allowed set is managed through the fork UI) are left
// untouched.
func TestRepairProGroupPolicyBindingsNoLegacyTable(t *testing.T) {
	ctx := context.Background()
	l := logging.NewConsoleLogger(logging.LevelError)
	client := openJoinDB(t)

	_, err := client.ExecContext(ctx, `DROP TABLE group_storage_policies`)
	require.NoError(t, err)
	require.False(t, proGroupPoliciesTableExists(client, ctx))
	// A pre-existing binding and default stay as-is.
	_, err = client.ExecContext(ctx, `INSERT INTO group_allowed_policies VALUES (1, 2)`)
	require.NoError(t, err)
	_, err = client.ExecContext(ctx, `UPDATE groups SET storage_policy_id = 2 WHERE id = 1`)
	require.NoError(t, err)

	repairProGroupPolicyBindings(l, client, ctx)

	require.Equal(t, []binding{{1, 2}}, queryBindings(t, client))
	defaults := queryGroupDefaults(t, client)
	require.Equal(t, 2, *defaults[1])
	require.Nil(t, defaults[2])
}

// TestProGroupPoliciesTableExists covers the dialect probes on SQLite: the
// legacy table is detected when present and absent once dropped.
func TestProGroupPoliciesTableExists(t *testing.T) {
	ctx := context.Background()
	client := openJoinDB(t)

	require.True(t, proGroupPoliciesTableExists(client, ctx))

	_, err := client.ExecContext(ctx, `DROP TABLE group_storage_policies`)
	require.NoError(t, err)
	require.False(t, proGroupPoliciesTableExists(client, ctx))
}

// TestRepairProGroupPolicyBindingsFullMigration runs the whole migrate()
// chain against a Pro-shaped database (as produced by Pro v4.18 before the
// fork attaches: groups without storage_policy_id, the legacy join table,
// settings carrying the db_version_4.18.0-pro mark) and asserts the repair
// integrates with ent's auto-migration: Schema.Create rebuilds the groups
// table with the new column, and the bindings survive.
func TestRepairProGroupPolicyBindingsFullMigration(t *testing.T) {
	ctx := context.Background()
	l := logging.NewConsoleLogger(logging.LevelError)

	dbPath := filepath.Join(t.TempDir(), "pro-full.db")
	raw, err := rawsql.Open("sqlite3", "file:"+dbPath)
	require.NoError(t, err)
	stmts := []string{
		// Pro-shaped groups: no storage_policy_id column.
		`CREATE TABLE groups (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME NULL, name TEXT NOT NULL, max_storage INTEGER NULL, speed_limit INTEGER NULL, permissions BLOB NOT NULL, settings JSON NULL)`,
		`CREATE TABLE storage_policies (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME NULL, name TEXT NOT NULL, type TEXT NOT NULL, max_size BIGINT NULL, dir_name_rule TEXT NULL, file_name_rule TEXT NULL)`,
		`CREATE TABLE group_storage_policies (group_id INTEGER NOT NULL, storage_policy_id INTEGER NOT NULL, PRIMARY KEY (group_id, storage_policy_id))`,
		`CREATE TABLE settings (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, deleted_at DATETIME NULL, name TEXT NOT NULL, value TEXT NULL)`,
		`INSERT INTO settings (id, created_at, updated_at, name, value) VALUES (1, '2024-01-01 00:00:00', '2024-01-01 00:00:00', 'db_version_4.18.0-pro', 'installed')`,
		`INSERT INTO groups (id, created_at, updated_at, name, max_storage, permissions) VALUES
			(1, '2024-01-01 00:00:00', '2024-01-01 00:00:00', 'Admin', 1099511627776, X'00'),
			(2, '2024-01-01 00:00:00', '2024-01-01 00:00:00', 'User', 1073741824, X'00')`,
		`INSERT INTO storage_policies (id, created_at, updated_at, name, type) VALUES
			(1, '2024-01-01 00:00:00', '2024-01-01 00:00:00', 'Default storage policy', 'local'),
			(2, '2024-01-01 00:00:00', '2024-01-01 00:00:00', 'A', 's3')`,
		`INSERT INTO group_storage_policies VALUES (1, 1), (1, 2), (2, 1)`,
	}
	for _, q := range stmts {
		_, err := raw.Exec(q)
		require.NoError(t, err, q)
	}
	require.NoError(t, raw.Close())

	client, err := ent.Open("sqlite3", "file:"+dbPath)
	require.NoError(t, err)
	defer client.Close()

	// The repair runs inside migrate() after Schema.Create; replicate the
	// relevant part of the chain (schema migration + repair).
	require.NoError(t, client.Schema.Create(ctx))
	repairProGroupPolicyBindings(l, client, ctx)

	require.Equal(t, []binding{
		{1, 1}, {1, 2}, {2, 1},
	}, queryBindings(t, client))
	defaults := queryGroupDefaults(t, client)
	require.Equal(t, 1, *defaults[1])
	require.Equal(t, 1, *defaults[2])
}
