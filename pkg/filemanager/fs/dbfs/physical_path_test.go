package dbfs

import (
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
)

// TestPhysicalPathForURIMatchesUploadRule pins the path a file occupies on a
// path addressed storage to the rules the upload used, so a rename or move
// recomputes the same address the file was written to.
func TestPhysicalPathForURIMatchesUploadRule(t *testing.T) {
	policy := &ent.StoragePolicy{
		DirNameRule:  "uploads/{uid}/{path}",
		FileNameRule: "{originname}",
	}

	cases := []struct {
		uri  string
		want string
	}{
		{"cloudreve://my/w/small.txt", "uploads/1/w/small.txt"},
		{"cloudreve://my/w/sub/deep.bin", "uploads/1/w/sub/deep.bin"},
		{"cloudreve://my/a.txt", "uploads/1/a.txt"},
	}

	for _, c := range cases {
		uri, err := fs.NewUriFromString(c.uri)
		if err != nil {
			t.Fatalf("failed to parse %q: %s", c.uri, err)
		}

		if got := PhysicalPathForURI(policy, uri, 1); got != c.want {
			t.Errorf("PhysicalPathForURI(%q) = %q, want %q", c.uri, got, c.want)
		}
	}
}

// TestPhysicalDirForURIExpandsFolderOnce guards the move destination: a folder
// expands to its own directory, so the file name rule must not be appended to
// it a second time.
func TestPhysicalDirForURIExpandsFolderOnce(t *testing.T) {
	policy := &ent.StoragePolicy{
		DirNameRule:  "uploads/{uid}/{path}",
		FileNameRule: "{originname}",
	}

	uri, err := fs.NewUriFromString("cloudreve://my/w/sub")
	if err != nil {
		t.Fatalf("failed to parse the uri: %s", err)
	}

	if got, want := PhysicalDirForURI(policy, uri, 1), "uploads/1/w/sub"; got != want {
		t.Fatalf("PhysicalDirForURI = %q, want %q (the destination folder itself)", got, want)
	}
}
