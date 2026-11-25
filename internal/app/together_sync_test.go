package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lilt/internal/player"
	"lilt/internal/qqmusic"
	"lilt/internal/together"
)

// The room's queue is the host's queue: a guest maps it back onto the list
// its player takes, starting where the room's track is.
func TestRoomQueueFollowsHostList(t *testing.T) {
	q := &together.Queue{
		Tracks: []together.Track{{ID: 1, Name: "一"}, {ID: 2, Name: "二"}, {ID: 3, Name: "三"}},
		Index:  0,
	}
	tracks, at, ok := roomQueue(q, 2)
	if !ok {
		t.Fatal("the room's track should be in its queue")
	}
	if len(tracks) != 3 || at != 1 || tracks[1].ID != 2 {
		t.Fatalf("queue = %d tracks at %d, want 3 at 1", len(tracks), at)
	}
	if _, _, ok := roomQueue(q, 99); ok {
		t.Fatal("a track outside the queue has no place in it")
	}
	if _, _, ok := roomQueue(nil, 1); ok {
		t.Fatal("no queue has no place for a track")
	}
}

// A host mirrors its queue once, not on every anchor.
func TestQueueSignatureIgnoresRepeats(t *testing.T) {
	queue := []qqmusic.Track{{ID: 1, Duration: 1000}, {ID: 2, Duration: 2000}}
	first := togetherQueueSignature(queue, 0, false)
	if again := togetherQueueSignature(queue, 0, false); again != first {
		t.Fatalf("the same queue hashed differently: %q vs %q", first, again)
	}
	if moved := togetherQueueSignature(queue, 1, false); moved == first {
		t.Fatal("a moved queue should hash differently")
	}
	if longer := togetherQueueSignature(append(queue, qqmusic.Track{ID: 3}), 0, false); longer == first {
		t.Fatal("a longer queue should hash differently")
	}
	// The room's play mode goes with the queue: QQ 音乐 keeps playMode in
	// the room, so a change of mode is a change the others must hear.
	if shuffled := togetherQueueSignature(queue, 0, true); shuffled == first {
		t.Fatal("a shuffled queue should hash differently")
	}
}

// A link another member signed is kept for the player, even when this
// account could not sign one itself: that is what lets one membership
// cover the room.
func TestTogetherGuestUsesRoomStream(t *testing.T) {
	a := togetherRoomApp()
	a.together.host = false
	a.together.clientID = "guest"
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "晴天", Duration: 269_000}})
	a.pl.SetErr(qqmusic.ErrLoginRequired.Error())
	// The view test has no audio to load; the link is what this checks.
	a.pl.LockControls = func() bool { return true }
	player.ForgetShared(1)
	t.Cleanup(func() { player.ForgetShared(1) })

	a.togetherUseStream(together.Stream{
		TrackID: 1,
		URL:     "https://ws.stream.qqmusic.qq.com/M500001.mp3?vkey=k",
		Type:    "mp3",
		Size:    4096,
		Level:   "standard",
	})

	s, ok := player.SharedStream(1)
	if !ok || s.URL == "" || s.Type != "mp3" {
		t.Fatalf("the room's link was not kept: %+v ok=%v", s, ok)
	}
	if a.pl.Err() != "" {
		t.Fatalf("the guest kept its own failure instead of trying the room's link: %q", a.pl.Err())
	}
	// The same link is not retried, so a link that fails too cannot loop.
	a.pl.SetErr("网络错误")
	a.togetherUseStream(together.Stream{TrackID: 1, URL: "https://ws.stream.qqmusic.qq.com/M500001.mp3?vkey=k"})
	if a.pl.Err() == "" {
		t.Fatal("the same link should not be tried again")
	}
}

// A hand-over makes this member the host, and the room keeps playing from
// here: the client publishes an anchor of its own.
func TestTogetherBecomingHostPublishes(t *testing.T) {
	a := togetherRoomApp()
	a.together.host = false
	a.together.clientID = "guest"
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "晴天", Duration: 269_000}})
	a.pl.LockControls = func() bool { return true }
	a.togetherEvent(together.Event{Kind: together.EventYou, You: &together.You{Client: "guest", Host: true}})
	if !a.together.host {
		t.Fatal("the member should hold the room now")
	}
	// The room says it on the page, not in a popup over the song.
	if a.together.note != "你现在是房主了" {
		t.Fatalf("note = %q", a.together.note)
	}
	if a.toast != "" {
		t.Fatalf("a hand-over should not pop up: toast = %q", a.toast)
	}
}

// Publishing is not throttled to the four-second keepalive: the room hears
// a change as it happens, which is what keeps the room and the client one
// player rather than two.
func TestTogetherPublishSoonSendsOnce(t *testing.T) {
	logger := &roomLogger{}
	srv := httptest.NewServer(http.HandlerFunc(logger.serve))
	defer srv.Close()

	a := newTestApp()
	a.postFn = func(fn func()) { fn() }
	client := together.New(together.Options{
		Relay: srv.URL, Code: "TEST", ClientID: "host", Name: "房主", HTTP: srv.Client(),
		OnEvent: func(together.Event) {},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)
	waitRoom(t, "the host connection", client.Online)

	a.together = togetherState{
		joined: true, host: true, clientID: "host", selfID: "host", status: together.StatusOnline,
		client: client, code: "TEST",
	}
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "晴天", Duration: 269_000}})
	a.pl.LockControls = func() bool { return true }

	start := time.Now()
	a.togetherPublishSoon()
	a.togetherPublishSoon()
	waitRoom(t, "the room to hear the player", func() bool {
		pb, ok := logger.lastPlayback()
		return ok && pb.Track != nil && pb.Track.ID == 1
	})
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the room took %v to hear the change", took)
	}
}

