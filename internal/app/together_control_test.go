package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/egoist/mygo/audio"

	"lilt/internal/qqmusic"
	"lilt/internal/together"
)

// roomLogger is a room server that records what a guest's controls send.
type roomLogger struct {
	mu        sync.Mutex
	playbacks []together.Playback
	requests  []string
}

func (r *roomLogger) serve(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/ws" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := context.Background()
	welcome, _ := json.Marshal(map[string]any{
		"t": "welcome", "serverTime": time.Now().UnixMilli(), "code": "TEST",
		"you": map[string]any{"client": "guest", "host": false},
	})
	_ = conn.Write(ctx, websocket.MessageText, welcome)
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			T        string             `json:"t"`
			Playback *together.Playback `json:"playback"`
			Text     string             `json:"text"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		r.mu.Lock()
		switch msg.T {
		case "playback":
			if msg.Playback != nil {
				r.playbacks = append(r.playbacks, *msg.Playback)
			}
		case "request":
			r.requests = append(r.requests, msg.Text)
		}
		r.mu.Unlock()
	}
}

func (r *roomLogger) lastPlayback() (together.Playback, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.playbacks) == 0 {
		return together.Playback{}, false
	}
	return r.playbacks[len(r.playbacks)-1], true
}

func (r *roomLogger) requestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func waitRoom(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTogetherGuestControlsReachRoom(t *testing.T) {
	logger := &roomLogger{}
	srv := httptest.NewServer(http.HandlerFunc(logger.serve))
	defer srv.Close()

	defer func(f func(time.Duration, func())) { sessionAfter = f }(sessionAfter)
	sessionAfter = func(time.Duration, func()) {}

	a := newTestApp()
	client := together.New(together.Options{
		Relay: srv.URL, Code: "TEST", ClientID: "guest", Name: "访客", HTTP: srv.Client(),
		OnEvent: func(together.Event) {},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)
	waitRoom(t, "the guest connection", client.Online)

	a.together = togetherState{
		joined:   true,
		host:     false,
		clientID: "guest",
		selfID:   "guest",
		status:   together.StatusOnline,
		client:   client,
		playback: &together.Playback{Track: &together.Track{ID: 1, Name: "one", Duration: 200_000}, Playing: true, PositionMs: 0, UpdatedAt: time.Now().UnixMilli()},
	}
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "one", Duration: 200_000}})
	out := audio.NewPlayer(&silence{})
	defer out.Close()
	out.Seek(0)
	a.pl.Attach(out)
	a.pl.Changed = a.playerChanged
	a.pl.LockControls = a.togetherControlsLocked
	a.pl.OnPlayPause = a.togetherPlayPause
	a.pl.OnSeek = a.togetherSeek
	a.pl.RequestNext = a.togetherNextRequest

	a.pl.TogglePlay()
	waitRoom(t, "the guest's pause", func() bool {
		pb, ok := logger.lastPlayback()
		return ok && !pb.Playing
	})

	a.pl.Seek(10 * time.Second)
	waitRoom(t, "the guest's seek", func() bool {
		pb, ok := logger.lastPlayback()
		return ok && pb.PositionMs >= 9_500
	})

	a.pl.Next()
	waitRoom(t, "the guest's next request", func() bool { return logger.requestCount() > 0 })
	logger.mu.Lock()
	action := logger.requests[0]
	logger.mu.Unlock()
	if action != "next" {
		t.Fatalf("the room heard request %q, want next", action)
	}
	if a.pl.Current() == nil || a.pl.Current().ID != 1 {
		t.Fatalf("the guest's player should not advance on a request, now %+v", a.pl.Current())
	}
}
