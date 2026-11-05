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

// TestPhysicalPathWithEmptyRules is the regression guard for an OpenList
// policy stored with both path rules empty (the type had no server side
// default, so a policy created outside the admin wizard or by an older release
// carried none). Such a policy expanded to an empty save path: the colliding
// empty object name either failed the upload or completed it against a
// nameless object, and the file was left listed as "uploading" forever.
// A policy without rules must fall back to the same rules a wizard sets.
func TestPhysicalPathWithEmptyRules(t *testing.T) {
	policy := &ent.StoragePolicy{DirNameRule: "", FileNameRule: ""}

	uri, err := fs.NewUriFromString("cloudreve://my/w/230x0w.png")
	if err != nil {
		t.Fatalf("failed to parse the uri: %s", err)
	}

	got := PhysicalPathForURI(policy, uri, 1)
	want := "uploads/1/w/230x0w.png"
	if got != want {
		t.Fatalf("PhysicalPathForURI with empty rules = %q, want %q", got, want)
	}

	dir, err := fs.NewUriFromString("cloudreve://my/w")
	if err != nil {
		t.Fatalf("failed to parse the folder uri: %s", err)
	}

	if got := PhysicalDirForURI(policy, dir, 1); got != "uploads/1/w" {
		t.Fatalf("PhysicalDirForURI with empty rules = %q, want %q", got, "uploads/1/w")
	}
}

// TestPhysicalPathWithUnusableNameRule covers rules that expand to something
// that is not a file name: an empty file rule, and one that keeps a separator.
// Both must fall back to the original name instead of addressing a directory on
// the storage.
func TestPhysicalPathWithUnusableNameRule(t *testing.T) {
	uri, err := fs.NewUriFromString("cloudreve://my/w/a.png")
	if err != nil {
		t.Fatalf("failed to parse the uri: %s", err)
	}

	for _, rule := range []string{"", "   ", "/", "a/b"} {
		policy := &ent.StoragePolicy{DirNameRule: "uploads/{uid}/{path}", FileNameRule: rule}
		if got, want := PhysicalPathForURI(policy, uri, 1), "uploads/1/w/a.png"; got != want {
			t.Errorf("file rule %q gave %q, want %q (the original name)", rule, got, want)
		}
	}
}
