package proxy

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/vrealzhou/agent-vm/internal/config"
)

// buildSASLAuthFrame builds a Kafka SASL_AUTHENTICATE request with PLAIN creds.
func buildSASLAuthFrame(username, password string, corrID int32) []byte {
	// Body: api_key(2) + version(2) + corr_id(4) + client_id(2=-1) + auth_len(4) + auth
	var body bytes.Buffer
	binary.Write(&body, binary.BigEndian, int16(36)) // API key: SASL_AUTHENTICATE
	binary.Write(&body, binary.BigEndian, int16(0))  // version
	binary.Write(&body, binary.BigEndian, corrID)    // correlation ID
	binary.Write(&body, binary.BigEndian, int16(-1)) // null client_id

	auth := append([]byte{0}, []byte(username)...)
	auth = append(auth, 0)
	auth = append(auth, []byte(password)...)
	binary.Write(&body, binary.BigEndian, int32(len(auth)))
	body.Write(auth)

	// Frame: size(4) + body
	frame := make([]byte, 4+body.Len())
	binary.BigEndian.PutUint32(frame[:4], uint32(body.Len()))
	copy(frame[4:], body.Bytes())
	return frame
}

// buildGenericFrame builds a generic Kafka frame with a given API key.
func buildGenericFrame(apiKey int16) []byte {
	var body bytes.Buffer
	binary.Write(&body, binary.BigEndian, apiKey)
	binary.Write(&body, binary.BigEndian, int16(0))  // version
	binary.Write(&body, binary.BigEndian, int32(42)) // corr ID
	body.Write([]byte{0xAA, 0xBB, 0xCC})             // dummy payload

	frame := make([]byte, 4+body.Len())
	binary.BigEndian.PutUint32(frame[:4], uint32(body.Len()))
	copy(frame[4:], body.Bytes())
	return frame
}

func TestReadKafkaFrame(t *testing.T) {
	frame := buildSASLAuthFrame("placeholder-user", "placeholder-pass", 99)
	r := bytes.NewReader(frame)

	body, err := readKafkaFrame(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != len(frame)-4 {
		t.Errorf("body length = %d, want %d", len(body), len(frame)-4)
	}
}

func TestGetKafkaAPIKey(t *testing.T) {
	frame := buildSASLAuthFrame("u", "p", 1)
	body := frame[4:]
	if key := getKafkaAPIKey(body); key != 36 {
		t.Errorf("API key = %d, want 36", key)
	}

	generic := buildGenericFrame(10) // LIST_OFFSETS
	if key := getKafkaAPIKey(generic[4:]); key != 10 {
		t.Errorf("API key = %d, want 10", key)
	}
}

func TestRewriteSASLPlain(t *testing.T) {
	frame := buildSASLAuthFrame("placeholder", "placeholder-pass", 7)
	body := frame[4:]

	modified := rewriteSASLPlain(body, "real-user", "real-secret-pass")

	// Verify the modified body contains real credentials
	modStr := string(modified)
	if !bytes.Contains(modified, []byte("real-user")) {
		t.Error("modified body does not contain real-user")
	}
	if !bytes.Contains(modified, []byte("real-secret-pass")) {
		t.Error("modified body does not contain real-secret-pass")
	}
	// Verify placeholder is gone
	if bytes.Contains(modified, []byte("placeholder")) {
		t.Error("modified body still contains 'placeholder'")
	}
	_ = modStr

	// Verify the frame can be re-read correctly
	if key := getKafkaAPIKey(modified); key != 36 {
		t.Errorf("modified API key = %d, want 36", key)
	}

	// Verify correlation ID preserved
	corrID := int32(binary.BigEndian.Uint32(modified[4:8]))
	if corrID != 7 {
		t.Errorf("correlation ID = %d, want 7", corrID)
	}

	// Verify the auth data structure: \0real-user\0real-secret-pass
	// Find auth data: skip header (8 bytes) + client_id (2 bytes for -1)
	authStart := 8 + 2
	authLen := int32(binary.BigEndian.Uint32(modified[authStart : authStart+4]))
	authData := modified[authStart+4 : authStart+4+int(authLen)]

	expected := append([]byte{0}, []byte("real-user")...)
	expected = append(expected, 0)
	expected = append(expected, []byte("real-secret-pass")...)

	if !bytes.Equal(authData, expected) {
		t.Errorf("auth data mismatch:\n  got:  %x\n  want: %x", authData, expected)
	}
}

func TestRewriteSASLPlainRoundTrip(t *testing.T) {
	// Build frame, rewrite, write, read back
	original := buildSASLAuthFrame("old-user", "old-pass", 1)
	body := original[4:]

	modified := rewriteSASLPlain(body, "new-user", "new-pass")

	// Write as complete frame and read back
	var buf bytes.Buffer
	writeKafkaFrame(&buf, modified)

	readBack, err := readKafkaFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}

	// Verify credentials
	authStart := 8 + 2
	authLen := int32(binary.BigEndian.Uint32(readBack[authStart : authStart+4]))
	authData := readBack[authStart+4 : authStart+4+int(authLen)]

	if !bytes.Contains(authData, []byte("new-user")) {
		t.Error("round-trip: new-user not found")
	}
	if !bytes.Contains(authData, []byte("new-pass")) {
		t.Error("round-trip: new-pass not found")
	}
	if bytes.Contains(authData, []byte("old-user")) {
		t.Error("round-trip: old-user should be gone")
	}
}

func TestNonSASLFrameUntouched(t *testing.T) {
	// Non-SASL frames should not be modified
	generic := buildGenericFrame(10) // API key 10 = LIST_OFFSETS
	body := generic[4:]

	// rewriteSASLPlain should not be called on non-SASL frames,
	// but verify it doesn't crash on them
	result := rewriteSASLPlain(body, "user", "pass")
	// The function still works (rewrites auth section if present),
	// but in practice pipeKafkaClientToBroker only calls it for API key 19
	if getKafkaAPIKey(result) != 10 {
		t.Error("API key should be preserved")
	}
}

func TestKafkaAPIKeys(t *testing.T) {
	// Verify known Kafka API keys
	tests := []struct {
		name string
		key  int16
	}{
		{"SASL_HANDSHAKE", 17},
		{"API_VERSIONS", 18},
		{"SASL_AUTHENTICATE", 36},
	}
	for _, tt := range tests {
		frame := buildGenericFrame(tt.key)
		got := getKafkaAPIKey(frame[4:])
		if got != tt.key {
			t.Errorf("%s: got key %d, want %d", tt.name, got, tt.key)
		}
	}
}

func TestKafkaProxyConfig(t *testing.T) {
	// Test LoadKafkaProxyConfig returns nil when no config
	os.Remove(config.ProxyConfigPath())
	if cfg := LoadKafkaProxyConfig(); cfg != nil {
		t.Error("expected nil when no proxy.json")
	}
}
