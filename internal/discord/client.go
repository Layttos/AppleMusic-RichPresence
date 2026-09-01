// Package discord speaks Discord's local IPC protocol well enough to publish a
// Rich Presence activity.
//
// The connection is treated as disposable: Discord may not be running when we
// start, it may restart underneath us, and it hangs up on protocol errors. Every
// call therefore reconnects on demand rather than assuming a socket obtained at
// startup stays valid.
package discord

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
	"unicode/utf8"
)

// IPC opcodes.
const (
	opHandshake uint32 = 0
	opFrame     uint32 = 1
	opClose     uint32 = 2
	opPing      uint32 = 3
	opPong      uint32 = 4
)

const (
	// maxPayload caps how large a frame we are willing to accept. The length
	// prefix arrives from the socket, so allocating it unchecked lets a corrupt
	// or desynchronised stream ask us for gigabytes. Real frames are a few
	// hundred bytes.
	maxPayload = 64 << 10

	// ioTimeout bounds every read and write. Without it a half-open socket
	// parks the daemon forever.
	ioTimeout = 10 * time.Second

	// fieldLimit is Discord's maximum length, in characters, for details, state
	// and asset text.
	fieldLimit = 128
)

// ErrNotConnected reports that Discord is not reachable right now. It is an
// expected condition — Discord may simply not be running — and callers should
// retry later rather than give up.
var ErrNotConnected = errors.New("discord: not connected")

// Activity is what shows up on a Discord profile.
type Activity struct {
	// Type 2 is "Listening to".
	Type       int         `json:"type"`
	Details    string      `json:"details,omitempty"`
	State      string      `json:"state,omitempty"`
	Assets     *Assets     `json:"assets,omitempty"`
	Timestamps *Timestamps `json:"timestamps,omitempty"`
}

// Assets are the images shown beside an activity.
type Assets struct {
	LargeImage string `json:"large_image,omitempty"`
	LargeText  string `json:"large_text,omitempty"`
	SmallImage string `json:"small_image,omitempty"`
	SmallText  string `json:"small_text,omitempty"`
}

// Timestamps drive Discord's own progress bar, which is why a playing track
// needs no periodic updates: Discord animates between Start and End by itself.
type Timestamps struct {
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

// Client is a Rich Presence connection. It is not safe for concurrent use.
type Client struct {
	clientID string

	conn net.Conn
	pid  int

	// nextDial throttles reconnection attempts so that a missing Discord costs
	// a stat() every few seconds rather than a dial per poll.
	nextDial time.Time
	backoff  time.Duration

	// cleared tracks whether Discord currently shows nothing for us, so that
	// repeated clears while Apple Music is closed become no-ops instead of
	// traffic.
	cleared bool

	nonce uint64
	wbuf  bytes.Buffer
	rbuf  []byte

	// dial obtains a connection to Discord. It is a field rather than a direct
	// call so tests can substitute an in-process server.
	dial func() (net.Conn, error)
}

// New returns a client for the given Discord application ID. It does not
// connect; the first call that needs the socket will.
func New(clientID string) *Client {
	return &Client{
		clientID: clientID,
		pid:      os.Getpid(),
		cleared:  true,
		rbuf:     make([]byte, 0, 1024),
		dial:     dialDiscord,
	}
}

// Connected reports whether a live socket is currently held.
func (c *Client) Connected() bool { return c.conn != nil }

// Close tears down the connection.
func (c *Client) Close() error {
	err := c.drop()
	c.cleared = true
	return err
}

func (c *Client) drop() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	// Whatever Discord was showing is gone with the socket.
	c.cleared = true
	return err
}

