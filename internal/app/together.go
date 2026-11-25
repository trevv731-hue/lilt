package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"lilt/internal/player"
	"lilt/internal/qqmusic"
	"lilt/internal/together"
)

// togetherShareMarker prefixes the host's stream hints in room chat. QQ Music
// signs a stream link for the account that asked for it, but the link itself
// plays for anyone holding it, so the host resolves once and guests reuse it:
// only the host needs a membership. Relays that know the room protocol carry
// the link as a message of its own; this marker is what older ones get.
const togetherShareMarker = "\u2063lilt-stream "

// The room's own pacing. The relay drops a member that talks more than ten
// times a second, and refuses a second offer of the same song inside two
// seconds; both are worth staying well inside, so the room never has to
// refuse anything and no listener ever sees a warning for being quick.
const (
	togetherPublishGap = 150 * time.Millisecond
	togetherShareGap   = 2100 * time.Millisecond
	togetherNoteLife   = 6 * time.Second
)

// togetherSharedStream is the payload behind togetherShareMarker.
type togetherSharedStream struct {
	ID    int64  `json:"id"`
	URL   string `json:"url"`
	Type  string `json:"type"`
	Size  int64  `json:"size"`
	Level string `json:"level"`
}

// togetherState is the room this app is in, and how its page looks. The
// client's goroutines reach it only through post, like every other piece of
// app state.
type togetherState struct {
	client *together.Client
	cancel context.CancelFunc
	gen    int

	joined   bool
	host     bool
	code     string
	hostKey  string
	clientID string
	// note is what the room says about itself: a line on the page, never a
	// popup over it. QQ 音乐 keeps its room's state in the window
	// (LTDescription) instead of interrupting the song, and a toast over a
	// record that is playing only gets in the way.
	note     string
	noteAt   time.Time
	noteWarn bool
	// noteWake is the fade already scheduled, so a page that repaints for
	// another reason does not queue a second timer for the same line.
	noteWake time.Time
	// selfID is how the room names this member in its lists and messages: a
	// public handle, not the client id this app reconnects with, so nobody
	// reading the room can reconnect as this member.
	selfID   string
	status   together.Status
	err      string
	busy     bool
	applying bool // the app is following the room, not the listener
	// userActed says the listener changed play or position, which a guest
	// then publishes to the room.
	userActed bool

	members  []together.Member
	playback *together.Playback
	pending  []together.Pending
	chat     []together.Chat
	// queue is the host's queue, as the host mirrored it, so a guest plays
	// the same list rather than a single borrowed track.
	queue *together.Queue
	// features are what the relay understands beyond the first protocol;
	// an older one sends none, and the room falls back to what it did
	// before.
	features map[string]bool

	// selfName and selfAvatar are what the room was last told this member
	// is, so a restored QQ Music account reaches the other side without a
	// reconnect.
	selfName, selfAvatar string
	// streamAsked is the track this member last asked the room to share a
	// stream for, and when: a link that keeps failing is not asked for
	// every second.
	streamAsked   int64
	streamAskedAt time.Time
	// streamFrom is whose membership signed the room's link for a track,
	// so the page can say it, as QQ 音乐 marks a song the room shares.
	streamFrom map[int64]string
	// roomStream is the link the room holds for a track, so two members
	// with links of their own do not trade theirs back and forth every
	// second: the first good one stays until someone asks for another.
	roomStream map[int64]string
	// verified is the last link of a track checked against the CDN, so a
	// link that comes around again is not probed again.
	verified map[int64]string
	// signTried is when this account last asked QQ to sign a track for the
	// room, so a track nobody here can sign is not asked for every second.
	signTried map[int64]time.Time
	// signFailed is a track this account already failed to sign: asking
	// again would only put the same refusal in QQ Music's logs.
	signFailed map[int64]time.Time
	// sharedURL is the room's link this client last tried, so a link that
	// fails too is not retried on every arrival.
	sharedURL string
	// queueSig is the host's queue as last mirrored, so an unchanged queue
	// is not sent again.
	queueSig string
	// publishTimer coalesces the burst of player changes one action makes.
	publishTimer *time.Timer
	// txAt is when this member last spoke to the room about its player. A
	// drag of the progress bar makes a burst of changes, and the relay
	// drops a member that shouts more than ten times a second: pacing here
	// is what keeps the room from ever answering with a refusal.
	txAt time.Time
	// shareAt is when a link was last offered for a track, so the same
	// song is not re-offered inside the relay's two-second window.
	shareAt map[int64]time.Time
	// heard is every song the room played while this member listened, in
	// the order it played them, and heardFrom is when: the end card counts
	// them, as QQ 音乐's does.
	heard     []togetherHeard
	heardFrom time.Time
	// summary is the room that just ended, kept on the page as QQ 音乐
	// keeps its end card.
	summary *togetherSummary
	// togetherShuffled is the room's play mode, mirrored from the host's
	// queue as QQ 音乐 mirrors playMode.
	roomShuffle bool

	joinCode  string
	chatDraft string
	seenErr   string
	// entryNote is what the way into a room has to say: a saved relay, a
	// room just left. entryRetry remembers whether retrying means opening
	// a room or joining one.
	entryNote  string
	entryRetry string

	// The song request desk's search.
	requestQuery   string
	requestResults []qqmusic.Track
	requestLoading bool
	requestErr     string
	requestGen     int

	chatList    ui.ListState
	pendingList ui.ListState
	queueList   ui.ListState
	// deskTab is the desk's tab: the room's requests, or the host's queue.
	deskTab     int
	lastSeek    time.Time
	lastTrack   int64
	sharedKey   string
	sharedParts map[string]*togetherShareBuffer
	lastPublish time.Time
	lastPlaying bool
	lastPos     time.Duration
	lastPosAt   time.Time
}

// togetherSummary is the room that just ended, kept as QQ 音乐 keeps its
// end card (endCard/endToast in the room's state): the songs the room heard,
// how long it lasted, and who was in it. It stays on the page until the next
// room, so leaving is not the same as losing what was played.
type togetherSummary struct {
	code    string
	name    string
	at      time.Time
	ended   string
	songs   []string
	people  []string
	dur     time.Duration
	wasHost bool
}

// togetherNote puts a line in the room instead of a popup over it: what the
// room wants to say is part of the page, as QQ 音乐's room description is.
func (a *app) togetherNote(text string) {
	st := &a.together
	st.note, st.noteAt, st.noteWarn = text, time.Now(), false
	a.redraw()
}

// togetherWarn is the same line, marked as something that did not work.
func (a *app) togetherWarn(text string) {
	st := &a.together
	st.note, st.noteAt, st.noteWarn = text, time.Now(), true
	a.redraw()
}

// togetherNoteWake repaints once, when the line is due to fade, and only
// once for each line.
func (a *app) togetherNoteWake(deadline time.Time) {
	st := &a.together
	if st.noteWake.Equal(deadline) {
		return
	}
	st.noteWake = deadline
	a.redrawLater(deadline)
}

// noteLive is whether the room still wants its line shown: it fades by
// itself, so the page returns to just the room.
func (st *togetherState) noteLive(now time.Time) bool {
	return st.note != "" && now.Sub(st.noteAt) < togetherNoteLife
}

func (a *app) togetherRelay() string {
	if relay := strings.TrimSpace(a.settings.TogetherRelay); relay != "" {
		return relay
	}
	return together.DefaultRelay
}

