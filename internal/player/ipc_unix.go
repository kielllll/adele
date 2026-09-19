//go:build !windows

package player

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ipcEndpoint returns the mpv --input-ipc-server value and dials it.
// Unix sockets work on macOS, Linux and BSDs.
func ipcEndpoint() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("adele-%d.sock", os.Getpid()))
}

func dialIPC(endpoint string) (net.Conn, error) {
	return net.DialTimeout("unix", endpoint, 500*time.Millisecond)
}
