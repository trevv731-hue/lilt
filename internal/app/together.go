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
// only the host needs a membership.
const togetherShareMarker = "\u2063lilt-stream "

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

	joinCode  string
	chatDraft string
	seenErr   string

	// The song request desk's search.
	requestQuery   string
	requestResults []qqmusic.Track
	requestLoading bool
	requestErr     string
	requestGen     int

	chatList    ui.ListState
	pendingList ui.ListState
	lastSeek    time.Time
	lastTrack   int64
	sharedKey   string
	sharedParts map[string]*togetherShareBuffer
	lastPublish time.Time
	lastPlaying bool
	lastPos     time.Duration
	lastPosAt   time.Time
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
		a.toast = "请输入房间号"
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
	st.status, st.err = together.StatusConnecting, ""
	st.members, st.playback, st.pending, st.chat = nil, nil, nil, nil
	st.seenErr, st.applying = "", false
	st.features = nil
	st.streamAsked, st.streamAskedAt = 0, time.Time{}
	st.requestQuery, st.requestResults, st.requestErr = "", nil, ""

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
		a.toast = "房间号是 8 位字母或数字"
		return
	}
	a.together.joinCode = ""
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
	st.busy, st.err = true, ""
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
				st.err = err.Error()
				a.toast = "创建房间失败，请重试"
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
				a.togetherLeave(true)
			})
		}()
		return
	}
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
	st.status, st.err = together.StatusOffline, ""
	st.members, st.playback, st.pending, st.chat = nil, nil, nil, nil
	st.features = nil
	st.applying, st.seenErr = false, ""
	st.selfName, st.selfAvatar = "", ""
	st.streamAsked, st.streamAskedAt = 0, time.Time{}
	st.requestQuery, st.requestResults, st.requestErr = "", nil, ""
	if announce {
		a.toast = "已离开一起听"
		a.redraw()
	}
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
			a.togetherLeave(false)
			if wasHost {
				a.toast = "房间已结束"
			} else {
				a.toast = together.ErrEnded.Error()
			}
			return
		}
		if errors.Is(ev.Err, together.ErrKicked) || errors.Is(ev.Err, together.ErrRoomNotFound) || errors.Is(ev.Err, together.ErrRoomFull) || errors.Is(ev.Err, together.ErrReplaced) {
			message := ev.Err.Error()
			a.togetherLeave(false)
			a.toast = message
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
		st.code = ev.Welcome.Code
		st.members = ev.Welcome.Members
		st.playback = ev.Welcome.Playback
		st.pending = ev.Welcome.Pending
		st.chat = ev.Welcome.Chat
		st.features = map[string]bool{}
		for _, f := range ev.Welcome.Features {
			st.features[f] = true
		}
		st.status = together.StatusOnline
		st.err = ""
		player.ExpectSharedStreams(len(st.members) > 1)
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
		a.togetherFollow(time.Now())
		a.togetherCheckPermission()
	case together.EventStreamRequest:
		// Another member cannot play the room's track: share what this
		// account resolved, even if it was shared before.
		if st.playback != nil && st.playback.Track != nil && st.playback.Track.ID == ev.TrackID {
			st.sharedKey = ""
			a.togetherShareCurrent()
		}
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
		switch ev.Code {
		case "not_host":
			a.toast = "房主控制播放"
		case "pending_full":
			a.toast = "点歌台已经满了"
		case "rate_limited":
			a.toast = "操作太频繁，稍后再试"
		case "bad_track":
			a.toast = "这首歌不能点播"
		case "not_allowed":
			a.toast = "只有点歌的人或房主能移除"
		}
	}
	a.redraw()
}

// togetherPublish sends the host's anchor: the track, whether it plays and
// where. The server stamps when it arrives, so guest clocks do not depend
// on the host's.
func (a *app) togetherPublish(force bool) {
	st := &a.together
	if !st.joined || st.client == nil || a.pl == nil || !st.client.Online() {
		return
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
	st.lastPublish, st.lastPlaying, st.lastPos, st.lastPosAt, st.lastTrack = now, playing, pos, now, id
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
	if !step.Reload && step.SetPlaying == nil && !step.Seek {
		return
	}
	st.applying = true
	defer func() { st.applying = false }()
	if step.Reload {
		st.lastSeek = time.Time{}
		a.pl.ClearErr()
		a.pl.PlayQueue([]qqmusic.Track{qqmusicTrack(*st.playback.Track)}, 0)
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
		a.toast = err
		if playbackNeedsPermission(err) && st.playback != nil && st.playback.Track != nil {
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
// room's track, if it is the track that plays here.
func (a *app) togetherShareCurrent() {
	st := &a.together
	if !st.joined || st.client == nil || st.playback == nil || st.playback.Track == nil {
		return
	}
	a.togetherShareStream(st.playback.Track.ID)
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
	if playbackNeedsPermission(a.pl.Err()) {
		a.togetherAskStream(st.playback.Track.ID)
	}
}

// togetherControlsLocked keeps a guest's own transport keys and buttons from
// fighting the room.
func (a *app) togetherControlsLocked() bool {
	st := &a.together
	if !st.joined || st.host || st.applying {
		return false
	}
	a.toast = "歌曲由房主选择，可用点歌台"
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
	a.toast = "已请求切歌"
	return true
}

func (a *app) togetherRequestSong(tr qqmusic.Track) {
	st := &a.together
	if !st.joined || st.client == nil {
		a.toast = "先加入一个房间"
		return
	}
	if len(st.pending) >= 50 {
		a.toast = "点歌台已经满了"
		return
	}
	track := *togetherTrack(tr)
	client := st.client
	go func() {
		if err := client.AddPending(track); err != nil {
			a.post(func() { a.toast = "点歌失败：" + err.Error() })
		}
	}()
	a.toast = "已点歌：" + tr.Name
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
		a.toast = "已点播：" + p.Track.Name
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
	if st.host || p.By == st.clientID {
		go func() { _ = client.RemovePending(p.ID) }()
	}
	a.toast = "正在播放：" + p.Track.Name
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
	a.toast = "已移出：" + m.Name
}

func (a *app) togetherCopyInvite() {
	if !a.together.joined {
		return
	}
	text := fmt.Sprintf("来 Lilt 一起听歌，房间号 %s（打开 Lilt 的「一起听」，输入房间号加入）", a.together.code)
	if err := copyText(text); err != nil {
		a.toast = "复制失败"
		return
	}
	a.toast = "邀请已复制"
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
	if !ok || stream.URL == "" {
		return
	}
	key := fmt.Sprintf("%d|%s", id, stream.URL)
	if key == st.sharedKey {
		return
	}
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
	st.sharedKey = key
	client := st.client
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
	a.togetherUseStream(shared)
	return true
}

// togetherUseStream records the host's link and, if the room's track failed
// here for want of permission, plays it again now.
func (a *app) togetherUseStream(shared togetherSharedStream) {
	player.ShareStream(shared.ID, qqmusic.Stream{
		URL:   shared.URL,
		Type:  shared.Type,
		Size:  shared.Size,
		Level: shared.Level,
	})
	st := &a.together
	if a.pl == nil || st.playback == nil || st.playback.Track == nil || st.playback.Track.ID != shared.ID {
		return
	}
	if cur := a.pl.Current(); cur == nil || cur.ID != shared.ID {
		return
	}
	if !playbackNeedsPermission(a.pl.Err()) {
		return
	}
	a.pl.ClearErr()
	a.pl.PlayQueue([]qqmusic.Track{qqmusicTrack(*st.playback.Track)}, 0)
	a.redraw()
}