func newTogetherClientID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// togetherIdentity is the name and portrait shown in a room: the signed-in
// QQ Music account's, or a local nickname when there is none. A listener
// who never signed in still gets a name of their own: two rooms full of
// "我" look like one person talking to themselves.
func (a *app) togetherIdentity() (name, avatar string) {
	if a.profile != nil {
		name = strings.TrimSpace(a.profile.Nickname)
		avatar = a.profile.AvatarURL
		if name != "" {
			return name, avatar
		}
	}
	return a.togetherLocalName(), avatar
}

// togetherLocalName is the name of a listener without a QQ Music account:
// the nickname they chose, or a short tag of this install.
func (a *app) togetherLocalName() string {
	if name := strings.TrimSpace(a.settings.TogetherName); name != "" {
		return name
	}
	id := a.togetherClientID()
	if len(id) > 4 {
		id = id[:4]
	}
	return "听众" + strings.ToUpper(id)
}

// togetherClientID identifies this install in a room, and keeps a listener
// without an account distinguishable from the next one.
func (a *app) togetherClientID() string {
	if a.settings.TogetherClient == "" {
		a.settings.TogetherClient = newTogetherClientID()
		a.settings.save()
	}
	return a.settings.TogetherClient
}

func (a *app) togetherNickname() string {
	name, _ := a.togetherIdentity()
	return name
}

func (a *app) togetherConnect(code, hostKey string) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		a.together.err = "请输入房间号"
		a.redraw()
		return
	}
	if err := together.ValidateRelay(a.togetherRelay()); err != nil {
		a.together.err = err.Error()
		a.redraw()
		return
	}
	a.togetherLeave(false)
	st := &a.together
	st.gen++
	gen := st.gen
	st.chatList = ui.ListState{FollowEnd: true}

	id := a.togetherClientID()
	st.joined, st.host = true, hostKey != ""
	st.code, st.hostKey, st.clientID = code, hostKey, id
	st.selfID = ""
	st.status, st.err = together.StatusConnecting, ""
	st.members, st.playback, st.pending, st.chat = nil, nil, nil, nil
	st.queue = nil
	st.seenErr, st.applying = "", false
	st.features = nil
	st.streamAsked, st.streamAskedAt = 0, time.Time{}
	st.streamFrom, st.signTried, st.signFailed, st.roomStream, st.verified = nil, nil, nil, nil, nil
	st.sharedURL, st.queueSig = "", ""
	st.requestQuery, st.requestResults, st.requestErr = "", nil, ""
	st.note, st.noteAt, st.noteWarn, st.noteWake = "", time.Time{}, false, time.Time{}
	st.summary = nil
	st.heard, st.heardFrom = nil, time.Time{}
	st.txAt, st.shareAt = time.Time{}, nil
	st.roomShuffle = false

	ctx, cancel := context.WithCancel(context.Background())
	st.cancel = cancel
	name, avatar := a.togetherIdentity()
	st.selfName, st.selfAvatar = name, avatar
	client := together.New(together.Options{
		Relay:    a.togetherRelay(),
		Code:     code,
		HostKey:  hostKey,
		ClientID: id,
		Name:     name,
		Avatar:   avatar,
		OnEvent: func(ev together.Event) {
			a.post(func() {
				if a.together.gen != gen {
					return
				}
				a.togetherEvent(ev)
			})
		},
	})
	st.client = client
	a.settings.save()
	go client.Run(ctx)
	go a.togetherTicker(ctx, gen)
	a.nowPlaying = false
	a.redraw()
}

func (a *app) togetherJoin() {
	code := roomCodeFrom(a.together.joinCode)
	if code == "" {
		a.together.err = "房间号是 8 位字母或数字"
		a.together.entryRetry = "join"
		a.redraw()
		return
	}
	a.together.joinCode = ""
	a.together.entryRetry, a.together.entryNote = "join", ""
	a.togetherConnect(code, "")
}

// roomCodeFrom finds a room code in what the listener pasted: the code
// alone, or the invitation sentence it came in.
func roomCodeFrom(text string) string {
	text = strings.ToUpper(text)
	start := -1
	for i := 0; i <= len(text); i++ {
		valid := i < len(text) && isRoomCodeByte(text[i])
		switch {
		case valid && start < 0:
			start = i
		case !valid && start >= 0:
			if code := text[start:i]; validRoomCode(code) {
				return code
			}
			start = -1
		}
	}
	return ""
}

func isRoomCodeByte(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= '2' && b <= '9'
}

func validRoomCode(code string) bool {
	if len(code) != 8 {
		return false
	}
	// The server's alphabet leaves out I, O, 0 and 1, which look alike.
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for _, r := range code {
		if !strings.ContainsRune(alphabet, r) {
			return false
		}
	}
	return true
}

func (a *app) togetherCreate() {
	st := &a.together
	if st.busy {
		return
	}
	if err := together.ValidateRelay(a.togetherRelay()); err != nil {
		st.err = err.Error()
		a.redraw()
		return
	}
	st.busy, st.err = true, ""
	st.entryRetry, st.entryNote = "create", ""
	relay := a.togetherRelay()
	hc := together.DefaultHTTPClient()
	go func() {
		// CreateRoom retries a flaky relay on its own; the deadline only
		// covers those attempts, so a listener is not left waiting.
		ctx, cancel := context.WithTimeout(context.Background(), 34*time.Second)
		defer cancel()
		room, err := together.CreateRoom(ctx, relay, hc)
		a.post(func() {
			st.busy = false
			if err != nil {
				st.err = "创建房间失败，请重试"
				a.redraw()
				return
			}
			a.togetherConnect(room.Code, room.HostKey)
		})
	}()
	a.redraw()
}

// togetherLeaveRoom ends a room the listener hosts for everyone, and leaves
// one they don't.
func (a *app) togetherLeaveRoom() {
	st := &a.together
	if st.host && st.client != nil {
		client, gen := st.client, st.gen
		go func() {
			_ = client.End()
			a.post(func() {
				if a.together.gen != gen {
					return
				}
				a.togetherFinish("你结束了这间房")
				a.togetherLeave(true)
			})
		}()
		return
	}
	a.togetherFinish("已离开一起听")
	a.togetherLeave(true)
}

// togetherExit ends a room the quitting app hosts, and lets go of one it
// only listens in. It runs as the app quits, so it does not wait long.
func (a *app) togetherExit() {
	st := &a.together
	if st.joined && st.host && st.client != nil && st.client.Online() {
		_ = st.client.End()
	}
	a.togetherLeave(false)
}

func (a *app) togetherLeave(announce bool) {
	player.ExpectSharedStreams(false)
	st := &a.together
	st.gen++
	if st.publishTimer != nil {
		st.publishTimer.Stop()
		st.publishTimer = nil
	}
	if st.client != nil && !st.host {
		_ = st.client.Leave()
	}
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	st.client = nil
	st.joined, st.host = false, false
	st.code, st.hostKey = "", ""
	st.selfID = ""
	st.status, st.err = together.StatusOffline, ""
	st.members, st.playback, st.pending, st.chat = nil, nil, nil, nil
	st.queue = nil
	st.features = nil
	st.applying, st.seenErr = false, ""
	st.selfName, st.selfAvatar = "", ""
	st.streamAsked, st.streamAskedAt = 0, time.Time{}
	st.streamFrom, st.signTried, st.signFailed, st.roomStream, st.verified = nil, nil, nil, nil, nil
	st.sharedURL, st.queueSig = "", ""
	st.requestQuery, st.requestResults, st.requestErr = "", nil, ""
	st.txAt, st.shareAt, st.heard, st.heardFrom = time.Time{}, nil, nil, time.Time{}
	st.roomShuffle = false
	if announce {
		a.redraw()
	}
}

