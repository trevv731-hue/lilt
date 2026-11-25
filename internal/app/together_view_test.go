package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/audio"
	"github.com/egoist/mygo/ui"

	"lilt/internal/qqmusic"
	"lilt/internal/together"
)

func togetherTestApp() *app {
	a := newTestApp()
	a.route = route{page: pageTogether}
	a.settings.TogetherName = "我"
	return a
}

func togetherRoomApp() *app {
	a := togetherTestApp()
	a.together = togetherState{
		joined:   true,
		host:     true,
		code:     "ABCD2345",
		clientID: "host",
		selfID:   "host",
		status:   together.StatusOnline,
		members:  []together.Member{{Client: "host", Name: "小右", Host: true}, {Client: "guest", Name: "朋友"}},
		playback: &together.Playback{Track: &together.Track{ID: 1, Name: "晴天", Artists: []together.Artist{{Name: "周杰伦"}}, Album: together.Album{Name: "叶惠美"}, Duration: 269_000}, Playing: true, PositionMs: 42_000},
		pending:  []together.Pending{{ID: "p1", Track: together.Track{ID: 2, Name: "稻香", Artists: []together.Artist{{Name: "周杰伦"}}, Duration: 200_000}, By: "guest", ByName: "朋友"}},
		chat:     []together.Chat{{ID: "c1", Client: "guest", Name: "朋友", Text: "这首好听", At: 1_700_000_000_000}},
	}
	a.together.chatList.FollowEnd = true
	return a
}

func TestTogetherPageShowsCreateAndJoin(t *testing.T) {
	a := togetherTestApp()
	tt := ui.NewTester(a.view, 1180, 780)
	tt.Frame()
	for _, want := range []string{"一起听", "创建房间", "加入房间"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("the entry page should show %q", want)
		}
	}
}

func TestTogetherRoomWarnsGuestWithoutPermission(t *testing.T) {
	a := togetherRoomApp()
	a.together.host = false
	a.together.clientID = "guest"
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "晴天", Duration: 269_000}})
	a.pl.SetErr("这首歌需要 VIP 才能播放")
	tt := ui.NewTester(a.view, 1180, 780)
	tt.Frame()
	if !tt.HasText("可能需要 VIP") {
		t.Fatal("a guest without permission should be told why the song is silent")
	}
}

func TestPlaybackNeedsPermission(t *testing.T) {
	for _, err := range []string{"这首歌需要 VIP 才能播放", "这期节目需要购买才能收听", "没有权限"} {
		if !playbackNeedsPermission(err) {
			t.Fatalf("%q should count as a permission problem", err)
		}
	}
	for _, err := range []string{"", "无法连接 QQ 音乐，请检查网络"} {
		if playbackNeedsPermission(err) {
			t.Fatalf("%q is not a permission problem", err)
		}
	}
}

func TestTogetherRoomShowsControlsToGuest(t *testing.T) {
	a := togetherRoomApp()
	a.together.host = false
	tt := ui.NewTester(a.view, 1180, 780)
	tt.Frame()
	for _, want := range []string{"播放/暂停", "下一首"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("a guest should see the %q control", want)
		}
	}
}

func TestTogetherPageShowsRoom(t *testing.T) {
	a := togetherTestApp()
	a.together = togetherState{
		joined:    true,
		host:      true,
		code:      "ABCD2345",
		clientID:  "host",
		selfID:    "host",
		status:    together.StatusOnline,
		members:   []together.Member{{Client: "host", Name: "小右", Host: true}, {Client: "guest", Name: "朋友"}},
		playback:  &together.Playback{Track: &together.Track{ID: 1, Name: "晴天", Artists: []together.Artist{{Name: "周杰伦"}}, Album: together.Album{Name: "叶惠美"}, Duration: 269_000}, Playing: true, PositionMs: 42_000},
		pending:   []together.Pending{{ID: "p1", Track: together.Track{ID: 2, Name: "稻香", Artists: []together.Artist{{Name: "周杰伦"}}, Duration: 200_000}, By: "guest", ByName: "朋友"}},
		chat:      []together.Chat{{ID: "c1", Client: "guest", Name: "朋友", Text: "这首好听", At: 1_700_000_000_000}},
		chatDraft: "",
	}
	tt := ui.NewTester(a.view, 1180, 780)
	tt.Frame()
	for _, want := range []string{"ABCD2345", "复制邀请", "小右", "朋友", "晴天", "点歌台", "稻香", "这首好听"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("the room should show %q", want)
		}
	}
}

