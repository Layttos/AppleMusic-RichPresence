package player

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// musicProcess is the executable name of Apple Music as the kernel reports it.
const musicProcess = "Music"

// fieldSep separates the fields the AppleScript hands back. ASCII 31 (unit
// separator) is used instead of a printable character because track titles are
// free-form text: an album called "A|B" would silently corrupt the parse.
const fieldSep = "\x1f"

// nowPlayingScript reads the player state in one AppleScript round trip.
//
// It returns "X" when Music is not running, "S" when it is running but has no
// advancing track (stopped, or the library view is open with nothing loaded),
// and otherwise a fieldSep-separated record. Position and duration come back as
// whole milliseconds so that we never have to parse a locale-dependent decimal
// separator or AppleScript's scientific notation.
//
// The inner `with timeout` bounds the Apple Event itself: without it a wedged
// Music process would block the script for the default two minutes.
const nowPlayingScript = `
with timeout of 4 seconds
	tell application "Music"
		if it is not running then return "X"
		set playState to (player state as text)
		if (playState is not "playing") and (playState is not "paused") then return "S"
		try
			set theTrack to current track
			set theName to (name of theTrack as text)
			set theArtist to (artist of theTrack as text)
			set theAlbum to (album of theTrack as text)
			set durMs to (round ((duration of theTrack) * 1000))
		on error
			return "S"
		end try
		set posMs to 0
		try
			set posMs to (round ((player position) * 1000))
		end try
		set sep to (character id 31)
		return playState & sep & theName & sep & theArtist & sep & theAlbum & sep & (posMs as text) & sep & (durMs as text)
	end tell
end timeout
`

// darwinPlayer talks to Apple Music through AppleScript, but only once it has
// confirmed the app is actually running. That check is a plain sysctl, which
// costs microseconds; spawning osascript costs a process plus an Apple Event
// round trip, so skipping it while Music is closed is most of the win.
type darwinPlayer struct {
	uid int
	// stdout and stderr are reused across polls so a long-lived daemon does not
	// allocate a fresh pair of buffers every few seconds.
	stdout bytes.Buffer
	stderr bytes.Buffer
}

// New returns a Player for this platform.
func New() (Player, error) {
	return &darwinPlayer{uid: os.Getuid()}, nil
}

func (p *darwinPlayer) Close() error { return nil }

func (p *darwinPlayer) Poll(ctx context.Context) (Snapshot, error) {
	if !p.musicRunning() {
		return Snapshot{}, nil
	}

	out, err := p.runScript(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	switch out {
	case "X":
		// Music quit between the sysctl and the script.
		return Snapshot{}, nil
	case "S":
		return Snapshot{Running: true, Track: Track{State: StateStopped}}, nil
	}

	track, err := parseTrack(out)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Running: true, Track: track}, nil
}

// musicRunning reports whether Apple Music is up, by scanning the process table
// for the current user. On any sysctl failure it answers true so that the
// AppleScript gets a chance to decide — failing open keeps a kernel quirk from
// making the daemon permanently blind.
func (p *darwinPlayer) musicRunning() bool {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.uid", p.uid)
	if err != nil {
		return true
	}
	for i := range procs {
		if comm(&procs[i].Proc.P_comm) == musicProcess {
			return true
		}
	}
	return false
}

// comm turns the kernel's NUL-padded command name into a string.
func comm(raw *[17]byte) string {
	if i := bytes.IndexByte(raw[:], 0); i >= 0 {
		return string(raw[:i])
	}
	return string(raw[:])
}

// runScript executes the AppleScript under a hard deadline. The original daemon
// used exec.Command with no timeout at all, so a pending automation-permission
// prompt — or a Music process stuck mid-Apple-Event — froze the whole loop
// indefinitely.
func (p *darwinPlayer) runScript(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	p.stdout.Reset()
	p.stderr.Reset()

	cmd := exec.CommandContext(ctx, "osascript", "-e", nowPlayingScript)
	cmd.Stdout = &p.stdout
	cmd.Stderr = &p.stderr
	// If osascript ignores the kill, stop waiting on its pipes anyway.
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(p.stderr.String())
		if ctx.Err() != nil {
			return "", fmt.Errorf("osascript timed out: %w", ctx.Err())
		}
		if msg != "" {
			return "", fmt.Errorf("osascript: %s", msg)
		}
		return "", fmt.Errorf("osascript: %w", err)
	}
	return strings.TrimSpace(p.stdout.String()), nil
}

// parseTrack reads the record produced by nowPlayingScript.
func parseTrack(record string) (Track, error) {
	f := strings.Split(record, fieldSep)
	if len(f) != 6 {
		return Track{}, fmt.Errorf("unexpected AppleScript record with %d fields", len(f))
	}

	posMs, err := strconv.ParseInt(f[4], 10, 64)
	if err != nil {
		return Track{}, fmt.Errorf("parsing position %q: %w", f[4], err)
	}
	durMs, err := strconv.ParseInt(f[5], 10, 64)
	if err != nil {
		return Track{}, fmt.Errorf("parsing duration %q: %w", f[5], err)
	}

	state := StatePaused
	if f[0] == "playing" {
		state = StatePlaying
	}

	return Track{
		Title:    f[1],
		Artist:   f[2],
		Album:    f[3],
		Position: time.Duration(posMs) * time.Millisecond,
		Duration: time.Duration(durMs) * time.Millisecond,
		State:    state,
	}, nil
}
