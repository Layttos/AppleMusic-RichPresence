// Package player reports what Apple Music is currently playing, hiding the
// per-OS mechanics behind a single interface.
package player

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrUnsupported is returned by New on platforms we have no implementation for.
var ErrUnsupported = errors.New("player: unsupported platform")

// State is what the player is doing right now.
type State int

const (
	// StateStopped means nothing is loaded, or playback has stopped.
	StateStopped State = iota
	// StatePlaying means a track is advancing.
	StatePlaying
	// StatePaused means a track is loaded but frozen.
	StatePaused
)

func (s State) String() string {
	switch s {
	case StatePlaying:
		return "playing"
	case StatePaused:
		return "paused"
	default:
		return "stopped"
	}
}

// Track is a snapshot of a song and where playback sits inside it.
type Track struct {
	Title    string
	Artist   string
	Album    string
	Position time.Duration
	Duration time.Duration
	State    State
}

// Key identifies the song itself, ignoring playback progress. Two snapshots of
// the same song a few seconds apart share a Key, which is what lets the caller
// skip redundant artwork lookups and Discord updates.
func (t Track) Key() string {
	var b strings.Builder
	b.Grow(len(t.Title) + len(t.Artist) + len(t.Album) + 2)
	b.WriteString(t.Title)
	b.WriteByte(0x1f)
	b.WriteString(t.Artist)
	b.WriteByte(0x1f)
	b.WriteString(t.Album)
	return b.String()
}

// Snapshot is the result of one poll.
type Snapshot struct {
	// Running reports whether the Apple Music application is up at all. When it
	// is false, Track is zero.
	Running bool
	Track   Track
}

// Playing reports whether the snapshot describes an advancing track worth
// showing on Discord.
func (s Snapshot) Playing() bool {
	return s.Running && s.Track.State == StatePlaying && s.Track.Title != ""
}

// Player reads the now-playing state. Implementations are not safe for
// concurrent use; the daemon polls from a single goroutine.
type Player interface {
	// Poll returns the current state. A non-nil error means the read failed,
	// which the caller should treat as transient rather than as "nothing is
	// playing" — that case is reported as a Snapshot with Running false.
	Poll(ctx context.Context) (Snapshot, error)
	// Close releases whatever the implementation holds.
	Close() error
}
