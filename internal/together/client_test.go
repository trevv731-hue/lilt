package together

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// relay is a stand-in for the Cloudflare room: enough of the protocol to
// drive a client.
type relay struct {
	t     *testing.T
	mu    sync.Mutex
	conns []*websocket.Conn
	// reject, when non-zero, is the HTTP status of the next WebSocket dial.
	reject int
	// closeFirst drops the first connection after its welcome, as a room
	// server restarting or losing the network would.
	closeFirst bool
	// closeCode is why closeFirst drops it: 4001 when another connection
	// took the client's place, 4003 kicked, 4004 ended.
	closeCode websocket.StatusCode
	served    int
}

func newRelay(t *testing.T) (*relay, *httptest.Server) {
	r := &relay{t: t}
	srv := httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(srv.Close)
	return r, srv
}

func (r *relay) serve(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case "/room":
		_ = json.NewEncoder(w).Encode(CreatedRoom{Code: "TESTROOM", HostKey: "key"})
	case "/ws":
		if r.reject != 0 {
			w.WriteHeader(r.reject)
			return
		}
		conn, err := websocket.Accept(w, req, nil)
		if err != nil {
			r.t.Logf("accept: %v", err)
			return
		}
		r.mu.Lock()
		r.conns = append(r.conns, conn)
		r.mu.Unlock()
		r.serveConn(conn)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (r *relay) serveConn(conn *websocket.Conn) {
	ctx := context.Background()
	defer conn.CloseNow()
	r.mu.Lock()
	r.served++
	first := r.served == 1
	r.mu.Unlock()
	_ = conn.Write(ctx, websocket.MessageText, mustJSON(wire{
		T:          "welcome",
		ServerTime: time.Now().UnixMilli(),
		Code:       "TESTROOM",
		You:        &You{Client: "me", Host: true},
		Members:    []Member{{Client: "me", Name: "我", Host: true}},
	}))
	if r.closeFirst && first {
		time.Sleep(100 * time.Millisecond)
		code := r.closeCode
		if code == 0 {
			code = websocket.StatusInternalError
		}
		_ = conn.Close(code, "drop")
		return
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg wire
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.T {
		case "ping":
			_ = conn.Write(ctx, websocket.MessageText, mustJSON(wire{T: "pong", ID: msg.ID, T0: msg.T0, T1: time.Now().UnixMilli()}))
		case "chat":
			_ = conn.Write(ctx, websocket.MessageText, mustJSON(wire{T: "chat", Message: &Chat{ID: "c1", Client: "me", Name: "我", Text: msg.Text, At: time.Now().UnixMilli()}}))
		case "playback":
			pb := *msg.Playback
			pb.UpdatedAt = time.Now().UnixMilli()
			_ = conn.Write(ctx, websocket.MessageText, mustJSON(wire{T: "playback", Playback: &pb}))
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestCreateRoom(t *testing.T) {
	_, srv := newRelay(t)
	room, err := CreateRoom(context.Background(), srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if room.Code != "TESTROOM" || room.HostKey != "key" {
		t.Fatalf("unexpected room: %+v", room)
	}
}

func TestClientConnectsWelcomeChatAndPlayback(t *testing.T) {
	_, srv := newRelay(t)
	events := make(chan Event, 16)
	c := New(Options{
		Relay:    srv.URL,
		Code:     "TESTROOM",
		ClientID: "me",
		Name:     "我",
		HTTP:     srv.Client(),
		OnEvent:  func(ev Event) { events <- ev },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	if ev := awaitKind(t, events, EventWelcome); ev.Welcome == nil || !ev.Welcome.You.Host {
		t.Fatalf("unexpected welcome: %+v", ev)
	}
	if !c.Online() {
		t.Fatal("client should be online")
	}

	if err := c.Chat("你好"); err != nil {
		t.Fatal(err)
	}
	if ev := awaitKind(t, events, EventChat); ev.Chat == nil || ev.Chat.Text != "你好" {
		t.Fatalf("unexpected chat: %+v", ev)
	}

	if err := c.Publish(Playback{Track: &Track{ID: 7, Name: "歌"}, Playing: true, PositionMs: 1000}); err != nil {
		t.Fatal(err)
	}
	if ev := awaitKind(t, events, EventPlayback); ev.Playback == nil || ev.Playback.Track == nil || ev.Playback.Track.ID != 7 {
		t.Fatalf("unexpected playback: %+v", ev)
	}

	// The first ping/pong round trip should land the clock near this machine's.
	deadline := time.Now().Add(2 * time.Second)
	for c.ServerNow() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	skew := time.Now().UnixMilli() - c.ServerNow()
	if skew < -2_000 || skew > 2_000 {
		t.Fatalf("clock skew too large: %dms", skew)
	}
}

func TestClientReportsMissingRoom(t *testing.T) {
	r, srv := newRelay(t)
	r.reject = http.StatusNotFound
	events := make(chan Event, 8)
	c := New(Options{Relay: srv.URL, Code: "NOPE", ClientID: "me", Name: "我", HTTP: srv.Client(), OnEvent: func(ev Event) { events <- ev }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	ev := awaitKind(t, events, EventStatus)
	for i := 0; i < 4 && !errorsIs(ev.Err, ErrRoomNotFound); i++ {
		ev = awaitKind(t, events, EventStatus)
	}
	if !errorsIs(ev.Err, ErrRoomNotFound) {
		t.Fatalf("expected ErrRoomNotFound, got %+v", ev)
	}
}

func TestClientReconnectsAfterDrop(t *testing.T) {
	r, srv := newRelay(t)
	r.closeFirst = true
	events := make(chan Event, 32)
	c := New(Options{Relay: srv.URL, Code: "TESTROOM", ClientID: "me", Name: "我", HTTP: srv.Client(), OnEvent: func(ev Event) { events <- ev }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	awaitKind(t, events, EventWelcome)
	deadline := time.After(8 * time.Second)
	for welcomes := 1; welcomes < 2; {
		select {
		case ev := <-events:
			if ev.Kind == EventWelcome {
				welcomes++
			}
		case <-deadline:
			t.Fatal("the client did not reconnect")
		}
	}
	r.mu.Lock()
	served := r.served
	r.mu.Unlock()
	if served < 2 {
		t.Fatalf("the relay should have served a second connection, served %d", served)
	}
}

func TestClientStopsWhenAnotherConnectionReplacesIt(t *testing.T) {
	r, srv := newRelay(t)
	r.closeFirst, r.closeCode = true, 4001
	events := make(chan Event, 32)
	c := New(Options{Relay: srv.URL, Code: "TESTROOM", ClientID: "me", Name: "我", HTTP: srv.Client(), OnEvent: func(ev Event) { events <- ev }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	awaitKind(t, events, EventWelcome)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Kind == EventStatus && errorsIs(ev.Err, ErrReplaced) {
				time.Sleep(1200 * time.Millisecond)
				r.mu.Lock()
				served := r.served
				r.mu.Unlock()
				if served != 1 {
					t.Fatalf("a replaced client should stop, served %d connections", served)
				}
				return
			}
		case <-deadline:
			t.Fatal("the client did not report that another connection replaced it")
		}
	}
}

func TestWelcomeSeedsClockBeforePong(t *testing.T) {
	c := New(Options{})
	ahead := time.Now().UnixMilli() + 1234
	c.noteServerTime(ahead)
	if skew := c.ServerNow() - time.Now().UnixMilli(); skew < 1000 {
		t.Fatalf("the welcome should seed the server clock, skew %dms", skew)
	}
}

func awaitKind(t *testing.T, ch <-chan Event, kind string) Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == kind {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %s event", kind)
		}
	}
}

func errorsIs(err, target error) bool {
	return err != nil && target != nil && (err == target || strings.Contains(err.Error(), target.Error()))
}
