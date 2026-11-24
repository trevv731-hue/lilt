package app

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"lilt/internal/player"
	"lilt/internal/qqmusic"
)

// shareChunks builds what togetherShareStream would post for one payload.
func shareChunks(payload, ref string) []string {
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	parts := (len(encoded) + togetherChunkMax - 1) / togetherChunkMax
	out := make([]string, 0, parts)
	for i := 0; i < parts; i++ {
		end := (i + 1) * togetherChunkMax
		if end > len(encoded) {
			end = len(encoded)
		}
		out = append(out, togetherShareMarker+fmt.Sprintf(`{"r":%q,"i":%d,"n":%d,"d":%q}`, ref, i, parts, encoded[i*togetherChunkMax:end]))
	}
	return out
}

// The host's stream hint is relayed as chat, but must never show in the
// transcript, and a guest must end up with the link.
func TestTogetherStreamHint(t *testing.T) {
	a := &app{}
	player.ForgetShared(42)
	for _, msg := range shareChunks(`{"id":42,"url":"http://ws.stream.qqmusic.qq.com/M50042.mp3?vkey=k","type":"mp3","size":1234,"level":"standard"}`, "aaaa0001") {
		if !a.togetherAcceptStream(msg) {
			t.Fatal("hint was not accepted")
		}
	}
	s, ok := player.SharedStream(42)
	if !ok {
		t.Fatal("guest did not get the shared stream")
	}
	if s.URL == "" || s.Type != "mp3" || s.Size != 1234 || s.Level != "standard" {
		t.Fatalf("shared stream = %+v", s)
	}
	player.ForgetShared(42)
	if _, ok := player.SharedStream(42); ok {
		t.Fatal("forgetting did not drop the link")
	}
	if a.togetherAcceptStream("大家好") {
		t.Fatal("plain chat was treated as a hint")
	}
	if !a.togetherAcceptStream(togetherShareMarker + "not json") {
		t.Fatal("malformed hint leaked into the transcript")
	}
}

// A long link, as a lossless stream carries, arrives in pieces, possibly out
// of order.
func TestTogetherStreamHintSpansMessages(t *testing.T) {
	a := &app{}
	player.ForgetShared(77)
	long := "http://ws.stream.qqmusic.qq.com/" + strings.Repeat("F00077Q", 60) + ".flac?vkey=" + strings.Repeat("a1b2", 60)
	chunks := shareChunks(`{"id":77,"url":"`+long+`","type":"flac","size":999,"level":"lossless"}`, "bbbb0002")
	if len(chunks) < 2 {
		t.Fatalf("fixture is too short to span messages: %d", len(chunks))
	}
	for i := len(chunks) - 1; i >= 0; i-- {
		if !a.togetherAcceptStream(chunks[i]) {
			t.Fatalf("chunk %d was not accepted", i)
		}
	}
	s, ok := player.SharedStream(77)
	if !ok {
		t.Fatal("guest did not reassemble the long hint")
	}
	if s.URL != long || s.Type != "flac" {
		t.Fatalf("shared stream = %.40s... type=%q", s.URL, s.Type)
	}
	player.ForgetShared(77)
}

func TestSharedStreamIgnoresZeroTrack(t *testing.T) {
	player.ShareStream(0, qqmusic.Stream{URL: "http://x/y.mp3", Type: "mp3"})
	if _, ok := player.SharedStream(0); ok {
		t.Fatal("track 0 must not be shared")
	}
}
