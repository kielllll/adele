//go:build windows

package player

import (
	"fmt"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
)

// ipcEndpoint returns the mpv --input-ipc-server value and dials it.
// Go's net package cannot dial Windows named pipes, so winio is used.
func ipcEndpoint() string {
	return fmt.Sprintf(`\\.\pipe\adele-%d`, os.Getpid())
}

func dialIPC(endpoint string) (net.Conn, error) {
	return winio.DialPipe(endpoint, nil)
}
