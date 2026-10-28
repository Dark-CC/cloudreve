package inventory

import (
	"context"
	rawsql "database/sql"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
)

// Cloudreve Pro (up to v4.18) bound each group's usable storage policies via
// a dedicated `group_storage_policies` join table, while the fork models the
// same relation as `group_allowed_policies` plus the `groups.storage_policy_id`
// default. When a Pro database is attached to the fork, ent's auto-migration
// creates `group_allowed_policies` empty and leaves the legacy table behind
// as an orphan, so every ported group loses all of its storage policies and
// uploads fail with 50001 ("No active storage policy available for the
// group") until an admin ports the bindings by hand.
//
// repairProGroupPolicyBindings ports them automatically at startup. The
// statements are:
//   - idempotent: pairs that were already ported — by an earlier run or by
//     the manual fix documented in docs/pro-db-migration-to-fork.md — are
//     skipped, and a group's default is only filled in when unset, so re-runs
//     and hand-fixed databases are left untouched;
//   - portable: plain SQL running unchanged on SQLite, PostgreSQL and MySQL
//     (verified against real Pro v4.18 data on SQLite and PostgreSQL);
//   - non-fatal: this is a compatibility shim for legacy data, so a failure
//     is logged loudly but never blocks the instance from booting — the same
//     policy as the other repair helpers in the migration chain. Both
//     statements are individually idempotent, and the half-applied state in
//     between (bindings copied, defaults still NULL) is already functional:
//     ListByGroup resolves policies through the allowed set alone.
func repairProGroupPolicyBindings(l logging.Logger, client *ent.Client, ctx context.Context) {
	if !proGroupPoliciesTableExists(client, ctx) {
		l.Debug("No legacy Pro group_storage_policies table found, skip group policy binding repair.")
		return
	}

	// Port the many-to-many bindings, skipping pairs that were already
	// ported and pairs whose group or policy no longer exists.
	res, err := client.ExecContext(ctx, `
INSERT INTO group_allowed_policies (group_id, storage_policy_id)
SELECT s.group_id, s.storage_policy_id
FROM group_storage_policies s
WHERE NOT EXISTS (
	SELECT 1 FROM group_allowed_policies t
	WHERE t.group_id = s.group_id AND t.storage_policy_id = s.storage_policy_id
)
AND EXISTS (SELECT 1 FROM groups g WHERE g.id = s.group_id)
AND EXISTS (SELECT 1 FROM storage_policies p WHERE p.id = s.storage_policy_id)`)
	if err != nil {
		l.Error("Failed to port legacy Pro group storage policy bindings; uploads will keep failing with error 50001 until the bindings are fixed. Manual fallback: %s", err)
		return
	}
	bindings, _ := res.RowsAffected()

	// Pro groups carry no single default policy — the fork column is added
	// by the auto-migration and starts NULL — so point each group at its
	// lowest-numbered allowed policy.
	res, err = client.ExecContext(ctx, `
UPDATE groups SET storage_policy_id = (
	SELECT MIN(t.storage_policy_id) FROM group_allowed_policies t
	WHERE t.group_id = groups.id
)
WHERE (storage_policy_id IS NULL OR storage_policy_id = 0)
AND EXISTS (SELECT 1 FROM group_allowed_policies t WHERE t.group_id = groups.id)`)
	if err != nil {
		l.Error("Failed to set default storage policies for ported groups; apply the UPDATE from docs/pro-db-migration-to-fork.md manually: %s", err)
		return
	}
	defaults, _ := res.RowsAffected()

	if bindings == 0 && defaults == 0 {
		l.Debug("Legacy Pro group storage policy bindings are already ported, nothing to do.")
		return
	}
	l.Info("Ported %d group storage policy bindings from legacy Pro table group_storage_policies and set %d group default policies.", bindings, defaults)
}

// proGroupPoliciesTableExists reports whether the legacy Pro join table is
// present in the connected database. The probes are tried in order and the
// first one the dialect understands wins; a probe that errors is a dialect
// mismatch (e.g. SQLite has no information_schema), not a failure.
func proGroupPoliciesTableExists(client *ent.Client, ctx context.Context) bool {
	// MySQL/MariaDB: scope to the current database, the server may host others.
	if rows, err := client.QueryContext(ctx,
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'group_storage_policies' AND table_schema = DATABASE()`); err == nil {
		return scanExistsCount(rows)
	}
	// PostgreSQL: to_regclass resolves through the role's search_path.
	if rows, err := client.QueryContext(ctx,
		`SELECT to_regclass('group_storage_policies') IS NOT NULL`); err == nil {
		return scanExistsBool(rows)
	}
	// SQLite: sqlite_master is the catalog.
	rows, err := client.QueryContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'group_storage_policies'`)
	if err != nil {
		return false
	}
	return scanExistsCount(rows)
}

func scanExistsCount(rows *rawsql.Rows) bool {
	defer rows.Close()
	var n int
	if rows.Next() {
		_ = rows.Scan(&n)
	}
	return n > 0
}

func scanExistsBool(rows *rawsql.Rows) bool {
	defer rows.Close()
	var exists bool
	if rows.Next() {
		_ = rows.Scan(&exists)
	}
	return exists
}