// The room's own description of a track is found by id, wherever it sits:
// playing, queued, or asked for at the desk.
func TestTogetherRoomTrackFindsEverywhere(t *testing.T) {
	a := togetherRoomApp()
	if tr, ok := a.togetherRoomTrack(1); !ok || tr.Name != "晴天" {
		t.Fatalf("the room's track was not found: %+v %v", tr, ok)
	}
	a.together.queue = &together.Queue{Tracks: []together.Track{{ID: 2, Name: "稻香"}, {ID: 3, Name: "七里香"}}}
	if tr, ok := a.togetherRoomTrack(3); !ok || tr.Name != "七里香" {
		t.Fatalf("a queued track was not found: %+v %v", tr, ok)
	}
	a.together.pending = []together.Pending{{ID: "p", Track: together.Track{ID: 4, Name: "搁浅"}}}
	if tr, ok := a.togetherRoomTrack(4); !ok || tr.Name != "搁浅" {
		t.Fatalf("a requested track was not found: %+v %v", tr, ok)
	}
	if _, ok := a.togetherRoomTrack(99); ok {
		t.Fatal("a track outside the room was found")
	}
}

// A link the room hands over is kept and its signer remembered, so the page
// can say whose membership plays and this member does not send it back.
func TestTogetherRemembersRoomStream(t *testing.T) {
	a := togetherRoomApp()
	player.ForgetShared(7)
	t.Cleanup(func() { player.ForgetShared(7) })
	if !a.togetherRememberStream(together.Stream{
		TrackID: 7,
		URL:     "https://ws.stream.qqmusic.qq.com/M5007.mp3?vkey=k",
		Type:    "mp3",
		Size:    2048,
		Level:   "standard",
		ByName:  "会员朋友",
	}) {
		t.Fatal("the room's link was refused")
	}
	s, ok := player.SharedStream(7)
	if !ok || s.Size != 2048 || s.Type != "mp3" {
		t.Fatalf("the room's link was not kept: %+v", s)
	}
	if got := a.togetherStreamSource(7); got != "会员朋友" {
		t.Fatalf("the signer was not remembered: %q", got)
	}
	if got := a.together.roomStream[7]; got != s.URL {
		t.Fatalf("the room's link was not remembered: %q", got)
	}
	if got := a.together.sharedKey; got != "7|"+s.URL {
		t.Fatalf("the link would be sent back to the room: %q", got)
	}
}

// A link too short to be the song is not kept, even if another member sent
// it: an older client could share its trial by mistake.
func TestTogetherRefusesTrialFromRoom(t *testing.T) {
	a := togetherRoomApp()
	a.together.playback.Track.Fee = 1
	player.ForgetShared(1)
	t.Cleanup(func() { player.ForgetShared(1) })
	// 晴天 is a member's song four minutes long; 300 KB is thirty seconds.
	if a.togetherRememberStream(together.Stream{TrackID: 1, URL: "https://ws.stream.qqmusic.qq.com/M5001.mp3?vkey=k", Type: "mp3", Size: 300_000, Level: "standard"}) {
		t.Fatal("a trial from the room was kept")
	}
	if _, ok := player.SharedStream(1); ok {
		t.Fatal("a trial from the room reached the player")
	}
}

// A room's track this account may not play is not pressed play on again
// every second: that would only ask QQ for the same refusal and log in
// again. The room's link reloads it when it arrives.
func TestTogetherDoesNotRetryUnplayableTrack(t *testing.T) {
	a := newTestApp()
	a.pl.LockControls = a.togetherControlsLocked
	a.together.joined, a.together.host = true, false
	a.together.client = together.New(together.Options{ClientID: "guest"})
	a.together.clientID = "guest"
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "会员歌", Duration: 200_000, Fee: 1}})
	a.pl.SetErr("这首歌需要 VIP 才能播放")
	now := time.Now()
	a.together.playback = &together.Playback{
		Track:      &together.Track{ID: 1, Name: "会员歌", Duration: 200_000, Fee: 1},
		Playing:    true,
		PositionMs: 0,
		UpdatedAt:  now.UnixMilli(),
	}
	a.togetherFollow(now)
	if a.pl.Playing() || a.pl.Loading() {
		t.Fatal("an unplayable track was pressed play on again")
	}
	// An error that another attempt could fix is retried.
	a.pl.SetErr("网络错误")
	a.togetherFollow(now)
	if !a.pl.Playing() {
		t.Fatal("a transient error should be retried")
	}
}

// The member does not have to hold the room. A host without a membership
// takes the link its guest signed exactly as a guest takes the host's.
func TestTogetherHostUsesRoomStream(t *testing.T) {
	a := togetherRoomApp()
	a.together.host = true
	a.together.clientID = "host"
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "晴天", Duration: 269_000, Fee: 1}})
	a.pl.SetErr("这首歌需要 VIP 才能播放")
	player.ForgetShared(1)
	t.Cleanup(func() { player.ForgetShared(1) })

	a.togetherUseStream(together.Stream{
		TrackID: 1,
		URL:     "https://ws.stream.qqmusic.qq.com/M500001.mp3?vkey=guest",
		Type:    "mp3",
		Size:    8 << 20,
		Level:   "exhigh",
		ByName:  "会员朋友",
	})
	if s, ok := player.SharedStream(1); !ok || s.URL == "" {
		t.Fatalf("the host did not keep the guest's link: %+v", s)
	}
	if a.pl.Err() != "" {
		t.Fatalf("the host kept its own failure instead of trying the guest's link: %q", a.pl.Err())
	}
	if got := a.togetherStreamSource(1); got != "会员朋友" {
		t.Fatalf("the host did not remember who signed it: %q", got)
	}
}
