package dbfs

import (
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
)

// versionRetentionEnabled mirrors the guard CompleteUpload uses before it reads
// the owner's retention policy. Keeping it here pins the nil handling without
// standing up a full DBFS.
func versionRetentionEnabled(owner *ent.User, fileName string) bool {
	return owner != nil && owner.Settings != nil &&
		owner.Settings.VersionRetention &&
		(len(owner.Settings.VersionRetentionExt) == 0 ||
			util.IsInExtensionList(owner.Settings.VersionRetentionExt, fileName))
}

// TestVersionRetentionToleratesNilOwnerSettings guards the version retention
// lookup in CompleteUpload: a missing owner or nullable settings column must
// fall back to one version, not panic while the last chunk is being finalized.
func TestVersionRetentionToleratesNilOwnerSettings(t *testing.T) {
	if versionRetentionEnabled(nil, "a.png") {
		t.Error("missing owner must not enable version retention")
	}
	if versionRetentionEnabled(&ent.User{}, "a.png") {
		t.Error("nil settings must not enable version retention")
	}
}

func TestVersionRetentionReadsSettingsWhenPresent(t *testing.T) {
	owner := &ent.User{Settings: &types.UserSetting{VersionRetention: true}}
	if !versionRetentionEnabled(owner, "a.png") {
		t.Error("retention enabled in settings should be honoured")
	}

	owner.Settings = &types.UserSetting{VersionRetention: false}
	if versionRetentionEnabled(owner, "a.png") {
		t.Error("retention disabled in settings should not be enabled")
	}

	owner.Settings = &types.UserSetting{VersionRetention: true, VersionRetentionExt: []string{"png"}}
	if !versionRetentionEnabled(owner, "a.png") {
		t.Error("matching extension should be covered")
	}
	if versionRetentionEnabled(owner, "a.bin") {
		t.Error("non matching extension should not be covered")
	}
}