// connect establishes the socket and performs the handshake, honouring the
// backoff window. Callers get ErrNotConnected while Discord is unavailable.
func (c *Client) connect() error {
	if c.conn != nil {
		return nil
	}
	if time.Now().Before(c.nextDial) {
		return ErrNotConnected
	}

	conn, err := c.dial()
	if err != nil {
		c.scheduleRetry()
		return fmt.Errorf("%w: %v", ErrNotConnected, err)
	}
	c.conn = conn

	if err := c.handshake(); err != nil {
		c.drop()
		c.scheduleRetry()
		return fmt.Errorf("%w: %v", ErrNotConnected, err)
	}

	c.backoff = 0
	c.nextDial = time.Time{}
	c.cleared = true
	return nil
}

// scheduleRetry backs off exponentially from 2s to 60s.
func (c *Client) scheduleRetry() {
	if c.backoff == 0 {
		c.backoff = 2 * time.Second
	} else if c.backoff < 60*time.Second {
		c.backoff *= 2
		if c.backoff > 60*time.Second {
			c.backoff = 60 * time.Second
		}
	}
	c.nextDial = time.Now().Add(c.backoff)
}

func (c *Client) handshake() error {
	req := map[string]string{"v": "1", "client_id": c.clientID}
	if err := c.writeFrame(opHandshake, req); err != nil {
		return fmt.Errorf("sending handshake: %w", err)
	}

	_, payload, err := c.readFrame()
	if err != nil {
		return fmt.Errorf("reading handshake reply: %w", err)
	}

	var reply struct {
		Evt  string `json:"evt"`
		Code int    `json:"code"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &reply); err != nil {
		return fmt.Errorf("decoding handshake reply: %w", err)
	}
	if reply.Evt == "ERROR" {
		return fmt.Errorf("discord refused the handshake: %s (code %d)", reply.Data.Message, reply.Code)
	}
	return nil
}

// SetActivity publishes an activity, connecting first if needed.
func (c *Client) SetActivity(a Activity) error {
	if err := c.connect(); err != nil {
		return err
	}

	a.Details = clamp(a.Details)
	a.State = clamp(a.State)
	if a.Assets != nil {
		a.Assets.LargeText = clamp(a.Assets.LargeText)
		a.Assets.SmallText = clamp(a.Assets.SmallText)
	}

	if err := c.command(map[string]any{"pid": c.pid, "activity": a}); err != nil {
		return err
	}
	c.cleared = false
	return nil
}

// ClearActivity removes whatever we published.
//
// Connecting comes first even when there is nothing to clear: it is what keeps
// a session alive while Apple Music is closed, so that the presence appears
// immediately when playback starts rather than after a connection round trip.
// The send itself is then skipped when Discord already shows nothing for us,
// which is the common case — a freshly opened connection displays nothing, and
// neither does one we cleared a moment ago.
func (c *Client) ClearActivity() error {
	if err := c.connect(); err != nil {
		return err
	}
	if c.cleared {
		return nil
	}
	// A nil activity is how the protocol expresses "show nothing".
	if err := c.command(map[string]any{"pid": c.pid, "activity": nil}); err != nil {
		return err
	}
	c.cleared = true
	return nil
}

// command sends a SET_ACTIVITY frame and consumes Discord's reply. Any I/O
// failure drops the socket so the next call reconnects rather than writing into
// a dead file descriptor forever.
func (c *Client) command(args map[string]any) error {
	c.nonce++
	req := map[string]any{
		"cmd":   "SET_ACTIVITY",
		"args":  args,
		"nonce": fmt.Sprintf("%d-%d", c.pid, c.nonce),
	}

	if err := c.writeFrame(opFrame, req); err != nil {
		c.drop()
		return fmt.Errorf("sending activity: %w", err)
	}

	_, payload, err := c.readFrame()
	if err != nil {
		c.drop()
		return fmt.Errorf("reading activity reply: %w", err)
	}

	var reply struct {
		Evt  string `json:"evt"`
		Data struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &reply); err != nil {
		return fmt.Errorf("decoding activity reply: %w", err)
	}
	if reply.Evt == "ERROR" {
		// A rejected payload is our fault, not the socket's: keep the
		// connection and report the problem.
		return fmt.Errorf("discord rejected the activity: %s (code %d)", reply.Data.Message, reply.Data.Code)
	}
	return nil
}

// writeFrame emits one frame: a little-endian opcode and payload length,
// followed by the JSON body.
//
// The header is eight bytes. Writing only the opcode makes Discord read the
// first four bytes of the JSON as the length, which asks it for roughly 1.8 GB
// and gets the connection closed.
func (c *Client) writeFrame(op uint32, v any) error {
	if c.conn == nil {
		return ErrNotConnected
	}

	c.wbuf.Reset()
	// Reserve the header, then encode straight into the same buffer so the
	// frame leaves in a single write with no intermediate copy.
	var header [8]byte
	c.wbuf.Write(header[:])
	if err := json.NewEncoder(&c.wbuf).Encode(v); err != nil {
		return fmt.Errorf("encoding payload: %w", err)
	}

	frame := c.wbuf.Bytes()
	body := len(frame) - 8
	if body > maxPayload {
		return fmt.Errorf("payload of %d bytes exceeds the %d byte limit", body, maxPayload)
	}
	binary.LittleEndian.PutUint32(frame[0:4], op)
	binary.LittleEndian.PutUint32(frame[4:8], uint32(body))

	if err := c.conn.SetWriteDeadline(time.Now().Add(ioTimeout)); err != nil {
		return err
	}
	_, err := c.conn.Write(frame)
	return err
}

// readFrame reads one frame, answering pings transparently.
func (c *Client) readFrame() (uint32, []byte, error) {
	for {
		op, payload, err := c.readFrameOnce()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case opPing:
			if err := c.writeRaw(opPong, payload); err != nil {
				return 0, nil, err
			}
		case opClose:
			return op, payload, fmt.Errorf("discord closed the connection: %s", payload)
		default:
			return op, payload, nil
		}
	}
}

func (c *Client) readFrameOnce() (uint32, []byte, error) {
	if c.conn == nil {
		return 0, nil, ErrNotConnected
	}
	if err := c.conn.SetReadDeadline(time.Now().Add(ioTimeout)); err != nil {
		return 0, nil, err
	}

	var header [8]byte
	if _, err := io.ReadFull(c.conn, header[:]); err != nil {
		return 0, nil, err
	}

	op := binary.LittleEndian.Uint32(header[0:4])
	length := binary.LittleEndian.Uint32(header[4:8])
	if length > maxPayload {
		// The stream is desynchronised or hostile. Refusing here is what keeps
		// a bogus length prefix from turning into a multi-gigabyte allocation.
		return 0, nil, fmt.Errorf("frame claims %d bytes, over the %d byte limit", length, maxPayload)
	}

	// Reuse the read buffer across frames; it settles at the size of the
	// largest reply Discord sends and stops growing.
	if cap(c.rbuf) < int(length) {
		c.rbuf = make([]byte, length)
	}
	payload := c.rbuf[:length]
	if _, err := io.ReadFull(c.conn, payload); err != nil {
		return 0, nil, err
	}
	return op, payload, nil
}

// writeRaw sends an already-encoded payload, used for pong replies.
func (c *Client) writeRaw(op uint32, payload []byte) error {
	if c.conn == nil {
		return ErrNotConnected
	}
	frame := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint32(frame[0:4], op)
	binary.LittleEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)

	if err := c.conn.SetWriteDeadline(time.Now().Add(ioTimeout)); err != nil {
		return err
	}
	_, err := c.conn.Write(frame)
	return err
}

// clamp trims a field to Discord's limit without splitting a rune, and drops
// single-character values, which Discord rejects outright.
func clamp(s string) string {
	if utf8.RuneCountInString(s) < 2 {
		return ""
	}
	if utf8.RuneCountInString(s) <= fieldLimit {
		return s
	}
	n := 0
	for i := range s {
		n++
		if n > fieldLimit-1 {
			return s[:i] + "…"
		}
	}
	return s
}
