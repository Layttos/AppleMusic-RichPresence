package player

import (
	"testing"
	"time"
)

func TestTrackKeyIgnoresProgress(t *testing.T) {
	a := Track{Title: "T", Artist: "A", Album: "Al", Position: 1 * time.Second}
	b := Track{Title: "T", Artist: "A", Album: "Al", Position: 90 * time.Second}
	if a.Key() != b.Key() {
		t.Errorf("Key() should ignore position: %q != %q", a.Key(), b.Key())
	}

	c := Track{Title: "T", Artist: "A", Album: "Other"}
	if a.Key() == c.Key() {
		t.Errorf("Key() should distinguish albums, both gave %q", a.Key())
	}
}

func TestSnapshotPlaying(t *testing.T) {
	tests := []struct {
		name string
		snap Snapshot
		want bool
	}{
		{"closed", Snapshot{}, false},
		{"open but stopped", Snapshot{Running: true, Track: Track{State: StateStopped}}, false},
		{"paused", Snapshot{Running: true, Track: Track{Title: "T", State: StatePaused}}, false},
		{"playing", Snapshot{Running: true, Track: Track{Title: "T", State: StatePlaying}}, true},
		{"playing but untitled", Snapshot{Running: true, Track: Track{State: StatePlaying}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.snap.Playing(); got != tc.want {
				t.Errorf("Playing() = %v, want %v", got, tc.want)
			}
		})
	}
}
