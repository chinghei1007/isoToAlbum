package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"isotoalbum/internal/sacd"
)

func TestValidateManifestAllowsApprovedDefaults(t *testing.T) {
	source := &scan{info: sacd.DiscInfo{Areas: []sacd.Area{{ID: "stereo"}}}}
	err := validateManifest(source, Manifest{Format: "flac", Area: "stereo", PCMRate: 176400, Destination: "browser"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWriteTrackList(t *testing.T) {
	root := t.TempDir()
	tracks := []sacd.Track{{Index: 0, Title: "So What"}, {Index: 1, Title: "Freddie Freeloader"}}
	if err := writeTrackList(tracks, root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "tracklist.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "\ufeff01. So What\n02. Freddie Freeloader\n" {
		t.Fatalf("unexpected track list: %q", data)
	}
}

func TestCleanName(t *testing.T) {
	if got := cleanName("../bad:name?", "fallback"); !strings.HasPrefix(got, "bad-name") {
		t.Fatalf("unexpected clean name: %q", got)
	}
}
