# adele

A minimal music-streaming TUI in Go. Search, play, and pause,
without playlists. Search the mainstream catalog, pick a track, listen.

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

| Key            | Action                                              |
| -------------- | --------------------------------------------------- |
| type           | search (refocuses the box from anywhere)            |
| `enter`        | run search / play selected track                    |
| `esc`          | leave the search box                                |
| `space`        | pause / resume                                      |
| `n` / `p`      | next / previous result (auto-advances at track end) |
| `←` / `→`      | seek ∓ 5 s                                          |
| `+` / `-`      | volume                                              |
| `s`            | stop                                                |
| `/`            | focus search                                        |
| `q` / `ctrl+c` | quit                                                |

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
