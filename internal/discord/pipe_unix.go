//go:build !windows

package discord

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// errNoSocket is returned when no Discord IPC endpoint exists, which normally
// just means Discord is not running.
var errNoSocket = errors.New("no Discord IPC socket found (is Discord running?)")

// dialDiscord finds and connects to Discord's Unix domain socket.
//
// Discord numbers its sockets 0-9 so that several clients can coexist, and it
// places them in the runtime or temporary directory. Both are re-read on every
// dial: after a Discord restart the socket can move to a different index.
func dialDiscord() (net.Conn, error) {
	for _, dir := range socketDirs() {
		for i := 0; i < 10; i++ {
			path := filepath.Join(dir, fmt.Sprintf("discord-ipc-%d", i))
			if _, err := os.Stat(path); err != nil {
				continue
			}
			conn, err := net.DialTimeout("unix", path, 2*time.Second)
			if err != nil {
				// A stale socket file left by a crashed Discord; keep looking.
				continue
			}
			return conn, nil
		}
	}
	return nil, errNoSocket
}

// socketDirs lists the directories Discord may keep its socket in, most
// specific first.
func socketDirs() []string {
	var dirs []string
	seen := make(map[string]bool)
	add := func(d string) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		dirs = append(dirs, d)
	}

	for _, env := range []string{"XDG_RUNTIME_DIR", "TMPDIR", "TMP", "TEMP"} {
		add(os.Getenv(env))
	}
	add("/tmp")

	// Flatpak and Snap builds of Discord are confined to a sandbox directory
	// underneath the ones above.
	for _, d := range append([]string(nil), dirs...) {
		add(filepath.Join(d, "app", "com.discordapp.Discord"))
		add(filepath.Join(d, "snap.discord"))
	}
	return dirs
}
