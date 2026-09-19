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

// ipcReply is one mpv response to a request_id command.
type ipcReply struct {
	data json.RawMessage
	err  string
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
	pending  map[int]chan ipcReply
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
				pending: make(map[int]chan ipcReply),
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

// send writes one fire-and-forget IPC command. Any reply mpv sends back
// carries the request_id but has no waiter, so eventLoop drops it; write
// errors are the failure signal.
func (p *Player) send(cmd any) error {
	return p.writeCmd(cmd, nil)
}

// writeCmd writes one IPC command stamped with a fresh request_id. When
// waiter is non-nil it is registered first so the reply is routed back.
func (p *Player) writeCmd(cmd any, waiter chan ipcReply) error {
	p.mu.Lock()
	id := p.nextReq
	p.nextReq++
	if waiter != nil {
		if p.pending == nil {
			p.pending = make(map[int]chan ipcReply)
		}
		p.pending[id] = waiter
	}
	raw, err := json.Marshal(cmd)
	if err != nil {
		if waiter != nil {
			delete(p.pending, id)
		}
		p.mu.Unlock()
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		if waiter != nil {
			delete(p.pending, id)
		}
		p.mu.Unlock()
		return err
	}
	m["request_id"] = id
	raw, _ = json.Marshal(m)
	if _, err := p.writer.Write(append(raw, '\n')); err != nil {
		if waiter != nil {
			delete(p.pending, id)
		}
		p.mu.Unlock()
		return err
	}
	err = p.writer.Flush()
	p.mu.Unlock()
	return err
}

// query sends one IPC command and waits for its reply.
func (p *Player) query(cmd any, timeout time.Duration) (json.RawMessage, error) {
	waiter := make(chan ipcReply, 1)
	if err := p.writeCmd(cmd, waiter); err != nil {
		return nil, err
	}
	select {
	case r := <-waiter:
		if r.err != "" && r.err != "success" {
			return nil, fmt.Errorf("player: mpv error: %s", r.err)
		}
		return r.data, nil
	case <-time.After(timeout):
		p.mu.Lock()
		for id, ch := range p.pending {
			if ch == waiter {
				delete(p.pending, id)
				break
			}
		}
		p.mu.Unlock()
		return nil, errors.New("player: mpv query timed out")
	}
}

// getProperty reads one mpv property (e.g. time-pos, duration).
func (p *Player) getProperty(prop string) (json.RawMessage, error) {
	return p.query(command("get_property", prop), 2*time.Second)
}

// Position returns playback position and duration in seconds. It errors
// when mpv is idle or a property is unavailable.
func (p *Player) Position() (pos, dur float64, err error) {
	posRaw, err := p.getProperty("time-pos")
	if err != nil {
		return 0, 0, err
	}
	if err := json.Unmarshal(posRaw, &pos); err != nil {
		return 0, 0, fmt.Errorf("player: decode time-pos: %w", err)
	}
	durRaw, err := p.getProperty("duration")
	if err != nil {
		return pos, 0, nil // position known, duration unknown
	}
	if err := json.Unmarshal(durRaw, &dur); err != nil {
		return pos, 0, nil
	}
	return pos, dur, nil
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

// eventLoop demuxes incoming frames: replies carrying request_id go to
// their query waiter, event frames drive end-file auto-advance.
func (p *Player) eventLoop() {
	defer p.failPending()
	sc := bufio.NewScanner(p.conn)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var msg struct {
			RequestID *int            `json:"request_id"`
			Error     string          `json:"error"`
			Data      json.RawMessage `json:"data"`
			Event     string          `json:"event"`
			Reason    string          `json:"reason"`
		}
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.RequestID != nil {
			p.mu.Lock()
			ch := p.pending[*msg.RequestID]
			delete(p.pending, *msg.RequestID)
			p.mu.Unlock()
			if ch != nil {
				ch <- ipcReply{data: msg.Data, err: msg.Error}
			}
			continue
		}
		if msg.Event == "end-file" && msg.Reason == "eof" && p.OnEnd != nil {
			p.OnEnd()
		}
	}
}

// failPending releases query waiters when the connection drops.
func (p *Player) failPending() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, ch := range p.pending {
		ch <- ipcReply{err: "connection closed"}
		delete(p.pending, id)
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
