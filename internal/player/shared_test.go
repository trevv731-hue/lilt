package player

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"lilt/internal/qqmusic"
)

// A guest waiting for the room's link must wake the moment one arrives,
// not on the next poll: that wait is what a member without the track's
// membership hears instead of silence.
func TestWaitSharedStreamWakesOnShare(t *testing.T) {
	const id = int64(987654321)
	ForgetShared(id)
	ExpectSharedStreams(true)
	t.Cleanup(func() {
		ExpectSharedStreams(false)
		ForgetShared(id)
	})

	type got struct {
		stream qqmusic.Stream
		ok     bool
	}
	done := make(chan got, 1)
	start := time.Now()
	go func() {
		s, ok := waitSharedStream(id, 5*time.Second)
		done <- got{s, ok}
	}()
	time.Sleep(40 * time.Millisecond)
	ShareStream(id, qqmusic.Stream{URL: "https://ws.stream.qqmusic.qq.com/M500987654321.mp3?vkey=k", Type: "mp3", Level: "standard"})

	select {
	case g := <-done:
		if !g.ok || g.stream.URL == "" {
			t.Fatalf("the waiter did not get the room's link: %+v", g)
		}
		if waited := time.Since(start); waited > time.Second {
			t.Fatalf("the waiter took %v to notice the link", waited)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the waiter never woke")
	}
}

// Outside a room there is nobody to ask, so a track this account cannot
// resolve fails at once rather than waiting.
func TestWaitSharedStreamWithoutRoomDoesNotWait(t *testing.T) {
	ExpectSharedStreams(false)
	start := time.Now()
	if _, ok := waitSharedStream(555111, 5*time.Second); ok {
		t.Fatal("there was no link to get")
	}
	if waited := time.Since(start); waited > 200*time.Millisecond {
		t.Fatalf("waited %v outside a room", waited)
	}
}

// Each account plays with the link it signed itself when it can: the room's
// link is for a member that cannot sign one, not a file every listener pulls
// through the same account.
func TestOpenPrefersOwnStream(t *testing.T) {
	id := int64(424242)
	file, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(file) }))
	defer local.Close()
	room := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(file) }))
	defer room.Close()

	ForgetShared(id)
	ExpectSharedStreams(true)
	t.Cleanup(func() {
		ExpectSharedStreams(false)
		ForgetShared(id)
	})
	ShareStream(id, qqmusic.Stream{URL: room.URL, Type: "mp3", Size: int64(len(file)), Level: "standard"})

	calls := 0
	orig := songURL
	songURL = func(int64, string) (*qqmusic.Stream, error) {
		calls++
		return &qqmusic.Stream{URL: local.URL, Type: "mp3", Size: int64(len(file)), Level: "standard"}, nil
	}
	t.Cleanup(func() { songURL = orig })

	p := New(func(func()) {})
	p.Cache = NewCache(t.TempDir(), 1<<30)
	track := qqmusic.Track{ID: id, Duration: 1000}
	info, s, _, err := p.open(track, "standard", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if calls != 1 {
		t.Fatalf("songURL was asked %d times", calls)
	}
	if info.URL != local.URL {
		t.Fatalf("played the room's %q instead of this account's own link", info.URL)
	}
}

// An account that cannot sign the track at all takes the room's link, which
// is what one membership in the room buys the other listener.
func TestOpenUsesRoomStreamWhenLocalCannotSign(t *testing.T) {
	id := int64(424243)
	file, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	room := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(file) }))
	defer room.Close()

	ForgetShared(id)
	ExpectSharedStreams(true)
	t.Cleanup(func() {
		ExpectSharedStreams(false)
		ForgetShared(id)
	})
	ShareStream(id, qqmusic.Stream{URL: room.URL, Type: "mp3", Size: int64(len(file)), Level: "standard"})

	orig := songURL
	songURL = func(int64, string) (*qqmusic.Stream, error) {
		return nil, qqmusic.ErrUnavailable
	}
	t.Cleanup(func() { songURL = orig })

	p := New(func(func()) {})
	p.Cache = NewCache(t.TempDir(), 1<<30)
	track := qqmusic.Track{ID: id, Duration: 1000, Fee: 1}
	info, s, _, err := p.open(track, "standard", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if info.URL != room.URL {
		t.Fatalf("played %q instead of the room's link", info.URL)
	}
}

