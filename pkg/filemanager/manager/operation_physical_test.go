package manager

import (
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
)

// TestExtendedStoragePolicyNilExtendedInfo covers the regression that made
// renaming a file panic: physicalRenameTarget asked a file that was loaded
// without WithExtendedInfo for its storage policy, and the nil pointer was
// dereferenced. A file without extended info must resolve to "no storage side
// operation" instead of crashing the request.
func TestExtendedStoragePolicyNilExtendedInfo(t *testing.T) {
	file := &dbfs.File{Model: &ent.File{}}

	if got := extendedStoragePolicy(file); got != nil {
		t.Fatalf("expected nil policy for a file without extended info, got %+v", got)
	}
}

// TestExtendedStoragePolicyReturnsPolicy is the positive counterpart: a file
// loaded with extended info hands back the policy the storage side operation
// needs.
func TestExtendedStoragePolicyReturnsPolicy(t *testing.T) {
	policy := &ent.StoragePolicy{ID: 7, Type: "openlist", Name: "ol"}
	file := &dbfs.File{
		Model:            &ent.File{},
		FileExtendedInfo: &fs.FileExtendedInfo{StoragePolicy: policy},
	}

	got := extendedStoragePolicy(file)
	if got == nil || got.ID != policy.ID {
		t.Fatalf("expected policy %d, got %+v", policy.ID, got)
	}
}
