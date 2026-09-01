package discord

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// recordedFrame is one frame as the fake Discord endpoint received it.
type recordedFrame struct {
	op      uint32
	payload []byte
}

// readFrameStrict reads a frame the way Discord does, and fails the test if the
// framing is wrong.
//
// This is the guard against the defect that motivated the rewrite: the old
// client wrote a four-byte header for its clear-activity frame, omitting the
// length entirely. Discord then read the first four bytes of the JSON body as
// the length — "{\"cm", or about 1.8 GB — and dropped the connection. A reader
// that insists on eight bytes and a matching body catches that immediately.
func readFrameStrict(t *testing.T, conn net.Conn) (recordedFrame, error) {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var header [8]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return recordedFrame{}, err
	}

	op := binary.LittleEndian.Uint32(header[0:4])
	length := binary.LittleEndian.Uint32(header[4:8])
	if length > 1<<20 {
		t.Fatalf("frame declared a %d byte payload; the header is malformed "+
			"(opcode %d, raw header %v)", length, op, header)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return recordedFrame{}, err
	}
	if !json.Valid(payload) {
		t.Fatalf("frame payload is not valid JSON, so the declared length is wrong: %q", payload)
	}
	return recordedFrame{op: op, payload: payload}, nil
}

func writeFrameRaw(t *testing.T, conn net.Conn, op uint32, payload []byte) {
	t.Helper()
	frame := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint32(frame[0:4], op)
	binary.LittleEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(frame); err != nil {
		t.Logf("fake endpoint could not write: %v", err)
	}
}

// serveOK answers every frame with an empty success payload, recording what it
// received. It stops when the connection closes.
func serveOK(t *testing.T, conn net.Conn, frames chan<- recordedFrame) {
	defer conn.Close()
	for {
		f, err := readFrameStrict(t, conn)
		if err != nil {
			return
		}
		select {
		case frames <- f:
		default:
		}
		writeFrameRaw(t, conn, opFrame, []byte(`{"cmd":"SET_ACTIVITY","data":{},"evt":null}`))
	}
}

