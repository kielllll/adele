// Package player drives an mpv subprocess over JSON IPC.
//
// Transport is OS-specific: a Unix socket on macOS/Linux/BSDs, a named
// pipe on Windows (mpv offers no TCP listener). The JSON protocol on top
// is identical everywhere.
package player

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// installHint returns a per-OS mpv install hint for startup errors.
func installHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "install mpv: brew install mpv"
	case "windows":
		return "install mpv: winget install mpv"
	default:
		return "install mpv: apt install mpv  (or your distro equivalent)"
	}
}

// ring keeps the tail of mpv's stderr for failure reports.
type ring struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, _ := r.buf.Write(p)
	const max = 4096
	if r.buf.Len() > max {
		r.buf.Next(r.buf.Len() - max)
	}
	return n, nil
}

func (r *ring) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// Player controls one idle mpv process.
type Player struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	conn     net.Conn
	writer   *bufio.Writer
	endpoint string
	waitCh   chan error
	nextReq  int
	// OnEnd is called when mpv reports end-file (track finished).
	OnEnd func()
}

// New starts `mpv --idle` with a JSON IPC endpoint and connects to it.
func New() (*Player, error) {
	mpvPath, err := exec.LookPath("mpv")
	if err != nil {
		return nil, fmt.Errorf("player: mpv not found in PATH (%s)", installHint())
	}
	return start(mpvPath)
}

// start launches mpvPath and waits for its IPC endpoint. It is split out
// for tests, which pass a fake binary to exercise startup failure paths.
func start(mpvPath string) (*Player, error) {
	endpoint := ipcEndpoint()
	// A stale socket from a crashed run would stop mpv binding.
	_ = os.Remove(endpoint)

	var stderr ring
	cmd := exec.Command(mpvPath,
		"--no-video", "--idle=yes", "--no-terminal",
		"--ytdl-format=bestaudio",
		"--input-ipc-server="+endpoint)
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("player: start mpv: %w", err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case werr := <-waitCh:
			msg := "player: mpv exited during startup"
			if werr != nil {
				msg += ": " + werr.Error()
			}
			if tail := stderr.String(); tail != "" {
				msg += "\nmpv stderr:\n" + tail
			}
			return nil, errors.New(msg)
		default:
		}
		conn, err := dialIPC(endpoint)
		if err == nil {
			p := &Player{
				cmd: cmd, conn: conn, endpoint: endpoint,
				waitCh: waitCh, nextReq: 1,
			}
			p.writer = bufio.NewWriter(conn)
			if err := p.observe("end-file"); err != nil {
				_ = p.Close()
				return nil, err
			}
			go p.eventLoop()
			return p, nil
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			<-waitCh // reap
			msg := fmt.Sprintf("player: mpv IPC not reachable at %s", endpoint)
			if tail := stderr.String(); tail != "" {
				msg += "\nmpv stderr:\n" + tail
			} else {
				msg += fmt.Sprintf(" (%s)", installHint())
			}
			return nil, errors.New(msg)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// send writes one IPC command. Replies are not read: eventLoop owns conn
// reads (end-file events), so write errors are the failure signal.
func (p *Player) send(cmd any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := p.nextReq
	p.nextReq++
	raw, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	m["request_id"] = id
	raw, _ = json.Marshal(m)
	if _, err := p.writer.Write(append(raw, '\n')); err != nil {
		return err
	}
	return p.writer.Flush()
}

// command builds a raw mpv command array.
func command(args ...any) map[string]any {
	return map[string]any{"command": args}
}

func (p *Player) observe(event string) error {
	return p.send(command("observe_property", 1, event))
}

// Play loads url, replacing the current playlist entry.
func (p *Player) Play(url string) error {
	if url == "" {
		return errors.New("player: empty URL")
	}
	return p.send(command("loadfile", url, "replace"))
}

// SetPaused pauses (true) or resumes (false) playback.
func (p *Player) SetPaused(paused bool) error {
	return p.send(command("set_property", "pause", paused))
}

// Stop clears the playlist.
func (p *Player) Stop() error {
	return p.send(command("playlist-clear"))
}

// Seek moves playback by deltaSeconds (may be negative).
func (p *Player) Seek(deltaSeconds int) error {
	return p.send(command("seek", deltaSeconds, "relative"))
}

// SetVolume sets mpv volume (UI clamps to 0-100).
func (p *Player) SetVolume(v int) error {
	return p.send(command("set_property", "volume", v))
}

// eventLoop watches for end-file events to trigger auto-advance.
func (p *Player) eventLoop() {
	sc := bufio.NewScanner(p.conn)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var msg struct {
			Event  string `json:"event"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Event == "end-file" && msg.Reason == "eof" && p.OnEnd != nil {
			p.OnEnd()
		}
	}
}

// Close terminates mpv and removes the socket file.
func (p *Player) Close() error {
	if p.conn != nil {
		_ = p.conn.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	if p.waitCh != nil {
		select {
		case <-p.waitCh:
		case <-time.After(5 * time.Second):
		}
	}
	if p.endpoint != "" {
		_ = os.Remove(p.endpoint)
	}
	return nil
}
