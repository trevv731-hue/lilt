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
)

// ShareStream records the stream a member of the room resolved for a
// track, so a listener whose own account may not play it can still listen.
// One membership in the room is enough, because the signed URL plays for
// anyone holding it.
func ShareStream(id int64, s qqmusic.Stream) {
	if id == 0 || s.URL == "" {
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
}

// SharedStream looks up what the room resolved for id.
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
func ExpectSharedStreams(on bool) { expectShared.Store(on) }

// waitSharedStream waits for the room's link: a listener starts loading the
// room's new track as soon as the anchor arrives, which is usually before
// whoever can resolve it has shared it.
func waitSharedStream(id int64, limit time.Duration) (qqmusic.Stream, bool) {
	if !expectShared.Load() {
		if s, ok := SharedStream(id); ok {
			return s, true
		}
		return qqmusic.Stream{}, false
	}
	deadline := time.Now().Add(limit)
	for {
		if s, ok := SharedStream(id); ok {
			return s, true
		}
		if time.Now().After(deadline) {
			return qqmusic.Stream{}, false
		}
		time.Sleep(150 * time.Millisecond)
	}
}
