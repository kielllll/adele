# adele

A minimal music-streaming TUI in Go — Spotify-like search, play, and pause,
without playlists. Search the mainstream catalog, pick a track, listen.

## Background

`adele` started as a Jamendo-based player and was reworked onto YouTube
once mainstream catalog mattered more than a cleanroom API. The design
stays deliberately thin:

- **Search** via `yt-dlp ytsearch` (no API key, no account).
- **Playback** via an idle `mpv` subprocess driven over JSON IPC.
  Watch URLs are handed to mpv, which resolves audio itself through its
  yt-dl hook at play time — nothing is downloaded or cached, and there
  are no expirable stream URLs to manage.
- **UI** is a single Bubble Tea screen: search box on top, results in the
  middle, now-playing bar at the bottom.

`mpv` exposes IPC on a Unix socket (macOS/Linux) or a named pipe
(Windows) — it has no TCP listener, so the player abstracts the transport
per OS. If `mpv` is missing, the app still runs search-only and says so
instead of crashing.

> Note: YouTube playback via yt-dlp lives in YouTube's ToS gray area —
> the same trade-off every yt-dlp-based music TUI makes.

## Prerequisites

You need Go, `mpv`, and `yt-dlp` on `PATH`.

**macOS (Homebrew)**

```sh
brew install go mpv yt-dlp
```

**Linux (Debian/Ubuntu)**

```sh
sudo apt install mpv
pip install yt-dlp   # or your distro's yt-dlp package
# Go: https://go.dev/dl/ (1.21+)
```

**Windows (winget)**

```powershell
winget install Go.Go
winget install mpv
winget install yt-dlp.yt-dlp
```

## Run

```sh
go run .
# or
go build -o adele . && ./adele
```

No config files, no API keys, no accounts.

## Keys

| Key | Action |
| --- | ------ |
| type | search (refocuses the box from anywhere) |
| `enter` | run search / play selected track |
| `esc` | leave the search box |
| `space` | pause / resume |
| `n` / `p` | next / previous result (auto-advances at track end) |
| `←` / `→` | seek ∓ 5 s |
| `+` / `-` | volume |
| `s` | stop |
| `/` | focus search |
| `q` / `ctrl+c` | quit |

## Layout

```
main.go                  entrypoint, degraded mode when mpv is missing
internal/source/         YouTube search via yt-dlp
internal/player/         mpv subprocess + JSON IPC (socket / named pipe)
internal/ui/             Bubble Tea 3-pane UI + key handling
docs/v1-spec.md          decisions and behavior spec (git-ignored)
```

## Troubleshooting

- `player: mpv not found in PATH (...)` — install mpv (see above).
- `source: yt-dlp not found in PATH (...)` — install yt-dlp.
- `player: mpv exited during startup` + stderr tail — mpv itself failed;
  read the quoted stderr (often a bad `mpv.conf` or missing yt-dlp for mpv).
- `player: mpv IPC not reachable ... Could not bind IPC socket` — a stale
  socket survived a crash; the player clears it on start, so just retry.
