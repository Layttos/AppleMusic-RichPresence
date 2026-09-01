//go:build windows

package discord

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// errNoSocket is returned when no Discord IPC endpoint exists, which normally
// just means Discord is not running.
var errNoSocket = errors.New("no Discord IPC pipe found (is Discord running?)")

// dialDiscord connects to Discord's named pipe.
//
// Windows has no Unix sockets, so Discord exposes the same protocol over
// \\.\pipe\discord-ipc-N. go-winio is used rather than os.OpenFile because it
// returns a net.Conn with working deadlines, and the client relies on those to
// avoid parking forever on a wedged pipe.
func dialDiscord() (net.Conn, error) {
	timeout := 2 * time.Second
	for i := 0; i < 10; i++ {
		path := fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i)
		conn, err := winio.DialPipe(path, &timeout)
		if err != nil {
			continue
		}
		return conn, nil
	}
	return nil, errNoSocket
}