// togetherFinish leaves the end card behind: what the room heard and for how
// long, the way QQ 音乐 keeps endCard and endToast so leaving a room is a
// page rather than a vanished window. The room itself is already gone by the
// time this runs, so it reads nothing but the record this member kept.
func (a *app) togetherFinish(ended string) {
	st := &a.together
	if !st.joined {
		return
	}
	sum := &togetherSummary{
		code:    st.code,
		name:    st.hostName(),
		at:      st.heardFrom,
		ended:   ended,
		dur:     0,
		wasHost: st.host,
	}
	for _, h := range st.heard {
		if h.name == "" {
			continue
		}
		sum.songs = append(sum.songs, h.name)
	}
	for _, m := range st.members {
		if m.Name != "" {
			sum.people = append(sum.people, m.Name)
		}
	}
	if !st.heardFrom.IsZero() {
		sum.dur = time.Since(st.heardFrom)
	}
	if sum.name == "" {
		sum.name = a.togetherNickname()
	}
	st.summary = sum
	st.note, st.noteAt = "", time.Time{}
}

// togetherHeard records a song the room played while this member listened,
// which is what the end card counts.
func (a *app) togetherHeard(id int64, name string) {
	st := &a.together
	if !st.joined || id == 0 || name == "" {
		return
	}
	if st.heardFrom.IsZero() {
		st.heardFrom = time.Now()
	}
	for _, h := range st.heard {
		if h.id == id && h.name == name {
			return
		}
	}
	// A hundred songs is an evening; older ones only pad the card.
	if len(st.heard) >= 100 {
		return
	}
	st.heard = append(st.heard, togetherHeard{id: id, name: name})
}

// togetherHeard is one song the room played, as the end card lists it.
type togetherHeard struct {
	id   int64
	name string
}

// togetherHeardPlayback counts the song an anchor names, when it has one.
func (a *app) togetherHeardPlayback(pb *together.Playback) {
	if pb == nil || pb.Track == nil {
		return
	}
	a.togetherHeard(pb.Track.ID, pb.Track.Name)
}

// togetherPoint says a song was asked for, on the desk rather than over the
// page: nothing about the room needs a popup.
func (a *app) togetherPoint(tr qqmusic.Track) {
	a.togetherNote("已点歌：「" + tr.Name + "」")
}

func (a *app) togetherTicker(ctx context.Context, gen int) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			a.post(func() {
				if a.together.gen != gen || !a.together.joined {
					return
				}
				a.togetherSyncIdentity()
				if a.together.host {
					a.togetherPublish(false)
				} else {
					// The queue's play mode is the room's: a guest whose
					// own shuffle button was pressed drifts back within the
					// second, without anyone being told off for pressing it.
					a.togetherFollowMode()
					a.togetherFollow(now)
				}
				// Whoever resolved the room's track shares it: a membership
				// anywhere in the room is enough for both listeners.
				a.togetherShareCurrent()
				a.togetherCheckPermission()
			})
		}
	}
}

func (a *app) togetherEvent(ev together.Event) {
	st := &a.together
	switch ev.Kind {
	case together.EventStatus:
		st.status = ev.Status
		if errors.Is(ev.Err, together.ErrLeft) {
			a.togetherLeave(false)
			return
		}
		if errors.Is(ev.Err, together.ErrEnded) {
			wasHost := st.host
			if wasHost {
				a.togetherFinish("你结束了这间房")
			} else {
				a.togetherFinish("房主结束了房间")
			}
			a.togetherLeave(false)
			return
		}
		if errors.Is(ev.Err, together.ErrKicked) || errors.Is(ev.Err, together.ErrRoomNotFound) || errors.Is(ev.Err, together.ErrRoomFull) || errors.Is(ev.Err, together.ErrReplaced) {
			message := ev.Err.Error()
			a.togetherFinish(message)
			a.togetherLeave(false)
			return
		}
		if ev.Err != nil {
			st.err = ev.Err.Error()
		}
		if ev.Status == together.StatusOnline {
			st.err = ""
		}
	case together.EventWelcome:
		if ev.Welcome == nil {
			return
		}
		if len(ev.Welcome.Chat) > 0 {
			// History may hold the host's earlier stream hints; take them
			// before the transcript shows the rest.
			kept := ev.Welcome.Chat[:0]
			for _, c := range ev.Welcome.Chat {
				if !a.togetherAcceptStream(c.Text) {
					kept = append(kept, c)
				}
			}
			ev.Welcome.Chat = kept
		}
		st.joined = true
		st.host = ev.Welcome.You.Host
		st.selfID = ev.Welcome.You.Client
		st.code = ev.Welcome.Code
		st.members = ev.Welcome.Members
		st.playback = ev.Welcome.Playback
		st.pending = ev.Welcome.Pending
		st.chat = ev.Welcome.Chat
		st.queue = ev.Welcome.Queue
		a.togetherFollowMode()
		st.features = map[string]bool{}
		for _, f := range ev.Welcome.Features {
			st.features[f] = true
		}
		st.status = together.StatusOnline
		st.err = ""
		if ev.Welcome.HeartbeatMs > 0 && st.client != nil {
			// The room knows when it sweeps a member who has gone quiet,
			// so it sets the beat, as QQ 音乐's heartBeatInterval does.
			st.client.SetHeartbeat(time.Duration(ev.Welcome.HeartbeatMs) * time.Millisecond)
		}
		a.togetherHeardPlayback(st.playback)
		player.ExpectSharedStreams(len(st.members) > 1)
		// The links the room already holds, so a member who joins late or
		// seeks back needs no one to sign again.
		for _, shared := range ev.Welcome.Streams {
			a.togetherRememberStream(shared)
		}
		if ev.Welcome.Stream != nil {
			// The link the room already holds, for a member who joins late.
			a.togetherUseStream(*ev.Welcome.Stream)
		}
		if st.playback != nil && st.playback.Track != nil {
			// The room's state is authoritative, including for a host that
			// was away while the other listener paused or sought.
			a.togetherFollow(time.Now())
		} else if st.host {
			a.togetherPublish(true)
		}
	case together.EventMembers:
		if len(ev.Members) > len(st.members) {
			// Someone joined: the room sends the current link again, so the
			// newcomer does not need to wait for a track change.
			st.sharedKey = ""
		}
		st.members = ev.Members
		player.ExpectSharedStreams(len(ev.Members) > 1)
		a.togetherShareCurrent()
	case together.EventPlayback:
		st.playback = ev.Playback
		a.togetherHeardPlayback(ev.Playback)
		a.togetherFollow(time.Now())
		a.togetherCheckPermission()
	case together.EventStreamRequest:
		// Another member cannot play the room's track: hand over the link
		// this account already holds, or sign one for the room now. A
		// member whose account holds the rights can supply the room even
		// while their own player is idle, which is what lets the listener
		// without a membership simply request a song and hear it.
		a.togetherOfferStream(ev.TrackID)
	case together.EventPlay:
		if ev.Track != nil {
			a.togetherPlayRequested(*ev.Track)
		}
	case together.EventRequest:
		if st.host && !st.applying {
			switch ev.Action {
			case "next":
				a.pl.Next()
			case "prev":
				a.pl.Prev()
			}
		}
	case together.EventPending:
		st.pending = ev.Pending
	case together.EventQueue:
		st.queue = ev.Queue
		a.togetherFollowMode()
	case together.EventStream:
		if ev.Stream != nil {
			a.togetherUseStream(*ev.Stream)
		}
	case together.EventYou:
		if ev.You == nil {
			return
		}
		wasHost := st.host
		st.host = ev.You.Host
		st.selfID = ev.You.Client
		switch {
		case st.host && !wasHost:
			// The room is this member's now: keep it playing from here, so
			// the hand-over costs the others no more than a frame.
			st.lastPublish = time.Time{}
			a.togetherPublish(true)
			a.togetherNote("你现在是房主了")
		case !st.host && wasHost:
			a.togetherNote("已把房间交给新房主")
		}
	case together.EventChat:
		if ev.Chat == nil {
			return
		}
		if a.togetherAcceptStream(ev.Chat.Text) {
			return
		}
		st.chat = append(st.chat, *ev.Chat)
		if len(st.chat) > 100 {
			st.chat = st.chat[len(st.chat)-100:]
		}
		a.nowPlaying = false
	case together.EventServerError:
		// The room answers in the page, not over it. A refusal for being
		// quick is not the listener's mistake: the client paces itself
		// (see togetherPublishGap), so the bursting that caused it stops,
		// and the next anchor lands on the ticker a moment later.
		switch ev.Code {
		case "not_host":
			a.togetherNote("播放由房主控制，可以点歌到点歌台")
		case "pending_full":
			a.togetherNote("点歌台已经满了，等一首被播放或移除")
		case "bad_track":
			a.togetherNote("这首歌在房间里放不了")
		case "not_allowed":
			a.togetherNote("只有点歌的人或房主能移除")
		case "no_member", "bad_target":
			a.togetherNote("那个人已经不在房间里了")
		case "bad_stream":
			a.togetherNote("房间没有接受这条音源")
		case "rate_limited":
			// Say nothing: the client already spaces its messages, and a
			// warning for it would be a popup the listener cannot act on.
		}
	}
	a.redraw()
}