func TestTogetherGuestKeepsQueueNotTransport(t *testing.T) {
	a := togetherTestApp()
	a.together.joined = true
	if !a.togetherControlsLocked() {
		t.Fatal("a guest's song choice should be locked")
	}
	if a.together.note == "" {
		t.Fatal("locking should say why")
	}
	if a.toast != "" {
		t.Fatalf("the room says it on its own line, not in a popup: toast = %q", a.toast)
	}
	a.together.applying = true
	if a.togetherControlsLocked() {
		t.Fatal("following the room should be allowed")
	}
	a.together.applying = false
	a.together.host = true
	if a.togetherControlsLocked() {
		t.Fatal("the host chooses songs")
	}
}

func TestTogetherGuestTransport(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "one", Duration: 200_000}})
	a.together.joined = true
	a.together.client = together.New(together.Options{ClientID: "guest"})

	a.togetherPlayPause()
	if !a.together.userActed {
		t.Fatal("a guest's play or pause should reach the room")
	}
	a.together.userActed = false
	a.togetherSeek()
	if !a.together.userActed {
		t.Fatal("a guest's seek should reach the room")
	}
	a.together.userActed = false
	if !a.togetherNextRequest() {
		t.Fatal("a guest's next should be handed to the host")
	}
	if a.together.userActed {
		t.Fatal("a next request is not a local transport change")
	}

	a.together.host = true
	if a.togetherNextRequest() {
		t.Fatal("the host advances its own queue")
	}
	a.together.userActed = false
	a.togetherPlayPause()
	if a.together.userActed {
		t.Fatal("the host publishes through its player, not the guest flag")
	}
}

func TestTogetherIdentityUsesQQMusicProfile(t *testing.T) {
	a := newTestApp()
	a.settings.TogetherName = "本地昵称"
	a.profile = &qqmusic.Profile{Nickname: "QQ昵称", AvatarURL: "https://thirdqq.qlogo.cn/avatar"}
	name, avatar := a.togetherIdentity()
	if name != "QQ昵称" {
		t.Fatalf("the room should use the QQ Music nickname, got %q", name)
	}
	if avatar != "https://thirdqq.qlogo.cn/avatar" {
		t.Fatalf("the room should use the QQ Music avatar, got %q", avatar)
	}
	a.profile = nil
	name, avatar = a.togetherIdentity()
	if name != "本地昵称" || avatar != "" {
		t.Fatalf("signed out should fall back to the local nickname, got %q %q", name, avatar)
	}
}

func TestTogetherGuestFollowsSeekAndPause(t *testing.T) {
	a := newTestApp()
	a.pl.LockControls = a.togetherControlsLocked
	a.together.joined, a.together.host = true, false
	a.together.client = together.New(together.Options{ClientID: "guest"})
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "测试歌曲", Duration: 200_000}})
	out := audio.NewPlayer(&silence{})
	defer out.Close()
	out.Seek(5 * time.Second)
	a.pl.Attach(out)

	now := time.Now()
	a.together.playback = &together.Playback{
		Track:      &together.Track{ID: 1, Name: "测试歌曲", Duration: 200_000},
		Playing:    true,
		PositionMs: 10_000,
		UpdatedAt:  now.UnixMilli(),
	}
	a.togetherFollow(now)
	if pos := a.pl.Position(); pos < 9*time.Second || pos > 11*time.Second {
		t.Fatalf("the guest should seek to 10s, got %v", pos)
	}

	a.together.playback.Playing = false
	a.together.playback.UpdatedAt = time.Now().UnixMilli()
	a.togetherFollow(time.Now())
	if a.pl.Playing() {
		t.Fatal("the guest should follow the host's pause")
	}
}

func TestRoomCodeFromInvite(t *testing.T) {
	if got := roomCodeFrom("来 Lilt 一起听歌，房间号 ABCD2345（打开「一起听」输入房间号加入）"); got != "ABCD2345" {
		t.Fatalf("should find the code in the invitation, got %q", got)
	}
	if got := roomCodeFrom(" abcd2345 "); got != "ABCD2345" {
		t.Fatalf("should accept a pasted code in lower case, got %q", got)
	}
	if got := roomCodeFrom("没有房间号"); got != "" {
		t.Fatalf("should not invent a code, got %q", got)
	}
}

