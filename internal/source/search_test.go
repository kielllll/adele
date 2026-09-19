package source

import (
	"testing"
)

// Sample mirrors real `yt-dlp --flat-playlist --dump-single-json`
// output: one full entry, one with null duration/uploader fallback.
const sampleSearch = `{"id": "q", "title": "q", "_type": "playlist", "entries": [` +
	`{"_type": "url", "ie_key": "Youtube", "id": "dQw4w9WgXcQ",` +
	` "url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ",` +
	` "title": "Rick Astley - Never Gonna Give You Up (Official Video)",` +
	` "duration": 214, "channel": "Rick Astley", "uploader": "Rick Astley"},` +
	`{"_type": "url", "ie_key": "Youtube", "id": "abc123",` +
	` "title": "Some Cover",` +
	` "duration": null, "channel": "Cover Channel", "uploader": ""}` +
	`]}`

func TestParseSearch(t *testing.T) {
	tracks, err := parseSearch([]byte(sampleSearch))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(tracks))
	}
	first := tracks[0]
	if first.ID != "dQw4w9WgXcQ" || first.Artist != "Rick Astley" || first.Duration != 214 {
		t.Fatalf("first = %+v", first)
	}
	if first.URL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("url = %q", first.URL)
	}
	second := tracks[1]
	if second.Artist != "Cover Channel" {
		t.Fatalf("fallback artist = %q", second.Artist)
	}
	if second.Duration != 0 || FormatDuration(second.Duration) != "--:--" {
		t.Fatalf("unknown duration = %d", second.Duration)
	}
	if got := FormatDuration(214); got != "3:34" {
		t.Fatalf("FormatDuration = %q, want 3:34", got)
	}
}

func TestParseSearchRejectsGarbage(t *testing.T) {
	if _, err := parseSearch([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