// togetherPublishSoon sends the anchor a moment after the player changes,
// coalescing the burst one action makes. The room follows the host's player
// at once rather than on a four-second beat, which is what makes the room and
// the client the same thing instead of two.
func (a *app) togetherPublishSoon() {
	st := &a.together
	if !st.joined || st.client == nil || st.publishTimer != nil {
		return
	}
	gen := st.gen
	st.publishTimer = time.AfterFunc(120*time.Millisecond, func() {
		a.postAlways(func() {
			if a.together.gen != gen {
				return
			}
			a.together.publishTimer = nil
			a.togetherPublish(true)
		})
	})
}

// togetherPublish sends the anchor: the track, whether it plays and where.
// The server stamps when it arrives, so guest clocks do not depend on the
// host's. A host also mirrors its queue, so a guest's client holds the same
// list and the next track needs no new load.
func (a *app) togetherPublish(force bool) {
	st := &a.together
	if !st.joined || st.client == nil || a.pl == nil || !st.client.Online() {
		return
	}
	if st.host {
		a.togetherPublishQueue()
	}
	now := time.Now()
	cur := a.pl.Current()
	var track *together.Track
	var id int64
	if cur != nil {
		track = togetherTrack(*cur)
		id = cur.ID
	}
	playing := a.pl.Playing()
	pos := a.pl.Position()
	due := st.publishDue(id, playing, pos, now)
	if id != 0 {
		// Hand the room the link this account resolved, so one membership
		// is enough for the whole room. Do it even when the anchor is not
		// due: the first anchor lands before the stream has loaded.
		a.togetherShareStream(id)
	}
	if !force && !due {
		return
	}
	if spare := st.publishWait(now); spare > 0 {
		// The relay drops a member that says more than ten things a
		// second. Waiting the remainder of the gap here keeps the room
		// inside that even while a progress bar is being dragged, and no
		// one has to be told off for dragging it.
		if st.publishTimer != nil {
			st.publishTimer.Stop()
		}
		gen := st.gen
		st.publishTimer = time.AfterFunc(spare, func() {
			a.postAlways(func() {
				if a.together.gen != gen {
					return
				}
				a.together.publishTimer = nil
				a.togetherPublish(true)
			})
		})
		return
	}
	st.txAt = now
	st.lastPublish, st.lastPlaying, st.lastPos, st.lastPosAt, st.lastTrack = now, playing, pos, now, id
	a.togetherHeard(id, roomTrackName(track))
	pb := together.Playback{Track: track, Playing: playing, PositionMs: pos.Milliseconds()}
	client := st.client
	go func() { _ = client.Publish(pb) }()
	if !st.host {
		// Believe the listener until the room echoes the state back, so the
		// next drift check does not undo their own pause or seek.
		local := pb
		local.UpdatedAt = client.ServerNow()
		st.playback = &local
	}
}

// togetherPublishQueue mirrors the host's queue to the room when it has
// changed: the list, and where in it the room is.
func (a *app) togetherPublishQueue() {
	st := &a.together
	if !st.joined || st.client == nil || !st.host || !st.client.Online() || a.pl == nil {
		return
	}
	if !st.hasFeature("queue") {
		return
	}
	queue := a.pl.Queue()
	if len(queue) == 0 {
		return
	}
	at := 0
	if cur := a.pl.Current(); cur != nil {
		for i := range queue {
			if queue[i].ID == cur.ID {
				at = i
				break
			}
		}
	}
	shuffle := a.pl.Shuffle()
	sig := togetherQueueSignature(queue, at, shuffle)
	if sig == st.queueSig {
		return
	}
	st.queueSig = sig
	st.roomShuffle = shuffle
	tracks := make([]together.Track, 0, min(len(queue), 60))
	for _, t := range queue {
		if len(tracks) >= 60 {
			break
		}
		tracks = append(tracks, *togetherTrack(t))
	}
	q := together.Queue{Tracks: tracks, Index: at, Shuffle: shuffle}
	client := st.client
	go func() { _ = client.PublishQueue(q) }()
}

// roomTrackName is a room track's title, or nothing when the room has no
// song loaded.
func roomTrackName(t *together.Track) string {
	if t == nil {
		return ""
	}
	return t.Name
}

// togetherQueueSignature is what the room was last told, cheaply compared.
func togetherQueueSignature(queue []qqmusic.Track, at int, shuffle bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%t|", at, shuffle)
	for i, t := range queue {
		if i >= 60 {
			break
		}
		fmt.Fprintf(&b, "%d,%d;", t.ID, t.Duration)
	}
	return b.String()
}

// togetherFollowMode holds a guest's queue in the same play order as the
// room's: QQ 音乐 puts playMode and songRandomIndex in the room, so both
// listeners move through the list the same way instead of each shuffling
// its own copy.
func (a *app) togetherFollowMode() {
	st := &a.together
	if !st.joined || st.host || a.pl == nil || st.queue == nil {
		return
	}
	st.roomShuffle = st.queue.Shuffle
	if a.pl.Shuffle() == st.queue.Shuffle {
		return
	}
	st.applying = true
	a.pl.SetShuffle(st.queue.Shuffle)
	st.applying = false
}

// roomQueue turns the room's mirrored queue into the list a guest's player
// takes, starting where the room's track is in it.
func roomQueue(q *together.Queue, trackID int64) ([]qqmusic.Track, int, bool) {
	if q == nil || len(q.Tracks) == 0 {
		return nil, 0, false
	}
	tracks := make([]qqmusic.Track, len(q.Tracks))
	at := -1
	for i, t := range q.Tracks {
		tracks[i] = qqmusicTrack(t)
		if t.ID == trackID && at < 0 {
			at = i
		}
	}
	if at < 0 {
		return nil, 0, false
	}
	return tracks, at, true
}