// newClientWithServer wires a client to an in-process endpoint driven by handle.
func newClientWithServer(t *testing.T, handle func(net.Conn)) *Client {
	t.Helper()
	c := New("test-client-id")
	c.dial = func() (net.Conn, error) {
		client, server := net.Pipe()
		go handle(server)
		return client, nil
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestFramingIsWellFormed(t *testing.T) {
	frames := make(chan recordedFrame, 8)
	c := newClientWithServer(t, func(conn net.Conn) { serveOK(t, conn, frames) })

	if err := c.SetActivity(Activity{Type: 2, Details: "Some Song", State: "by Someone"}); err != nil {
		t.Fatalf("SetActivity: %v", err)
	}

	handshake := <-frames
	if handshake.op != opHandshake {
		t.Errorf("first frame opcode = %d, want %d (handshake)", handshake.op, opHandshake)
	}
	var hs map[string]string
	if err := json.Unmarshal(handshake.payload, &hs); err != nil {
		t.Fatalf("decoding the handshake: %v", err)
	}
	if hs["client_id"] != "test-client-id" || hs["v"] != "1" {
		t.Errorf("handshake payload = %v, want v=1 and the client id", hs)
	}

	activity := <-frames
	if activity.op != opFrame {
		t.Errorf("activity frame opcode = %d, want %d", activity.op, opFrame)
	}
	var cmd struct {
		Cmd  string `json:"cmd"`
		Args struct {
			PID      int      `json:"pid"`
			Activity Activity `json:"activity"`
		} `json:"args"`
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(activity.payload, &cmd); err != nil {
		t.Fatalf("decoding the activity: %v", err)
	}
	if cmd.Cmd != "SET_ACTIVITY" {
		t.Errorf("cmd = %q, want SET_ACTIVITY", cmd.Cmd)
	}
	if cmd.Nonce == "" {
		t.Error("the frame carries no nonce")
	}
	if cmd.Args.PID == 0 {
		t.Error("the frame carries no pid")
	}
	if cmd.Args.Activity.Details != "Some Song" {
		t.Errorf("details = %q, want %q", cmd.Args.Activity.Details, "Some Song")
	}
}

// TestClearActivityFraming is the direct regression test for the original bug.
func TestClearActivityFraming(t *testing.T) {
	frames := make(chan recordedFrame, 8)
	c := newClientWithServer(t, func(conn net.Conn) { serveOK(t, conn, frames) })

	if err := c.SetActivity(Activity{Type: 2, Details: "Some Song"}); err != nil {
		t.Fatalf("SetActivity: %v", err)
	}
	<-frames // handshake
	<-frames // activity

	if err := c.ClearActivity(); err != nil {
		t.Fatalf("ClearActivity: %v", err)
	}

	cleared := <-frames
	if cleared.op != opFrame {
		t.Errorf("clear frame opcode = %d, want %d", cleared.op, opFrame)
	}

	var cmd struct {
		Cmd  string `json:"cmd"`
		Args struct {
			Activity *Activity `json:"activity"`
		} `json:"args"`
	}
	if err := json.Unmarshal(cleared.payload, &cmd); err != nil {
		t.Fatalf("decoding the clear frame: %v", err)
	}
	if cmd.Cmd != "SET_ACTIVITY" {
		t.Errorf("cmd = %q, want SET_ACTIVITY", cmd.Cmd)
	}
	if cmd.Args.Activity != nil {
		t.Errorf("activity = %+v, want null to clear the presence", cmd.Args.Activity)
	}
}

func TestClearActivityIsIdempotent(t *testing.T) {
	frames := make(chan recordedFrame, 8)
	c := newClientWithServer(t, func(conn net.Conn) { serveOK(t, conn, frames) })

	if err := c.ClearActivity(); err != nil {
		t.Fatalf("first ClearActivity: %v", err)
	}
	<-frames // handshake

	// Nothing was published, so the connection should carry no clear frame.
	select {
	case f := <-frames:
		t.Fatalf("an unnecessary frame was sent: %s", f.payload)
	case <-time.After(200 * time.Millisecond):
	}

	if err := c.ClearActivity(); err != nil {
		t.Fatalf("second ClearActivity: %v", err)
	}
	select {
	case f := <-frames:
		t.Fatalf("a repeated clear sent a frame: %s", f.payload)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestRejectsOversizedFrame covers the other half of the memory problem: the
// payload length arrives over the wire, and the old client passed it straight
// to make([]byte, n).
func TestRejectsOversizedFrame(t *testing.T) {
	c := newClientWithServer(t, func(conn net.Conn) {
		defer conn.Close()
		if _, err := readFrameStrict(t, conn); err != nil {
			return
		}
		// Claim a gigabyte, then send nothing.
		var header [8]byte
		binary.LittleEndian.PutUint32(header[0:4], opFrame)
		binary.LittleEndian.PutUint32(header[4:8], 1<<30)
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		conn.Write(header[:])
		// Hold the connection open so the client cannot mistake this for EOF.
		time.Sleep(2 * time.Second)
	})

	err := c.SetActivity(Activity{Type: 2, Details: "Some Song"})
	if err == nil {
		t.Fatal("SetActivity accepted a frame claiming a gigabyte")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %v, want it to mention the size limit", err)
	}
	if c.Connected() {
		t.Error("the client kept a connection that sent a malformed frame")
	}
}

func TestReconnectsAfterTheEndpointHangsUp(t *testing.T) {
	frames := make(chan recordedFrame, 16)
	connections := 0

	c := New("test-client-id")
	c.dial = func() (net.Conn, error) {
		connections++
		client, server := net.Pipe()
		if connections == 1 {
			// The first endpoint accepts the handshake, then vanishes the way
			// Discord does when it restarts.
			go func() {
				defer server.Close()
				readFrameStrict(t, server)
				writeFrameRaw(t, server, opFrame, []byte(`{"evt":null}`))
			}()
		} else {
			go serveOK(t, server, frames)
		}
		return client, nil
	}
	t.Cleanup(func() { c.Close() })

	// The first publish lands on the endpoint that is about to disappear.
	_ = c.SetActivity(Activity{Type: 2, Details: "First Song"})

	// Backoff would otherwise make the retry wait a couple of seconds.
	c.nextDial = time.Time{}

	if err := c.SetActivity(Activity{Type: 2, Details: "Second Song"}); err != nil {
		t.Fatalf("SetActivity after the hang-up: %v", err)
	}
	if connections < 2 {
		t.Errorf("dialled %d times, want a reconnection", connections)
	}

	<-frames // handshake on the new connection
	activity := <-frames
	if !strings.Contains(string(activity.payload), "Second Song") {
		t.Errorf("republished payload = %s, want the second song", activity.payload)
	}
}

func TestNotConnectedWhenDiscordIsAbsent(t *testing.T) {
	c := New("test-client-id")
	c.dial = func() (net.Conn, error) { return nil, errors.New("no socket") }

	err := c.SetActivity(Activity{Type: 2, Details: "Some Song"})
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("error = %v, want it to wrap ErrNotConnected", err)
	}
	if c.Connected() {
		t.Error("Connected() is true with no endpoint")
	}

	// A second attempt must be throttled rather than dialling again.
	before := c.nextDial
	_ = c.SetActivity(Activity{Type: 2, Details: "Some Song"})
	if !c.nextDial.Equal(before) {
		t.Error("the retry window was extended by an attempt it should have skipped")
	}
}

func TestClamp(t *testing.T) {
	long := strings.Repeat("é", 400)

	tests := []struct {
		name string
		in   string
		want func(string) bool
	}{
		{"empty stays empty", "", func(s string) bool { return s == "" }},
		{"single character is dropped", "x", func(s string) bool { return s == "" }},
		{"short is untouched", "ok", func(s string) bool { return s == "ok" }},
		{"long is trimmed", long, func(s string) bool {
			return len([]rune(s)) <= fieldLimit && len(s) < len(long)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clamp(tc.in)
			if !tc.want(got) {
				t.Errorf("clamp(%d chars) = %q (%d chars), which fails the check",
					len([]rune(tc.in)), got, len([]rune(got)))
			}
			// Trimming must never split a multi-byte character.
			if !isValidUTF8(got) {
				t.Errorf("clamp produced invalid UTF-8: %q", got)
			}
		})
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