func TestTogetherPageInDarkAppearance(t *testing.T) {
	a := togetherRoomApp()
	tt := ui.NewTesterWith(a.view, 1180, 780, ui.TesterOptions{Dark: true, Preferences: ui.Preferences{ReduceMotion: true}})
	tt.Frame()
	for _, want := range []string{"ABCD2345", "复制邀请", "点歌台", "聊天"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("the dark room page should show %q", want)
		}
	}
}

func TestTogetherAtMinimumWindow(t *testing.T) {
	a := togetherTestApp()
	tt := ui.NewTester(a.view, 880, 580)
	tt.Frame()
	for _, want := range []string{"创建房间", "加入房间"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("the entry page should fit %q at the minimum window", want)
		}
	}

	a = togetherRoomApp()
	tt = ui.NewTester(a.view, 880, 580)
	tt.Frame()
	for _, want := range []string{"复制邀请", "点歌台", "聊天"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("the room page should fit %q at the minimum window", want)
		}
	}
}

func TestValidRoomCode(t *testing.T) {
	for _, code := range []string{"ABCD2345", "ZZZZ9999"} {
		if !validRoomCode(code) {
			t.Fatalf("%q should be valid", code)
		}
	}
	for _, code := range []string{"", "ABCD234", "ABCD23455", "abcd2345", "ABCDO234"} {
		if validRoomCode(code) {
			t.Fatalf("%q should be invalid", code)
		}
	}
}

func TestTogetherIdentityUsesQQMusicName(t *testing.T) {
	a := togetherTestApp()
	a.profile = &qqmusic.Profile{UserID: 1, Nickname: "小右", AvatarURL: "https://thirdqq.qlogo.cn/g?b=qq&nk=1&s=160"}
	a.settings.TogetherName = "我"
	name, avatar := a.togetherIdentity()
	if name != "小右" || avatar != a.profile.AvatarURL {
		t.Fatalf("identity = %q %q, want the QQ Music account's", name, avatar)
	}
}

func TestTogetherLocalNameIsNotShared(t *testing.T) {
	a := togetherTestApp()
	a.settings.TogetherName = ""
	a.settings.TogetherClient = "aaaaaaaa"
	b := togetherTestApp()
	b.settings.TogetherName = ""
	b.settings.TogetherClient = "bbbbbbbb"
	if a.togetherLocalName() == b.togetherLocalName() {
		t.Fatalf("two listeners without accounts share the name %q", a.togetherLocalName())
	}
	if a.togetherLocalName() == "我" {
		t.Fatal("a listener without an account should not be named 我")
	}
}

// The room shows the host's queue, mirrored: the guest sees the same list
// and which of it the room is playing.
func TestTogetherRoomShowsHostQueue(t *testing.T) {
	a := togetherRoomApp()
	a.together.deskTab = 1
	a.together.queue = &together.Queue{
		Tracks: []together.Track{
			{ID: 1, Name: "晴天", Artists: []together.Artist{{Name: "周杰伦"}}},
			{ID: 2, Name: "稻香", Artists: []together.Artist{{Name: "周杰伦"}}},
		},
		Index: 0,
	}
	tt := ui.NewTester(a.view, 1180, 780)
	tt.Frame()
	for _, want := range []string{"队列", "稻香", "与房主同一个队列"} {
		if !tt.HasText(want) {
			t.Fatalf("the room's queue tab should show %q", want)
		}
	}
}

// The room says whether this client is where the room is.
func TestTogetherRoomShowsSyncState(t *testing.T) {
	a := togetherRoomApp()
	a.together.client = together.New(together.Options{Relay: "ws://127.0.0.1:1", Code: "ABCD2345", ClientID: "guest"})
	a.together.playback = &together.Playback{
		Track:      a.together.playback.Track,
		Playing:    false,
		PositionMs: 0,
		UpdatedAt:  time.Now().UnixMilli(),
	}
	a.pl.Cue([]qqmusic.Track{{ID: 1, Name: "晴天", Duration: 269_000}})
	tt := ui.NewTester(a.view, 1180, 780)
	tt.Frame()
	if !tt.HasText("已同步") {
		t.Fatal("a paused client at the room's position is in sync")
	}
}