// publishWait is how long this member still holds its tongue before the room
// can hear another anchor: the relay drops a member that speaks more than
// ten times a second, and staying under that here means the room never has
// to refuse an anchor, so the listener never sees a warning.
func (st *togetherState) publishWait(now time.Time) time.Duration {
	if st.txAt.IsZero() {
		return 0
	}
	if wait := togetherPublishGap - now.Sub(st.txAt); wait > 0 {
		return wait
	}
	return 0
}

func (st *togetherState) publishDue(id int64, playing bool, pos time.Duration, now time.Time) bool {
	if st.lastPublish.IsZero() {
		return true
	}
	if id != st.lastTrack || playing != st.lastPlaying {
		return true
	}
	if now.Sub(st.lastPublish) >= 4*time.Second {
		return true
	}
	want := st.lastPos
	if st.lastPlaying {
		want += now.Sub(st.lastPosAt)
	}
	if d := pos - want; d > 1500*time.Millisecond || d < -1500*time.Millisecond {
		return true
	}
	return false
}

// togetherFollow makes a guest's player match the room.
func (a *app) togetherFollow(now time.Time) {
	st := &a.together
	if !st.joined || st.client == nil || a.pl == nil || st.playback == nil || st.playback.Track == nil {
		return
	}
	local := together.Local{Position: a.pl.Position(), Playing: a.pl.Playing(), Loading: a.pl.Loading()}
	if cur := a.pl.Current(); cur != nil {
		local.TrackID = cur.ID
	}
	step := together.Plan(st.playback, local, st.client.ServerNow(), now, st.lastSeek)
	if a.pl.Err() != "" && !togetherRetryable(a.pl.Err()) {
		// A track this account may not play stays that way until the
		// room's link arrives: pressing play again every second would
		// only ask QQ again, and log in again, for the same answer.
		step.SetPlaying = nil
	}
	if !step.Reload && step.SetPlaying == nil && !step.Seek {
		return
	}
	st.applying = true
	defer func() { st.applying = false }()
	if step.Reload {
		st.lastSeek = time.Time{}
		a.pl.ClearErr()
		tracks := []qqmusic.Track{qqmusicTrack(*st.playback.Track)}
		start := 0
		// The host mirrors its queue: a guest holds the same list, so the
		// two clients are one player, not two.
		if list, at, ok := roomQueue(st.queue, st.playback.Track.ID); ok {
			tracks, start = list, at
		}
		a.pl.PlayQueue(tracks, start)
	}
	if step.Seek {
		a.pl.Seek(step.Target)
		// A seek while the track loads only sets where it lands; it is not
		// a correction to wait out, so the first frame after loading may
		// correct the drift at once.
		if !a.pl.Loading() {
			st.lastSeek = now
		}
	}
	if step.SetPlaying != nil && a.pl.Playing() != *step.SetPlaying {
		a.pl.TogglePlay()
	}
	if err := a.pl.Err(); err != "" && err != st.seenErr {
		st.seenErr = err
		// A song this account may not play is already explained under it,
		// by the room waiting for another member's link: saying it twice
		// would be the same sentence in two places.
		if !playbackNeedsPermission(err) {
			a.togetherWarn(err)
		}
		// Whatever the reason the room's track did not load here, another
		// member may hold a link that plays: ask, unless this is a room of
		// one, where nobody can answer.
		if len(st.members) > 1 && st.playback != nil && st.playback.Track != nil {
			a.togetherAskStream(st.playback.Track.ID)
		}
	}
}

// togetherSyncIdentity tells the room the name and portrait this member
// now has: a QQ Music session that restores after the room was joined
// changes them, and the others should see that name, not a placeholder.
func (a *app) togetherSyncIdentity() {
	st := &a.together
	if !st.joined || st.client == nil {
		return
	}
	name, avatar := a.togetherIdentity()
	if name == st.selfName && avatar == st.selfAvatar {
		return
	}
	st.selfName, st.selfAvatar = name, avatar
	if !st.hasFeature("profile") {
		// An older relay only reads the name when a member joins: join
		// again, so the others see the QQ Music name once it restored.
		a.togetherReconnect()
		return
	}
	client := st.client
	go func() { _ = client.SetProfile(name, avatar) }()
}

// togetherReconnect joins the same room again, keeping its code and the
// host key, as when the name this member shows changes on an old relay.
func (a *app) togetherReconnect() {
	st := &a.together
	if !st.joined || st.code == "" {
		return
	}
	a.togetherConnect(st.code, st.hostKey)
}

func (st *togetherState) hasFeature(name string) bool {
	return st.features[name]
}

// togetherShareCurrent shares the stream this account resolved for the
// room's track, and, when this account has none yet, asks QQ to sign one for
// the room: any member whose account holds the rights can supply it.
func (a *app) togetherShareCurrent() {
	st := &a.together
	if !st.joined || st.client == nil || st.playback == nil || st.playback.Track == nil {
		return
	}
	id := st.playback.Track.ID
	if cur := a.pl.Current(); cur != nil && cur.ID == id {
		if s, ok := a.pl.StreamURL(); ok && s.URL != "" && !s.Trial {
			// The room already plays someone's link: keep playing it in
			// step rather than offering this account's own, which would
			// have the two members replace each other's every second.
			if held, ok := st.roomStream[id]; ok && held != s.URL {
				return
			}
			a.togetherShareStream(id)
			return
		}
	}
	// Otherwise this account is already loading the room's song, as any
	// listener would: its own player signs the link and the ticker shares
	// it. Nothing is asked of QQ Music here, so ordinary playback looks
	// exactly like one person playing a song in their own client.
}

// togetherRoomTrack finds the room's own description of a track, by id.
func (a *app) togetherRoomTrack(id int64) (together.Track, bool) {
	st := &a.together
	if st.playback != nil && st.playback.Track != nil && st.playback.Track.ID == id {
		return *st.playback.Track, true
	}
	if st.queue != nil {
		for _, tr := range st.queue.Tracks {
			if tr.ID == id {
				return tr, true
			}
		}
	}
	if st.pending != nil {
		for _, p := range st.pending {
			if p.Track.ID == id {
				return p.Track, true
			}
		}
	}
	return together.Track{}, false
}

// togetherRememberStream keeps a link the room holds for a track, without
// touching what plays here: the welcome sends them all at once, and the
// player takes the one it needs when it loads. A link too short to be the
// song is not kept: an older client may have shared its trial by mistake.
func (a *app) togetherRememberStream(shared together.Stream) bool {
	if shared.TrackID == 0 || shared.URL == "" {
		return false
	}
	if tr, ok := a.togetherRoomTrack(shared.TrackID); ok {
		if qqmusicTrack(tr).TrialLike(qqmusic.Stream{Size: shared.Size, Level: shared.Level}) {
			return false
		}
	}
	st := &a.together
	player.ShareStream(shared.TrackID, qqmusic.Stream{
		URL:   shared.URL,
		Type:  shared.Type,
		Size:  shared.Size,
		Level: shared.Level,
	})
	// This link came from the room, so it must not be sent straight back.
	st.sharedKey = fmt.Sprintf("%d|%s", shared.TrackID, shared.URL)
	if shared.ByName != "" {
		if st.streamFrom == nil {
			st.streamFrom = map[int64]string{}
		}
		st.streamFrom[shared.TrackID] = shared.ByName
	}
	// The room's link, as everyone else knows it: the page says whose it
	// is, and this member does not answer with one of its own.
	if st.roomStream == nil {
		st.roomStream = map[int64]string{}
	}
	st.roomStream[shared.TrackID] = shared.URL
	// The room's current song is checked against the CDN itself: an older
	// client may have shared its trial while reporting the whole song's
	// size. Only that one is probed, once per link, so joining a room does
	// not send a request for every link it holds.
	if st.playback != nil && st.playback.Track != nil && st.playback.Track.ID == shared.TrackID && shared.Size > 0 {
		track := qqmusicTrack(*st.playback.Track)
		if track.NeedsRights() {
			if st.verified == nil {
				st.verified = map[int64]string{}
			}
			if st.verified[shared.TrackID] != shared.URL {
				st.verified[shared.TrackID] = shared.URL
				go a.togetherVerifyStream(track, shared)
			}
		}
	}
	return true
}

