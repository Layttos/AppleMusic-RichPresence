// Package ui puts the daemon in the macOS menu bar and the Windows
// notification area, so that a background process has somewhere to show what
// it is doing and somewhere to be turned off.
//
// Building with the `notray` tag drops the dependency entirely and leaves a
// headless daemon.
package ui

//go:generate go run ../../tools/genicons ../ui

// Status is what the menu displays. It is a plain value so the daemon can hand
// one over without sharing any state.
type Status struct {
	// Track and Artist are empty unless something is loaded.
	Track  string
	Artist string
	// Playing distinguishes an advancing track from a paused one.
	Playing bool
	// MusicOpen reports whether Apple Music itself is running.
	MusicOpen bool
	// Connected reports whether Discord is reachable.
	Connected bool
	// Paused reports whether the user suspended publishing from the menu.
	Paused bool
}

// summary renders the headline shown in the menu, in priority order: the
// reasons nothing is published come before what is playing.
func (s Status) summary() string {
	switch {
	case s.Paused:
		return "Presence suspended"
	case !s.Connected:
		return "Waiting for Discord…"
	case !s.MusicOpen:
		return "Apple Music is not running"
	case s.Track == "":
		return "Nothing playing"
	case !s.Playing:
		return "Paused — " + s.Track
	default:
		return s.Track
	}
}

// detail renders the second line, empty when there is nothing to add.
func (s Status) detail() string {
	if s.Track == "" || s.Artist == "" || s.Paused || !s.Connected || !s.MusicOpen {
		return ""
	}
	return "by " + s.Artist
}
