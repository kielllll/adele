package player

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

// The IPC reader must route request_id replies to query waiters while
// still delivering end-file events to OnEnd.
func TestQueryAndEventDemux(t *testing.T) {
	client, server := net.Pipe()
	p := &Player{conn: client, pending: make(map[int]chan ipcReply), nextReq: 1}
	p.writer = bufio.NewWriter(client)
	ended := make(chan struct{}, 1)
	p.OnEnd = func() { ended <- struct{}{} }
	go p.eventLoop()
	defer p.Close()

	type result struct {
		data json.RawMessage
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		d, e := p.query(command("get_property", "time-pos"), 2*time.Second)
		resCh <- result{d, e}
	}()

	sr := bufio.NewReader(server)
	line, err := sr.ReadString('\n')
	if err != nil {
		t.Fatalf("fake mpv read: %v", err)
	}
	var cmd struct {
		RequestID int `json:"request_id"`
	}
	if err := json.Unmarshal([]byte(line), &cmd); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if _, err := fmt.Fprintf(server, `{"request_id":%d,"error":"success","data":83.5}`+"\n", cmd.RequestID); err != nil {
		t.Fatalf("fake mpv write reply: %v", err)
	}
	r := <-resCh
	if r.err != nil {
		t.Fatalf("query: %v", r.err)
	}
	var pos float64
	if err := json.Unmarshal(r.data, &pos); err != nil || pos != 83.5 {
		t.Fatalf("pos = %v, %v; want 83.5", string(r.data), err)
	}

	if _, err := fmt.Fprint(server, `{"event":"end-file","reason":"eof"}`+"\n"); err != nil {
		t.Fatalf("fake mpv write event: %v", err)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("end-file event did not reach OnEnd")
	}
}
