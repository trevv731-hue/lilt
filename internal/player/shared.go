package player

import (
	"sync"
	"sync/atomic"
	"time"

	"lilt/internal/qqmusic"
)

// sharedLife is how long a stream another listener resolved stays usable.
// QQ Music signs these URLs with a short-lived vkey, but a link that is still
// fresh resolves to the same file, and a downloaded one is cached by path.
const sharedLife = 45 * time.Minute

type sharedStream struct {
	stream qqmusic.Stream
	at     time.Time
}

var (
	sharedMu sync.Mutex
	shared   = map[int64]sharedStream{}
	// sharedWake closes as a link arrives, waking whoever is waiting for
	// one: a guest starts loading the room's track the moment the anchor
	// lands, usually before the member who can sign it has shared.
	sharedWake = make(chan struct{})
)

// ShareStream records the stream a member of the room resolved for a
// track, so a listener whose own account may not play it can still listen.
// One membership in the room is enough, because the signed URL plays for
// anyone holding it.
func ShareStream(id int64, s qqmusic.Stream) {
	// A trial is not the song: sharing it would have the whole room listen
	// to thirty seconds and stay out of step.
	if id == 0 || s.URL == "" || s.Trial {
		return
	}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if len(shared) > 512 {
		cut := time.Now().Add(-sharedLife)
		for k, v := range shared {
			if v.at.Before(cut) {
				delete(shared, k)
			}
		}
	}
	shared[id] = sharedStream{stream: s, at: time.Now()}
	close(sharedWake)
	sharedWake = make(chan struct{})
}

// SharedStream looks up what the room resolved for id: the link a member
// whose account holds the rights signed, which plays for anyone holding it.
func SharedStream(id int64) (qqmusic.Stream, bool) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	v, ok := shared[id]
	if !ok || time.Since(v.at) > sharedLife {
		return qqmusic.Stream{}, false
	}
	return v.stream, true
}

// ForgetShared drops a track's shared stream, as when a newer one replaces it.
func ForgetShared(id int64) {
	sharedMu.Lock()
	delete(shared, id)
	sharedMu.Unlock()
}

// expectShared says a room is open, so a track the account may not play is
// worth waiting for another member to share.
var expectShared atomic.Bool

// ExpectSharedStreams tells the player a room is open: outside one, a link the
// account cannot resolve is a failure at once.
func ExpectSharedStreams(on bool) {
	expectShared.Store(on)
	// Only a room needs to tell a trial from a song, and only there is the
	// extra look at the CDN worth making: ordinary playback stays exactly
	// the requests QQ Music's own client makes.
	qqmusic.DetectTrial(on)
}

// waitSharedStream waits for the room's link. It wakes as soon as one
// arrives, and gives up at limit: a room whose members can none of them
// sign the track stays silent rather than loading forever.
func waitSharedStream(id int64, limit time.Duration) (qqmusic.Stream, bool) {
	expecting := expectShared.Load()
	deadline := time.Now().Add(limit)
	for {
		sharedMu.Lock()
		if v, ok := shared[id]; ok && time.Since(v.at) <= sharedLife {
			sharedMu.Unlock()
			return v.stream, true
		}
		if !expecting {
			sharedMu.Unlock()
			return qqmusic.Stream{}, false
		}
		wake := sharedWake
		sharedMu.Unlock()
		remain := time.Until(deadline)
		if remain <= 0 {
			return qqmusic.Stream{}, false
		}
		timer := time.NewTimer(min(remain, 250*time.Millisecond))
		select {
		case <-wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}
