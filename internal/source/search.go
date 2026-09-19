// Package source searches YouTube via yt-dlp and returns playable tracks.
//
// Playback itself is left to mpv (see internal/player): mpv resolves the
// watch URL through its ytdl hook at play time, so search results never
// carry expirable stream URLs.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// Track is one playable search result.
type Track struct {
	ID       string // YouTube video ID
	Title    string
	Artist   string // uploader / channel
	Duration int    // seconds, 0 when unknown
	URL      string // watch URL handed to mpv
}

// Client shells out to yt-dlp for search.
type Client struct {
	YtdlpPath string
}

// New locates yt-dlp in PATH.
func New() (*Client, error) {
	path, err := exec.LookPath("yt-dlp")
	if err != nil {
		return nil, fmt.Errorf("source: yt-dlp not found in PATH (%s)", installHint())
	}
	return &Client{YtdlpPath: path}, nil
}

func installHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "install yt-dlp: brew install yt-dlp"
	case "windows":
		return "install yt-dlp: winget install yt-dlp"
	default:
		return "install yt-dlp: pip install yt-dlp (or your distro package)"
	}
}

type flatPlaylist struct {
	Entries []struct {
		ID       string   `json:"id"`
		Title    string   `json:"title"`
		Uploader string   `json:"uploader"`
		Channel  string   `json:"channel"`
		Duration *float64 `json:"duration"`
		URL      string   `json:"url"`
	} `json:"entries"`
}

// Search returns up to limit videos matching query.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Track, error) {
	if query == "" {
		return nil, errors.New("source: empty query")
	}
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.YtdlpPath,
		"--no-warnings", "--flat-playlist", "--dump-single-json",
		fmt.Sprintf("ytsearch%d:%s", limit, query))
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("source: yt-dlp search failed: %w", err)
	}
	return parseSearch(out)
}

// parseSearch decodes yt-dlp --dump-single-json output.
func parseSearch(raw []byte) ([]Track, error) {
	var pl flatPlaylist
	if err := json.Unmarshal(raw, &pl); err != nil {
		return nil, fmt.Errorf("source: decode failed: %w", err)
	}
	out := make([]Track, 0, len(pl.Entries))
	for _, e := range pl.Entries {
		if e.ID == "" {
			continue
		}
		artist := e.Uploader
		if artist == "" {
			artist = e.Channel
		}
		url := e.URL
		if url == "" {
			url = "https://www.youtube.com/watch?v=" + e.ID
		}
		dur := 0
		if e.Duration != nil && *e.Duration > 0 {
			dur = int(*e.Duration)
		}
		out = append(out, Track{
			ID: e.ID, Title: e.Title, Artist: artist,
			Duration: dur, URL: url,
		})
	}
	return out, nil
}

// FormatDuration renders seconds as m:ss, or --:-- when unknown.
func FormatDuration(s int) string {
	if s <= 0 {
		return "--:--"
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
