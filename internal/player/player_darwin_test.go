package player

import (
	"context"
	"testing"
	"time"
)

func TestParseTrack(t *testing.T) {
	sep := fieldSep
	tests := []struct {
		name    string
		record  string
		want    Track
		wantErr bool
	}{
		{
			name:   "playing",
			record: "playing" + sep + "Bohemian Rhapsody" + sep + "Queen" + sep + "A Night at the Opera" + sep + "65000" + sep + "354000",
			want: Track{
				Title: "Bohemian Rhapsody", Artist: "Queen", Album: "A Night at the Opera",
				Position: 65 * time.Second, Duration: 354 * time.Second, State: StatePlaying,
			},
		},
		{
			name:   "paused",
			record: "paused" + sep + "T" + sep + "A" + sep + "Al" + sep + "0" + sep + "1000",
			want: Track{
				Title: "T", Artist: "A", Album: "Al",
				Position: 0, Duration: time.Second, State: StatePaused,
			},
		},
		{
			// The old delimiter was "|", so any title containing one shifted every
			// later field along and crashed the index-based parse.
			name:   "pipe in metadata is not a delimiter",
			record: "playing" + sep + "Rock|Roll" + sep + "A|B" + sep + "C|D" + sep + "1500" + sep + "2500",
			want: Track{
				Title: "Rock|Roll", Artist: "A|B", Album: "C|D",
				Position: 1500 * time.Millisecond, Duration: 2500 * time.Millisecond, State: StatePlaying,
			},
		},
		{name: "too few fields", record: "playing" + sep + "T", wantErr: true},
		{name: "bad position", record: "playing" + sep + "T" + sep + "A" + sep + "Al" + sep + "x" + sep + "1", wantErr: true},
		{name: "empty", record: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTrack(tc.record)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseTrack(%q) = %+v, want error", tc.record, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTrack(%q): unexpected error: %v", tc.record, err)
			}
			if got != tc.want {
				t.Errorf("parseTrack(%q)\n got %+v\nwant %+v", tc.record, got, tc.want)
			}
		})
	}
}

func TestCommTrimsPadding(t *testing.T) {
	var raw [17]byte
	copy(raw[:], "Music")
	if got := comm(&raw); got != "Music" {
		t.Errorf("comm() = %q, want %q", got, "Music")
	}

	var full [17]byte
	for i := range full {
		full[i] = 'x'
	}
	if got := comm(&full); len(got) != 17 {
		t.Errorf("comm() on an unterminated name = %q (len %d), want len 17", got, len(got))
	}
}

// TestPollAgainstLiveSystem exercises the real sysctl and osascript path. It
// asserts only on internal consistency, since whether Music is open depends on
// the machine running the test.
func TestPollAgainstLiveSystem(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	start := time.Now()
	snap, err := p.Poll(ctx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Poll(): %v", err)
	}

	t.Logf("running=%v state=%v title=%q artist=%q pos=%v dur=%v (poll took %v)",
		snap.Running, snap.Track.State, snap.Track.Title, snap.Track.Artist,
		snap.Track.Position, snap.Track.Duration, elapsed.Round(time.Millisecond))

	if !snap.Running && snap.Track != (Track{}) {
		t.Errorf("Music reported not running but the track is non-zero: %+v", snap.Track)
	}
	if elapsed > 10*time.Second {
		t.Errorf("Poll took %v; the deadline should have capped it", elapsed)
	}
}
