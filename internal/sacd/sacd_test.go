package sacd

import (
	"reflect"
	"testing"
)

const sampleInfo = `
Album Information:
	Sequence Number: 1; Set Size: 1
	Title: Kind of Blue
	Artist: Miles Davis

Disc Information:
	Version:  1.20

Area count: 2
	Area Information [0]:
	Track Count: 3
	Total play time: 24:45:00 [mins:secs:frames]
	Frame format encoding: [01] [DST]
	Speaker config: 2 Channel
	Track list of area[0]:
		Title[1]: So What
		Performer[1]: Miles Davis
		Duration: 09:22:37 [mins:secs:frames]
		Title[2]: Freddie Freeloader
		Duration: 09:46:00 [mins:secs:frames]
		Title[3]: Blue in Green
		Duration: 05:37:00 [mins:secs:frames]

	Area Information [1]:
	Track Count: 3
	Total play time: 24:45:00 [mins:secs:frames]
	Frame format encoding: [01] [DSD]
	Speaker config: 6 Channel
	Track list of area[1]:
		Title[1]: So What
		Duration: 09:22:37 [mins:secs:frames]
		Title[2]: Freddie Freeloader
		Duration: 09:46:00 [mins:secs:frames]
		Title[3]: Blue in Green
		Duration: 05:37:00 [mins:secs:frames]
`

func TestParseInfo(t *testing.T) {
	info, err := ParseInfo(sampleInfo)
	if err != nil {
		t.Fatal(err)
	}
	if info.Album != "Kind of Blue" || info.Artist != "Miles Davis" || !info.DST {
		t.Fatalf("unexpected disc metadata: %#v", info)
	}
	if len(info.Areas) != 2 || info.Areas[0].ID != "stereo" || info.Areas[1].ID != "multichannel" {
		t.Fatalf("unexpected areas: %#v", info.Areas)
	}
	if len(info.Tracks) != 3 || info.Tracks[0].Title != "So What" || info.Tracks[0].Duration != "09:22" {
		t.Fatalf("unexpected tracks: %#v", info.Tracks)
	}
	if !reflect.DeepEqual(info.Tracks[0].Areas, []string{"stereo", "multichannel"}) {
		t.Fatalf("unexpected track areas: %#v", info.Tracks[0].Areas)
	}
}

func TestExtractArgsProvideBothNativeContainers(t *testing.T) {
	dsf, err := extractArgs("album.iso", "out", ExtractOptions{Format: "dsf", Area: "stereo"})
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(dsf, "--dsf", "--convert-dst", "--stereo") {
		t.Fatalf("unexpected DSF args: %#v", dsf)
	}
	dff, err := extractArgs("album.iso", "out", ExtractOptions{Format: "dff", Area: "stereo", PreserveDST: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(dff, "--dsdiff", "--stereo") || containsAll(dff, "--convert-dst") {
		t.Fatalf("unexpected DFF args: %#v", dff)
	}
}

func TestExtractArgsUsesApprovedPCMDefault(t *testing.T) {
	args, err := extractArgs("album.iso", "out", ExtractOptions{Format: "flac", Area: "stereo", PCMRate: 176400})
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(args, "--flac", "--flac-rate", "176400") {
		t.Fatalf("unexpected PCM args: %#v", args)
	}
}

func containsAll(values []string, expected ...string) bool {
	for _, target := range expected {
		found := false
		for _, value := range values {
			if value == target {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