// togetherVerifyStream asks the CDN how long a room's link really is, and
// drops it if it turns out to be the thirty seconds an account without the
// rights hears: a trial must not pass for the song just because the client
// that sent it said so.
func (a *app) togetherVerifyStream(track qqmusic.Track, shared together.Stream) {
	size := qqmusic.ProbeStreamSize(shared.URL)
	if size <= 0 || !track.TrialLike(qqmusic.Stream{Size: size, Level: shared.Level}) {
		return
	}
	a.post(func() {
		st := &a.together
		if st.playback == nil || st.playback.Track == nil || st.playback.Track.ID != shared.TrackID {
			return
		}
		held, ok := player.SharedStream(shared.TrackID)
		if !ok || held.URL != shared.URL {
			// A newer link has arrived meanwhile: it is not this one's turn.
			return
		}
		player.ForgetShared(shared.TrackID)
		delete(st.roomStream, shared.TrackID)
		delete(st.streamFrom, shared.TrackID)
		st.sharedKey, st.sharedURL = "", ""
		st.streamAsked, st.streamAskedAt = 0, time.Time{}
		a.togetherAskStream(shared.TrackID)
		// Reload, so the trial does not keep playing while the room asks
		// for the real link.
		if a.pl != nil {
			if cur := a.pl.Current(); cur != nil && cur.ID == shared.TrackID {
				tracks := []qqmusic.Track{track}
				start := 0
				if list, at, ok := roomQueue(st.queue, shared.TrackID); ok {
					tracks, start = list, at
				}
				st.applying = true
				a.pl.ClearErr()
				a.pl.PlayQueue(tracks, start)
				st.applying = false
			}
		}
		a.redraw()
	})
}

// togetherShareKnown gives the room a link this account already holds, once
// per link.
func (a *app) togetherShareKnown(id int64, s qqmusic.Stream) {
	st := &a.together
	if st.client == nil || id == 0 || s.URL == "" || s.Trial {
		return
	}
	key := fmt.Sprintf("%d|%s", id, s.URL)
	if key == st.sharedKey {
		return
	}
	// One offer of the same song every couple of seconds is all the room
	// accepts; offering faster only earns a refusal the listener cannot
	// use, so the offer waits its turn instead.
	now := time.Now()
	if at, ok := st.shareAt[id]; ok && now.Sub(at) < togetherShareGap {
		return
	}
	if st.shareAt == nil {
		st.shareAt = map[int64]time.Time{}
	}
	st.shareAt[id] = now
	st.sharedKey = key
	client := st.client
	go func() {
		_ = client.ShareStream(together.Stream{TrackID: id, URL: s.URL, Type: s.Type, Size: s.Size, Level: s.Level})
	}()
}

// togetherSignEvery is how often this account asks QQ to sign one track for
// the room: a track nobody here can play is not worth asking for every
// second, and the one who can sign answers on the first ask.
const togetherSignEvery = 15 * time.Second

// togetherSignFailedFor is how long a refusal holds for one track: the
// rights behind it do not change mid-song, so retrying only adds requests.
const togetherSignFailedFor = 10 * time.Minute

// togetherSignTrack asks QQ for the room's track with this account, for the
// room rather than for this player: a member who holds the rights can answer
// another member's request even while their own player is idle, so the
// listener without a membership only has to request the song. A trial answer
// means this account does not hold them, and is never shared.
func (a *app) togetherSignTrack(id int64, tr together.Track) {
	st := &a.together
	if !st.joined || st.client == nil || id <= 0 {
		return
	}
	if known, ok := player.SharedStream(id); ok && known.URL != "" && !known.Trial {
		// The link the room already has is not an answer to a request
		// that link failed: only a different one is.
		if held := st.roomStream[id]; held != known.URL {
			a.togetherShareKnown(id, known)
			return
		}
	}
	if st.signTried == nil {
		st.signTried = map[int64]time.Time{}
	}
	if at, ok := st.signFailed[id]; ok && time.Since(at) < togetherSignFailedFor {
		// This account already answered for this track: a refusal is the
		// same answer, and QQ Music should not hear it again.
		return
	}
	if at, ok := st.signTried[id]; ok && time.Since(at) < togetherSignEvery {
		return
	}
	st.signTried[id] = time.Now()
	if len(st.signTried) > 80 {
		cut := time.Now().Add(-10 * time.Minute)
		for track, at := range st.signTried {
			if at.Before(cut) {
				delete(st.signTried, track)
			}
		}
	}
	level := a.settings.Quality
	client := st.client
	go func() {
		s, err := qqmusic.SongURL(id, level)
		signed := err == nil && s != nil && s.URL != "" && !s.Trial
		a.post(func() {
			if !a.together.joined {
				return
			}
			state := &a.together
			if state.signFailed == nil {
				state.signFailed = map[int64]time.Time{}
			}
			if !signed {
				state.signFailed[id] = time.Now()
				if len(state.signFailed) > 80 {
					cut := time.Now().Add(-togetherSignFailedFor)
					for track, at := range state.signFailed {
						if at.Before(cut) {
							delete(state.signFailed, track)
						}
					}
				}
				return
			}
			delete(state.signFailed, id)
		})
		if !signed {
			return
		}
		_ = client.ShareStream(together.Stream{TrackID: id, URL: s.URL, Type: s.Type, Size: s.Size, Level: s.Level})
	}()
}

// togetherOfferStream answers another member's request for a track's link:
// offer a link of this account's own that the room does not have, then sign
// one now. The room's failing link is never sent back as its own answer.
func (a *app) togetherOfferStream(id int64) {
	st := &a.together
	if !st.joined || st.client == nil || id <= 0 {
		return
	}
	st.sharedKey = ""
	if cur := a.pl.Current(); cur != nil && cur.ID == id {
		if s, ok := a.pl.StreamURL(); ok && s.URL != "" && !s.Trial && s.URL != st.roomStream[id] {
			a.togetherShareKnown(id, s)
		}
	}
	if tr, ok := a.togetherRoomTrack(id); ok {
		a.togetherSignTrack(id, tr)
	}
}

// togetherAskStream asks the room for a link this account cannot resolve,
// at most once every twenty seconds for the same track.
func (a *app) togetherAskStream(id int64) {
	st := &a.together
	if !st.joined || st.client == nil || id == 0 {
		return
	}
	if st.streamAsked == id && time.Since(st.streamAskedAt) < 20*time.Second {
		return
	}
	if !st.hasFeature("stream") {
		// An older relay does not pass the question on; the ticker's
		// sharing still brings the link over.
		return
	}
	st.streamAsked, st.streamAskedAt = id, time.Now()
	// A link that failed here must be tried again when the room re-sends
	// it: the guard below is about not retrying the same dead link in a
	// loop, not about refusing the answer to a question just asked.
	st.sharedURL = ""
	client := st.client
	go func() { _ = client.RequestStream(id) }()
}

