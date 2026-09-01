// Package artwork resolves album art URLs through the public iTunes Search API.
//
// Lookups are cached, including misses. The daemon polls every few seconds but
// a song only changes every few minutes, so without a cache the same query
// would be repeated hundreds of times per track — enough to hit the API's rate
// limit and to keep a network connection busy for no reason.
package artwork

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// cacheSize bounds the cache. Entries are a few hundred bytes, so this is
	// tens of kilobytes at worst and, crucially, cannot grow without limit over
	// a multi-week uptime.
	cacheSize = 128

	// maxBody caps how much of a response we read. The API returns a few
	// kilobytes; anything larger is a malfunction we should not buffer.
	maxBody = 1 << 20

	requestTimeout = 8 * time.Second
)

// searchEndpoint is the iTunes Search API. It is a field on Resolver rather
// than a constant so tests can point at a local server.
const searchEndpoint = "https://itunes.apple.com/search"

// Resolver looks up artwork and remembers what it finds.
type Resolver struct {
	client   *http.Client
	cache    *lru
	endpoint string
}

// New returns a Resolver.
func New() *Resolver {
	return &Resolver{
		client: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				// One host, one request at a time, and idle connections are
				// dropped quickly: a background daemon should not hold sockets
				// open between songs.
				MaxIdleConns:        2,
				MaxIdleConnsPerHost: 1,
				IdleConnTimeout:     30 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   5 * time.Second,
				ExpectContinueTimeout: time.Second,
				ForceAttemptHTTP2:     true,
			},
		},
		cache:    newLRU(cacheSize),
		endpoint: searchEndpoint,
	}
}

// Lookup returns an artwork URL for the track, or "" when none is found.
//
// A miss is cached as an empty string so an obscure track is not looked up
// again on every poll. Network errors are not cached, so a lookup that failed
// because the machine was offline is retried once connectivity returns.
func (r *Resolver) Lookup(ctx context.Context, title, artist, album string) string {
	if title == "" {
		return ""
	}
	key := title + "\x1f" + artist + "\x1f" + album
	if u, ok := r.cache.get(key); ok {
		return u
	}

	// The album disambiguates covers and live versions, but it also narrows the
	// search enough to miss singles, so fall back to title and artist alone.
	queries := []string{joinTerms(title, artist, album)}
	if album != "" {
		queries = append(queries, joinTerms(title, artist))
	}

	for _, q := range queries {
		u, err := r.search(ctx, q)
		if err != nil {
			// Leave the cache untouched so this is retried later.
			return ""
		}
		if u != "" {
			r.cache.put(key, u)
			return u
		}
	}

	r.cache.put(key, "")
	return ""
}

func joinTerms(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func (r *Resolver) search(ctx context.Context, term string) (string, error) {
	endpoint := r.endpoint + "?entity=song&limit=1&term=" + url.QueryEscape(term)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Drain a little so the connection can be reused, then give up.
		io.CopyN(io.Discard, resp.Body, 4<<10)
		return "", fmt.Errorf("itunes search returned %s", resp.Status)
	}

	var payload struct {
		ResultCount int `json:"resultCount"`
		Results     []struct {
			ArtworkURL100 string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decoding itunes response: %w", err)
	}
	if payload.ResultCount == 0 || len(payload.Results) == 0 {
		return "", nil
	}
	return upgradeSize(payload.Results[0].ArtworkURL100), nil
}

// upgradeSize rewrites the API's 100px thumbnail URL to a 512px one. The "bb"
// suffix selects the padded-square crop and is kept, since dropping it changes
// how non-square covers are framed.
func upgradeSize(u string) string {
	if u == "" {
		return ""
	}
	return strings.Replace(u, "/100x100bb.", "/512x512bb.", 1)
}

// lru is a fixed-size cache keyed by track. It is not safe for concurrent use;
// the daemon looks artwork up from a single goroutine.
type lru struct {
	capacity int
	order    *list.List
	items    map[string]*list.Element
}

type entry struct {
	key string
	url string
}

func newLRU(capacity int) *lru {
	return &lru{
		capacity: capacity,
		order:    list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
}

func (c *lru) get(key string) (string, bool) {
	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	c.order.MoveToFront(el)
	return el.Value.(*entry).url, true
}

func (c *lru) put(key, u string) {
	if el, ok := c.items[key]; ok {
		el.Value.(*entry).url = u
		c.order.MoveToFront(el)
		return
	}
	if c.order.Len() >= c.capacity {
		if oldest := c.order.Back(); oldest != nil {
			c.order.Remove(oldest)
			delete(c.items, oldest.Value.(*entry).key)
		}
	}
	c.items[key] = c.order.PushFront(&entry{key: key, url: u})
}
