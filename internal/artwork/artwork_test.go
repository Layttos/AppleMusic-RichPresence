package artwork

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUpgradeSize(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{
			"https://is1-ssl.mzstatic.com/image/thumb/Music125/v4/a7/00/d7/x.jpg/100x100bb.jpg",
			"https://is1-ssl.mzstatic.com/image/thumb/Music125/v4/a7/00/d7/x.jpg/512x512bb.jpg",
		},
		{"", ""},
		// A URL that does not carry the expected thumbnail segment is left alone
		// rather than mangled.
		{"https://example.invalid/cover.png", "https://example.invalid/cover.png"},
	}
	for _, tc := range tests {
		if got := upgradeSize(tc.in); got != tc.want {
			t.Errorf("upgradeSize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestJoinTerms(t *testing.T) {
	if got := joinTerms("Song", "", "  ", "Album"); got != "Song Album" {
		t.Errorf("joinTerms dropped the wrong parts: %q", got)
	}
}

func TestLRUEvictsAndBounds(t *testing.T) {
	c := newLRU(3)
	for i := 0; i < 10; i++ {
		c.put(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}
	if len(c.items) > 3 || c.order.Len() > 3 {
		t.Fatalf("cache holds %d entries, want at most 3", len(c.items))
	}
	if _, ok := c.get("k0"); ok {
		t.Error("the oldest entry survived eviction")
	}
	if v, ok := c.get("k9"); !ok || v != "v9" {
		t.Errorf("the newest entry is missing: %q, %v", v, ok)
	}
}

func TestLRUKeepsRecentlyUsed(t *testing.T) {
	c := newLRU(2)
	c.put("a", "1")
	c.put("b", "2")
	c.get("a")      // "a" is now the most recent
	c.put("c", "3") // evicts "b"

	if _, ok := c.get("a"); !ok {
		t.Error("a recently read entry was evicted")
	}
	if _, ok := c.get("b"); ok {
		t.Error("the least recently used entry survived")
	}
}

// TestLookupCachesHitsAndMisses is the point of the package: the daemon asks on
// every poll, and only the first ask for a given song may reach the network.
func TestLookupCachesHitsAndMisses(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		term := r.URL.Query().Get("term")
		if strings.Contains(term, "Known") {
			fmt.Fprint(w, `{"resultCount":1,"results":[{"artworkUrl100":"https://cdn.invalid/a/100x100bb.jpg"}]}`)
			return
		}
		fmt.Fprint(w, `{"resultCount":0,"results":[]}`)
	}))
	defer server.Close()

	r := New()
	r.endpoint = server.URL
	ctx := context.Background()

	got := r.Lookup(ctx, "Known", "Artist", "Album")
	if want := "https://cdn.invalid/a/512x512bb.jpg"; got != want {
		t.Fatalf("Lookup = %q, want %q", got, want)
	}
	hitCalls := calls.Load()

	for i := 0; i < 5; i++ {
		if again := r.Lookup(ctx, "Known", "Artist", "Album"); again != got {
			t.Fatalf("repeat lookup = %q, want %q", again, got)
		}
	}
	if calls.Load() != hitCalls {
		t.Errorf("a cached hit still made %d requests", calls.Load()-hitCalls)
	}

	// A miss falls back to a second query, then must also be remembered.
	if u := r.Lookup(ctx, "Obscure", "Nobody", "Nowhere"); u != "" {
		t.Errorf("Lookup of an unknown track = %q, want empty", u)
	}
	missCalls := calls.Load()
	for i := 0; i < 5; i++ {
		r.Lookup(ctx, "Obscure", "Nobody", "Nowhere")
	}
	if calls.Load() != missCalls {
		t.Errorf("a cached miss still made %d requests", calls.Load()-missCalls)
	}
}

func TestLookupDoesNotCacheNetworkFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	r := New()
	r.endpoint = server.URL

	r.Lookup(context.Background(), "Song", "Artist", "Album")
	first := calls.Load()
	r.Lookup(context.Background(), "Song", "Artist", "Album")

	if calls.Load() <= first {
		t.Error("a failed lookup was cached, so it will never be retried")
	}
}

func TestLookupIgnoresEmptyTitle(t *testing.T) {
	r := New()
	r.endpoint = "http://127.0.0.1:1" // would fail if it were ever dialled
	if got := r.Lookup(context.Background(), "", "Artist", "Album"); got != "" {
		t.Errorf("Lookup with no title = %q, want empty", got)
	}
}