// togetherCheckPermission asks the room for a stream when this account may
// not play the room's track, host or guest: whoever has the membership
// shares it.
func (a *app) togetherCheckPermission() {
	st := &a.together
	if !st.joined || st.playback == nil || st.playback.Track == nil {
		return
	}
	if cur := a.pl.Current(); cur == nil || cur.ID != st.playback.Track.ID {
		return
	}
	if a.pl.Err() != "" {
		a.togetherAskStream(st.playback.Track.ID)
	}
}

// togetherRetryable reports whether pressing play again could help: a
// missing membership or an expired login needs another answer, not another
// attempt.
func togetherRetryable(err string) bool {
	return err != "" && !playbackNeedsPermission(err) && !strings.Contains(err, "登录") && !strings.Contains(err, "续期")
}

// togetherControlsLocked keeps a guest's own transport keys and buttons from
// fighting the room.
func (a *app) togetherControlsLocked() bool {
	st := &a.together
	if !st.joined || st.host || st.applying {
		return false
	}
	a.togetherNote("选歌由房主决定，可以点歌到点歌台")
	return true
}

// togetherPlayPause and togetherSeek note that the listener, not the room,
// changed the transport, so a guest publishes it to everyone.
func (a *app) togetherPlayPause() { a.togetherUserTransport() }
func (a *app) togetherSeek()      { a.togetherUserTransport() }

func (a *app) togetherUserTransport() {
	st := &a.together
	if st.joined && !st.host && !st.applying && a.pl != nil && a.pl.Current() != nil {
		st.userActed = true
	}
}

func (a *app) togetherNextRequest() bool { return a.togetherTransportRequest("next") }
func (a *app) togetherPrevRequest() bool { return a.togetherTransportRequest("prev") }

// togetherTransportRequest asks the host to move the queue, which only the
// host's player knows. A true return stops the guest's own player.
func (a *app) togetherTransportRequest(action string) bool {
	st := &a.together
	if !st.joined || st.host || st.applying || st.client == nil {
		return false
	}
	client := st.client
	go func() { _ = client.Request(action) }()
	return true
}

// togetherPlayQueueAt starts the host's queue at i: QQ 音乐 lets the host
// pick a song out of the room's list, and the room follows at once.
func (a *app) togetherPlayQueueAt(i int) {
	st := &a.together
	if !st.joined || !st.host || a.pl == nil {
		return
	}
	queue := a.pl.Queue()
	if i < 0 || i >= len(queue) {
		return
	}
	st.applying = true
	a.pl.ClearErr()
	a.pl.PlayQueue(queue, i)
	st.applying = false
	st.lastSeek = time.Time{}
	a.togetherPublish(true)
	a.redraw()
}

func (a *app) togetherRequestSong(tr qqmusic.Track) {
	st := &a.together
	if !st.joined || st.client == nil {
		return
	}
	if len(st.pending) >= 50 {
		a.togetherNote("点歌台已经满了，等一首被播放或移除")
		return
	}
	track := *togetherTrack(tr)
	client := st.client
	go func() {
		if err := client.AddPending(track); err != nil {
			a.post(func() { a.togetherWarn("点歌失败：" + err.Error()) })
		}
	}()
	a.togetherPoint(tr)
}

// togetherPlayPending asks the room to start a requested song. A relay
// that knows the desk hands it to the host's queue, so the queue stays
// the host's; an older one only lets this client play it and publish the
// new anchor.
func (a *app) togetherPlayPending(p together.Pending) {
	st := &a.together
	if !st.joined || st.client == nil || a.pl == nil {
		return
	}
	client := st.client
	if st.hasFeature("desk") {
		go func() { _ = client.PlayPending(p.ID) }()
		return
	}
	track := qqmusicTrack(p.Track)
	st.applying = true
	a.pl.ClearErr()
	if st.host && a.pl.Current() != nil {
		a.pl.PlayNext(track)
		a.pl.Next()
	} else {
		a.pl.PlayQueue([]qqmusic.Track{track}, 0)
	}
	st.applying = false
	st.lastSeek = time.Time{}
	a.togetherPublish(true)
	if st.host || p.By == st.selfID {
		go func() { _ = client.RemovePending(p.ID) }()
	}
}

// togetherPlayRequested starts a song another member picked from the desk:
// only the host's queue holds it, so only the host acts on the message.
func (a *app) togetherPlayRequested(t together.Track) {
	st := &a.together
	if !st.joined || !st.host || a.pl == nil {
		return
	}
	track := qqmusicTrack(t)
	st.applying = true
	a.pl.ClearErr()
	if a.pl.Current() == nil {
		a.pl.PlayQueue([]qqmusic.Track{track}, 0)
	} else {
		a.pl.PlayNext(track)
		a.pl.Next()
	}
	st.applying = false
	st.lastSeek = time.Time{}
	a.togetherPublish(true)
}

// togetherSearchSongs looks for songs to request, in the room's desk.
func (a *app) togetherSearchSongs() {
	st := &a.together
	query := strings.TrimSpace(st.requestQuery)
	if query == "" || st.requestLoading {
		return
	}
	st.requestLoading, st.requestErr = true, ""
	st.requestGen++
	gen := st.requestGen
	go func() {
		songs, err := qqmusic.Search(query, 8)
		a.post(func() {
			if st.requestGen != gen {
				return
			}
			st.requestLoading = false
			if err != nil {
				st.requestErr = "搜索失败，稍后再试"
				a.redraw()
				return
			}
			st.requestResults = songs
			a.redraw()
		})
	}()
	a.redraw()
}

func (a *app) togetherRemovePending(p together.Pending) {
	if !a.together.joined || a.together.client == nil {
		return
	}
	client := a.together.client
	go func() { _ = client.RemovePending(p.ID) }()
}

// togetherKick removes a member; only the host's client asks.
func (a *app) togetherKick(m together.Member) {
	if !a.together.joined || !a.together.host || a.together.client == nil || m.Host {
		return
	}
	client := a.together.client
	go func() { _ = client.Kick(m.Client) }()
	a.togetherNote("已把「" + m.Name + "」移出房间")
}

// togetherTransferHost hands the room to another member, as QQ 音乐 lets a
// host do: the queue, the desk and the transport become theirs, and this
// client becomes a guest.
func (a *app) togetherTransferHost(m together.Member) {
	st := &a.together
	if !st.joined || !st.host || st.client == nil || m.Host || m.Client == st.selfID {
		return
	}
	client := st.client
	go func() { _ = client.Transfer(m.Client) }()
	a.togetherNote("已把房主交给「" + m.Name + "」")
}

func (a *app) togetherCopyInvite() {
	if !a.together.joined {
		return
	}
	text := fmt.Sprintf("来 Lilt 一起听歌，房间号 %s（打开 Lilt 的「一起听」，输入房间号加入）", a.together.code)
	if err := copyText(text); err != nil {
		a.togetherWarn("复制失败，可以手动记下房间号 " + a.together.code)
		return
	}
	a.togetherNote("邀请已复制，发给朋友就能一起听")
}

