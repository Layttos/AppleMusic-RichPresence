package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

type DiscordClient struct {
	ClientID       string
	Conn           net.Conn
	activityLoaded bool
}

type Activity struct {
	Type       int        `json:"type"`
	Details    string     `json:"details"`
	State      string     `json:"state"`
	Assets     Assets     `json:"assets"`
	Timestamps Timestamps `json:"timestamps"`
}

type Timestamps struct {
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

type Assets struct {
	LargeImage string `json:"large_image,omitempty"`
	LargeText  string `json:"large_text,omitempty"`
	SmallImage string `json:"small_image,omitempty"`
	SmallText  string `json:"small_text,omitempty"`
}

type Payload struct {
	Cmd   string      `json:"cmd"`
	Args  interface{} `json:"args"`
	Nonce string      `json:"nonce"`
}

func NewClient(clientID string) (*DiscordClient, error) {
	pipePath := getDiscordPipe()
	if pipePath == "" {
		return nil, fmt.Errorf("could not locate a valid Discord IPC pipe")
	}

	conn, err := net.Dial("unix", pipePath)
	if err != nil {
		return nil, fmt.Errorf("error connecting to Discord IPC: %w", err)
	}

	client := &DiscordClient{
		ClientID:       clientID,
		Conn:           conn,
		activityLoaded: true,
	}

	handshake := map[string]string{
		"v":         "1",
		"client_id": clientID,
	}

	if err := client.sendPayload(0, handshake); err != nil {
		client.Close()
		return nil, fmt.Errorf("handshake send failed: %w", err)
	}

	if _, err := client.readPayload(); err != nil {
		client.Close()
		return nil, fmt.Errorf("handshake response failed: %w", err)
	}

	fmt.Println("Connected to Discord!")
	return client, nil
}

func (c *DiscordClient) SetActivity(activity Activity) error {
	fActivity := Payload{
		Cmd:   "SET_ACTIVITY",
		Nonce: fmt.Sprintf("%d", time.Now().Unix()),
		Args: map[string]interface{}{
			"pid":      os.Getpid(),
			"activity": activity,
		},
	}

	c.activityLoaded = true

	if err := c.sendPayload(1, fActivity); err != nil {
		return err
	}

	if _, err := c.readPayload(); err != nil {
		return fmt.Errorf("failed to read SET_ACTIVITY response: %w", err)
	}

	return nil
}

func (c *DiscordClient) ClearActivity() error {
	if !c.activityLoaded {
		return nil
	}
	c.activityLoaded = false
	payload := map[string]interface{}{
		"cmd": "SET_ACTIVITY",
		"args": map[string]interface{}{
			"pid":      os.Getpid(),
			"activity": nil,
		},
		"nonce": fmt.Sprintf("%d", time.Now().Unix()),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = c.Conn.Write(append([]byte{1, 0, 0, 0}, data...))
	return err
}

func (c *DiscordClient) sendPayload(opcode int, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	header := make([]byte, 8)
	binary.LittleEndian.PutUint32(header[0:4], uint32(opcode))
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(payload)))

	_, err = c.Conn.Write(append(header, payload...))
	return err
}

func (c *DiscordClient) readPayload() ([]byte, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(c.Conn, header); err != nil {
		return nil, err
	}

	length := binary.LittleEndian.Uint32(header[4:8])
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.Conn, payload); err != nil {
		return nil, err
	}

	return payload, nil
}

func getDiscordPipe() string {
	tmpDir := os.Getenv("TMPDIR")

	if tmpDir == "" {
		tmpDir = "/tmp"
	}

	for i := 0; i < 10; i++ {
		path := fmt.Sprintf("%s/discord-ipc-%d", tmpDir, i)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func (c *DiscordClient) Close() error {
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return nil
}
