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

	"lilt/internal/qqmusic"
	"lilt/internal/together"
)

// The room says things on its own line, never in a popup over the song: QQ
// 音乐 keeps its room's state in the window, and a toast that covers the
// record is exactly what the room should not do.
func TestTogetherRoomErrorsStayOnThePage(t *testing.T) {
	a := togetherRoomApp()
	a.togetherEvent(together.Event{Kind: together.EventServerError, Code: "pending_full"})
	if a.toast != "" {
		t.Fatalf("a refused request should not pop up: toast = %q", a.toast)
	}
	if a.together.note == "" {
		t.Fatal("the room should say why on its own line")
	}
	was := a.together.note
	a.togetherEvent(together.Event{Kind: together.EventServerError, Code: "not_host"})
	if a.together.note == was {
		t.Fatal("a different refusal should replace the line")
	}
}

// Being quick is not the listener's mistake: a refusal for pacing is not
// worth a word, and the client spaces its own anchors so it cannot happen.
func TestTogetherRateLimitIsSilent(t *testing.T) {
	a := togetherRoomApp()
	a.togetherEvent(together.Event{Kind: together.EventServerError, Code: "rate_limited"})
	if a.toast != "" {
		t.Fatalf("toast = %q, want none", a.toast)
	}
	if a.together.note != "" {
		t.Fatalf("note = %q, want none: nothing here is worth telling", a.together.note)
	}
	if a.together.noteLive(time.Now()) {
		t.Fatal("no line should be live")
	}
}

// Anchors are paced under the relay's ten-a-second ceiling, so dragging the
// progress bar never earns a refusal in the first place.
func TestTogetherPublishPacing(t *testing.T) {
	st := &togetherState{}
	now := time.Now()
	if wait := st.publishWait(now); wait != 0 {
		t.Fatalf("the first anchor is not held back: %v", wait)
	}
	st.txAt = now
	if wait := st.publishWait(now.Add(10 * time.Millisecond)); wait < togetherPublishGap-10*time.Millisecond {
		t.Fatalf("a burst should wait out the gap, got %v", wait)
	}
	if wait := st.publishWait(now.Add(togetherPublishGap)); wait != 0 {
		t.Fatalf("the gap having passed, the anchor goes: %v", wait)
	}
	// Ten a second is what the relay allows; the gap stays well inside it.
	if time.Second/togetherPublishGap >= 10 {
		t.Fatalf("pacing %v still reaches ten a second", togetherPublishGap)
	}
}

// The room's line fades by itself, so the page goes back to just the room.
func TestTogetherNoteFades(t *testing.T) {
	a := togetherRoomApp()
	a.togetherNote("已点歌：「晴天」")
	if !a.together.noteLive(time.Now()) {
		t.Fatal("a fresh line is live")
	}
	a.together.noteAt = time.Now().Add(-togetherNoteLife - time.Second)
	if a.together.noteLive(time.Now()) {
		t.Fatal("an old line has faded")
	}
	if line, _ := a.togetherRoomNote(); line == "已点歌：「晴天」" {
		t.Fatal("the faded line should not be drawn")
	}
}

// Leaving a room leaves its end card: what was heard, for how long, and who
// was there, as QQ 音乐's end card does.
func TestTogetherEndCardKeepsTheEvening(t *testing.T) {
	a := togetherRoomApp()
	a.together.heardFrom = time.Now().Add(-12 * time.Minute)
	a.together.heard = []togetherHeard{{id: 1, name: "晴天"}, {id: 2, name: "稻香"}}
	a.togetherEvent(together.Event{
		Kind: together.EventStatus,
		Err:  together.ErrEnded,
	})
	if a.together.joined {
		t.Fatal("the room is over")
	}
	sum := a.together.summary
	if sum == nil {
		t.Fatal("the end card should stay after the room")
	}
	if len(sum.songs) != 2 {
		t.Fatalf("songs = %v, want the two the room played", sum.songs)
	}
	if sum.dur < 11*time.Minute {
		t.Fatalf("duration = %v, want the time it lasted", sum.dur)
	}
	if sum.ended == "" {
		t.Fatal("the card should say why it ended")
	}
	if a.toast != "" {
		t.Fatalf("the end card is a page, not a popup: toast = %q", a.toast)
	}
	// The next room starts with a fresh record.
	a.togetherConnect("ABCD2345", "")
	if a.together.summary != nil {
		t.Fatal("a new room should not inherit the last card")
	}
	a.togetherLeave(false)
}