func togetherTrack(t qqmusic.Track) *together.Track {
	artists := make([]together.Artist, len(t.Artists))
	for i, ar := range t.Artists {
		artists[i] = together.Artist{ID: ar.ID, Name: ar.Name}
	}
	return &together.Track{
		ID:       t.ID,
		MID:      t.MID,
		MediaMID: t.MediaMID,
		Name:     t.Name,
		Artists:  artists,
		Album: together.Album{
			ID:     t.Album.ID,
			MID:    t.Album.MID,
			Name:   t.Album.Name,
			PicURL: t.Album.PicURL,
		},
		Duration: t.Duration,
		Fee:      t.Fee,
	}
}

func qqmusicTrack(t together.Track) qqmusic.Track {
	artists := make([]qqmusic.Artist, len(t.Artists))
	for i, ar := range t.Artists {
		artists[i] = qqmusic.Artist{ID: ar.ID, Name: ar.Name}
	}
	return qqmusic.Track{
		ID:       t.ID,
		MID:      t.MID,
		MediaMID: t.MediaMID,
		Name:     t.Name,
		Artists:  artists,
		Album: qqmusic.Album{
			ID:     t.Album.ID,
			MID:    t.Album.MID,
			Name:   t.Album.Name,
			PicURL: t.Album.PicURL,
		},
		Duration: t.Duration,
		Fee:      t.Fee,
	}
}

// togetherTogglePlay plays or pauses the room's song, loading it first if
// this client has not got it yet.
func (a *app) togetherTogglePlay() {
	st := &a.together
	if a.pl == nil {
		return
	}
	if cur := a.pl.Current(); cur == nil || st.playback == nil || st.playback.Track == nil || cur.ID != st.playback.Track.ID {
		a.togetherFollow(time.Now())
		return
	}
	a.pl.TogglePlay()
}

// togetherChunkMax is how much of an encoded hint one chat message carries:
// the relay keeps 500 bytes per message and trims what exceeds it.
const togetherChunkMax = 360

// togetherShareChunk is one piece of an encoded stream hint.
type togetherShareChunk struct {
	Ref   string `json:"r"`
	Index int    `json:"i"`
	Parts int    `json:"n"`
	Data  string `json:"d"`
}

type togetherShareBuffer struct {
	parts []string
	at    time.Time
}

// togetherShareStream sends the host's resolved stream for the current track,
// once per link, to the room's guests.
func (a *app) togetherShareStream(id int64) {
	st := &a.together
	if st.client == nil || a.pl == nil {
		return
	}
	cur := a.pl.Current()
	if cur == nil || cur.ID != id {
		return
	}
	stream, ok := a.pl.StreamURL()
	if !ok || stream.URL == "" || stream.Trial {
		// A trial never goes to the room: it would have everyone listen to
		// thirty seconds of a song and drift out of step.
		return
	}
	if st.hasFeature("stream2") {
		// The room carries the link itself: no chat, no chunks, and a
		// member who joins late gets it with the welcome.
		a.togetherShareKnown(id, stream)
		return
	}
	key := fmt.Sprintf("%d|%s", id, stream.URL)
	if key == st.sharedKey {
		return
	}
	st.sharedKey = key
	client := st.client
	payload, err := json.Marshal(togetherSharedStream{ID: id, URL: stream.URL, Type: stream.Type, Size: stream.Size, Level: stream.Level})
	if err != nil {
		return
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	parts := (len(encoded) + togetherChunkMax - 1) / togetherChunkMax
	ref, err := togetherShareRef()
	if err != nil {
		return
	}
	messages := make([]string, 0, parts)
	for i := 0; i < parts; i++ {
		end := (i + 1) * togetherChunkMax
		if end > len(encoded) {
			end = len(encoded)
		}
		body, err := json.Marshal(togetherShareChunk{Ref: ref, Index: i, Parts: parts, Data: encoded[i*togetherChunkMax : end]})
		if err != nil {
			return
		}
		messages = append(messages, togetherShareMarker+string(body))
	}
	go func() {
		for _, text := range messages {
			if err := client.Chat(text); err != nil {
				return
			}
		}
	}()
}

func togetherShareRef() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// togetherAcceptStream takes a host's stream hint off the room chat and hides
// it from the transcript. A true return means the message was a hint.
func (a *app) togetherAcceptStream(text string) bool {
	if !strings.HasPrefix(text, togetherShareMarker) {
		return false
	}
	var chunk togetherShareChunk
	if err := json.Unmarshal([]byte(strings.TrimPrefix(text, togetherShareMarker)), &chunk); err != nil {
		return true
	}
	if chunk.Ref == "" || chunk.Parts <= 0 || chunk.Parts > 16 || chunk.Index < 0 || chunk.Index >= chunk.Parts {
		return true
	}
	st := &a.together
	if st.sharedParts == nil {
		st.sharedParts = map[string]*togetherShareBuffer{}
	}
	for ref, b := range st.sharedParts {
		if time.Since(b.at) > 30*time.Second {
			delete(st.sharedParts, ref)
		}
	}
	buf := st.sharedParts[chunk.Ref]
	if buf == nil {
		buf = &togetherShareBuffer{parts: make([]string, chunk.Parts)}
		st.sharedParts[chunk.Ref] = buf
	}
	if len(buf.parts) != chunk.Parts {
		return true
	}
	buf.at = time.Now()
	buf.parts[chunk.Index] = chunk.Data
	for _, part := range buf.parts {
		if part == "" {
			return true
		}
	}
	delete(st.sharedParts, chunk.Ref)
	payload, err := base64.StdEncoding.DecodeString(strings.Join(buf.parts, ""))
	if err != nil {
		return true
	}
	var shared togetherSharedStream
	if err := json.Unmarshal(payload, &shared); err != nil {
		return true
	}
	if shared.ID == 0 || shared.URL == "" {
		return true
	}
	a.togetherUseStream(together.Stream{
		TrackID: shared.ID,
		URL:     shared.URL,
		Type:    shared.Type,
		Size:    shared.Size,
		Level:   shared.Level,
	})
	return true
}

// togetherUseStream records the room's link and, when the room's track did
// not load here, plays it again now: the member whose account signed it may
// be the only one in the room with the membership the track needs. Any
// failure counts, not only a permission one, because a client that is not
// signed in at all cannot resolve a link either.
func (a *app) togetherUseStream(shared together.Stream) {
	if shared.TrackID == 0 || shared.URL == "" {
		return
	}
	if !a.togetherRememberStream(shared) {
		return
	}
	st := &a.together
	if a.pl == nil || st.playback == nil || st.playback.Track == nil || st.playback.Track.ID != shared.TrackID {
		return
	}
	if cur := a.pl.Current(); cur == nil || cur.ID != shared.TrackID {
		return
	}
	// The link the player is already using, or one that failed too, is not
	// worth another load: the player waits for the room's link itself.
	local, hasLocal := a.pl.StreamURL()
	if hasLocal && local.URL == shared.URL {
		return
	}
	// A track that plays the room's full link is where it should be. One
	// that failed here, or that only got thirty seconds of a song this
	// account has no rights to, is not: reload it with the room's link.
	if a.pl.Err() == "" && !(hasLocal && local.Trial) {
		return
	}
	if st.sharedURL == shared.URL {
		return
	}
	st.sharedURL = shared.URL
	st.seenErr = ""
	tracks := []qqmusic.Track{qqmusicTrack(*st.playback.Track)}
	start := 0
	if list, at, ok := roomQueue(st.queue, shared.TrackID); ok {
		tracks, start = list, at
	}
	// Applying, not the listener: a guest's queue is the room's.
	st.applying = true
	a.pl.ClearErr()
	a.pl.PlayQueue(tracks, start)
	st.applying = false
	a.redraw()
}