// A trial is not the song: when the room can sign the real one, the trial
// is dropped and the room's link plays instead.
func TestOpenTakesRoomLinkOverTrial(t *testing.T) {
	id := int64(434343)
	file, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(file) }))
	defer srv.Close()

	ForgetShared(id)
	ExpectSharedStreams(true)
	oldTrial := trialWait
	trialWait = 2 * time.Second
	t.Cleanup(func() {
		ExpectSharedStreams(false)
		ForgetShared(id)
		trialWait = oldTrial
	})

	orig := songURL
	songURL = func(int64, string) (*qqmusic.Stream, error) {
		return &qqmusic.Stream{URL: srv.URL + "/trial", Size: 1024, Level: "standard", Trial: true}, nil
	}
	t.Cleanup(func() { songURL = orig })

	shared := srv.URL + "/full"
	go func() {
		time.Sleep(50 * time.Millisecond)
		playerShare(id, shared, int64(len(file)))
	}()

	p := New(func(func()) {})
	p.Cache = NewCache(t.TempDir(), 1<<30)
	track := qqmusic.Track{ID: id, Duration: 1000, Fee: 1, Size128: 1 << 20}
	info, s, _, err := p.open(track, "standard", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if info.URL != shared || info.Trial {
		t.Fatalf("played %q (trial=%v), want the room's full link", info.URL, info.Trial)
	}
}

// When nobody in the room can sign the track, a trial is not the song: the
// player says so and stays on the room's track instead of stopping thirty
// seconds in while the other listener plays on.
func TestOpenRefusesTrialWithoutRoomLink(t *testing.T) {
	id := int64(444444)
	file, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(file) }))
	defer srv.Close()

	ForgetShared(id)
	ExpectSharedStreams(true)
	oldTrial := trialWait
	trialWait = 100 * time.Millisecond
	orig := songURL
	songURL = func(int64, string) (*qqmusic.Stream, error) {
		return &qqmusic.Stream{URL: srv.URL, Size: int64(len(file)), Level: "standard", Trial: true}, nil
	}
	t.Cleanup(func() {
		ExpectSharedStreams(false)
		ForgetShared(id)
		trialWait = oldTrial
		songURL = orig
	})

	p := New(func(func()) {})
	p.Cache = NewCache(t.TempDir(), 1<<30)
	track := qqmusic.Track{ID: id, Duration: 1000, Fee: 1, Size128: 1 << 20}
	if _, s, _, err := p.open(track, "standard", false); !errors.Is(err, qqmusic.ErrUnavailable) {
		if s != nil {
			s.Close()
		}
		t.Fatalf("a trial in a room should be refused, got %v", err)
	}
}

// Outside a room there is nobody to sign the song, so the trial is what
// this account may hear, and it plays as such.
func TestOpenPlaysTrialOutsideRoom(t *testing.T) {
	id := int64(454546)
	file, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(file) }))
	defer srv.Close()

	ForgetShared(id)
	ExpectSharedStreams(false)
	orig := songURL
	songURL = func(int64, string) (*qqmusic.Stream, error) {
		return &qqmusic.Stream{URL: srv.URL, Size: int64(len(file)), Level: "standard", Trial: true}, nil
	}
	t.Cleanup(func() {
		ForgetShared(id)
		songURL = orig
	})

	p := New(func(func()) {})
	p.Cache = NewCache(t.TempDir(), 1<<30)
	track := qqmusic.Track{ID: id, Duration: 1000, Fee: 1, Size128: 1 << 20}
	info, s, _, err := p.open(track, "standard", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !info.Trial {
		t.Fatalf("played %q as the song, but it is only a trial", info.URL)
	}
	if _, _, ok := p.Cache.lookup(id, "standard"); ok {
		t.Fatal("a trial was kept in the cache as if it were the song")
	}
}

// A trial must never reach the room: everyone would hear thirty seconds.
func TestShareStreamRefusesTrial(t *testing.T) {
	const id = int64(454545)
	ForgetShared(id)
	t.Cleanup(func() { ForgetShared(id) })
	ShareStream(id, qqmusic.Stream{URL: "https://x/trial.mp3", Trial: true})
	if _, ok := SharedStream(id); ok {
		t.Fatal("a trial was shared with the room")
	}
}

// playerShare is ShareStream, spelled for the tests below.
func playerShare(id int64, url string, size int64) {
	ShareStream(id, qqmusic.Stream{URL: url, Type: "mp3", Size: size, Level: "standard"})
}