// The room's play mode travels with the queue: a guest walks the list in the
// host's order rather than shuffling its own copy.
func TestTogetherGuestFollowsRoomPlayMode(t *testing.T) {
	a := togetherRoomApp()
	a.together.host = false
	a.together.clientID = "guest"
	a.pl.Cue([]qqmusic.Track{
		{ID: 1, Name: "一", Duration: 1000},
		{ID: 2, Name: "二", Duration: 1000},
		{ID: 3, Name: "三", Duration: 1000},
	})
	if a.pl.Shuffle() {
		t.Fatal("the player starts in order")
	}
	a.together.queue = &together.Queue{
		Tracks: []together.Track{{ID: 1, Name: "一"}, {ID: 2, Name: "二"}, {ID: 3, Name: "三"}},
		Index:  0,
		Shuffle: true,
	}
	a.togetherFollowMode()
	if !a.pl.Shuffle() || !a.together.roomShuffle {
		t.Fatal("the guest should take the room's mode")
	}
	a.together.queue.Shuffle = false
	a.togetherFollowMode()
	if a.pl.Shuffle() {
		t.Fatal("the room back in order, the guest follows")
	}
	if a.toast != "" {
		t.Fatalf("a mode change is not a popup: toast = %q", a.toast)
	}
}

// pacedRelay is the room server's own rule, kept where a test can see it: a
// member that publishes more than ten anchors a second is refused.
type pacedRelay struct {
	mu      sync.Mutex
	times   []time.Time
	refused int
}

func (r *pacedRelay) serve(w http.ResponseWriter, req *http.Request) {
	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := context.Background()
	welcome, _ := json.Marshal(map[string]any{
		"t": "welcome", "serverTime": time.Now().UnixMilli(), "code": "TEST",
		"you": map[string]any{"client": "host", "host": true},
	})
	_ = conn.Write(ctx, websocket.MessageText, welcome)
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			T string `json:"t"`
		}
		if json.Unmarshal(data, &msg) != nil || msg.T != "playback" {
			continue
		}
		now := time.Now()
		r.mu.Lock()
		burst := 0
		for _, at := range r.times {
			if now.Sub(at) <= time.Second {
				burst++
			}
		}
		r.times = append(r.times, now)
		if burst >= 10 {
			r.refused++
		}
		r.mu.Unlock()
	}
}

func (r *pacedRelay) counts() (sent, refused int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.times), r.refused
}

// A drag of the progress bar is a burst of changes; the room must never see
// more than the ten anchors a second it accepts, so it never has to refuse
// one and no listener is ever told they were too quick.
func TestTogetherDragNeverTripsTheRelay(t *testing.T) {
	relay := &pacedRelay{}
	srv := httptest.NewServer(http.HandlerFunc(relay.serve))
	defer srv.Close()

	a := newTestApp()
	a.postFn = func(fn func()) { go fn() }
	client := together.New(together.Options{
		Relay: srv.URL, Code: "TEST", ClientID: "host", Name: "房主", HTTP: srv.Client(),
		OnEvent: func(together.Event) {},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for !client.Online() {
		if time.Now().After(deadline) {
			t.Fatal("the client never joined")
		}
		time.Sleep(10 * time.Millisecond)
	}

	a.together = togetherState{
		joined:   true,
		host:     true,
		code:     "TEST",
		clientID: "host",
		selfID:   "host",
		status:   together.StatusOnline,
		client:   client,
	}
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "晴天", Duration: 269_000}})

	// Fifty changes in a little over a second is what a drag looks like.
	for i := 0; i < 50; i++ {
		a.togetherPublish(true)
		time.Sleep(25 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)

	sent, refused := relay.counts()
	if refused != 0 {
		t.Fatalf("the room refused %d anchors: the client should pace itself", refused)
	}
	if sent == 0 {
		t.Fatal("no anchor reached the room at all")
	}
	if sent > 12 {
		t.Fatalf("sent %d anchors in about a second and a half, want the paced few", sent)
	}
}
