// Command together-probe exercises a room server end to end: it creates a
// room, joins as host and guest, and checks sync, chat and song requests.
// It is a development check, not part of the app.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"

	"lilt/internal/together"
)

func main() {
	relay := flag.String("relay", "http://127.0.0.1:8787", "room server base URL")
	join := flag.String("join", "", "join this room as a guest instead of running the whole check")
	stale := flag.Bool("stale", false, "check that a member whose pings stop is swept from the room")
	stay := flag.Bool("stay", false, "host a room with one track for a while, for a guest app to follow")
	stayTrack := flag.Int64("track", 102296994, "track ID the staying host publishes")
	hostgone := flag.Bool("hostgone", false, "check that a room ends two minutes after its host drops")
	full := flag.Bool("full", false, "check that the eleventh member is refused")
	control := flag.String("control", "", "join this room and seek it to 30 seconds, paused")
	chatText := flag.String("chat", "", "with -join, send this chat message")
	soak := flag.Int("soak", 0, "host and follow a room for this many minutes")
	flag.Parse()

	if *soak > 0 {
		soakCheck(*relay, *soak)
		return
	}

	if *control != "" {
		controlRoom(*relay, *control)
		return
	}

	if *stale {
		staleCheck(*relay)
		return
	}
	if *stay {
		stayHost(*relay, *stayTrack)
		return
	}
	if *hostgone {
		hostGoneCheck(*relay)
		return
	}
	if *full {
		fullCheck(*relay)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if *join != "" {
		events := make(chan together.Event, 64)
		guest := together.New(together.Options{
			Relay: *relay, Code: *join, ClientID: "probe-join", Name: "探针访客",
			Avatar:  "https://thirdqq.qlogo.cn/g?b=qq&nk=10003&s=160",
			OnEvent: func(ev together.Event) { events <- ev },
		})
		go guest.Run(ctx)
		welcome := waitKind(ctx, events, together.EventWelcome)
		fmt.Println("joined", welcome.Welcome.Code, "members", len(welcome.Welcome.Members))
		if welcome.Welcome.Playback != nil && welcome.Welcome.Playback.Track != nil {
			fmt.Println("host track", welcome.Welcome.Playback.Track.Name, welcome.Welcome.Playback.PositionMs)
		}
		if *chatText != "" {
			if err := guest.Chat(*chatText); err != nil {
				fail("chat", err)
			}
			fmt.Println("said", *chatText)
		}
		time.Sleep(2 * time.Second)
		fmt.Println("JOIN PASS")
		return
	}

	room, err := together.CreateRoom(ctx, *relay, together.DefaultHTTPClient())
	if err != nil {
		fail("create room", err)
	}
	fmt.Println("room", room.Code)

	hostAvatar := "https://thirdqq.qlogo.cn/g?b=qq&nk=10001&s=160"
	hostEvents := make(chan together.Event, 64)
	host := together.New(together.Options{
		Relay: *relay, Code: room.Code, HostKey: room.HostKey, ClientID: "probe-host", Name: "房主", Avatar: hostAvatar,
		OnEvent: func(ev together.Event) { hostEvents <- ev },
	})
	go host.Run(ctx)
	welcome := waitKind(ctx, hostEvents, together.EventWelcome)
	if welcome.Welcome == nil || !welcome.Welcome.You.Host {
		fail("host welcome", fmt.Errorf("not host: %+v", welcome))
	}

	guestAvatar := "https://thirdqq.qlogo.cn/g?b=qq&nk=10002&s=160"
	guestEvents := make(chan together.Event, 64)
	guest := together.New(together.Options{
		Relay: *relay, Code: room.Code, ClientID: "probe-guest", Name: "访客", Avatar: guestAvatar,
		OnEvent: func(ev together.Event) { guestEvents <- ev },
	})
	go guest.Run(ctx)
	guestWelcome := waitKind(ctx, guestEvents, together.EventWelcome)
	if got := memberAvatar(guestWelcome.Welcome.Members, "probe-host"); got != hostAvatar {
		fail("host avatar", fmt.Errorf("got %q", got))
	}
	if !slices.Contains(guestWelcome.Welcome.Features, "desk") {
		fail("features", fmt.Errorf("the relay does not announce the desk: %v", guestWelcome.Welcome.Features))
	}

	track := together.Track{ID: 123456, MID: "probe", Name: "测试歌曲", Duration: 200_000, Artists: []together.Artist{{Name: "歌手"}}}
	if err := host.Publish(together.Playback{Track: &track, Playing: true, PositionMs: 3000}); err != nil {
		fail("host publish", err)
	}
	ev := waitKind(ctx, guestEvents, together.EventPlayback)
	if ev.Playback == nil || ev.Playback.Track == nil || ev.Playback.Track.ID != track.ID {
		fail("guest playback", fmt.Errorf("%+v", ev.Playback))
	}
	if ev.Playback.UpdatedAt == 0 {
		fail("guest playback", fmt.Errorf("no server time"))
	}
	fmt.Println("sync ok")

	if err := guest.Publish(together.Playback{Track: &track, Playing: false, PositionMs: 10_000}); err != nil {
		fail("guest publish", err)
	}
	waitPlayback(ctx, hostEvents, false)
	fmt.Println("shared control ok")

	if err := guest.Request("next"); err != nil {
		fail("guest request", err)
	}
	request := waitKind(ctx, hostEvents, together.EventRequest)
	if request.Action != "next" {
		fail("host request", fmt.Errorf("unexpected action %q", request.Action))
	}
	fmt.Println("request ok")

	if err := guest.Chat("你好"); err != nil {
		fail("guest chat", err)
	}
	chat := waitKind(ctx, hostEvents, together.EventChat)
	if chat.Chat == nil || chat.Chat.Text != "你好" {
		fail("host chat", fmt.Errorf("%+v", chat.Chat))
	}
	if chat.Chat.Avatar != guestAvatar {
		fail("chat avatar", fmt.Errorf("got %q", chat.Chat.Avatar))
	}
	fmt.Println("chat ok")

	// A QQ Music session can restore after the room was joined: the name
	// and portrait a member shows follow it.
	const renamedAvatar = "https://thirdqq.qlogo.cn/g?b=qq&nk=10002&s=320"
	if err := guest.SetProfile("访客改名", renamedAvatar); err != nil {
		fail("guest profile", err)
	}
	renamed := waitMemberName(ctx, hostEvents, "probe-guest", "访客改名")
	if renamed.Name != "访客改名" || renamed.Avatar != renamedAvatar {
		fail("profile", fmt.Errorf("got %+v", renamed))
	}
	fmt.Println("profile ok")

	// A member who cannot resolve the room's track asks the room for the
	// link, and whoever can answers.
	if err := guest.RequestStream(track.ID); err != nil {
		fail("guest stream request", err)
	}
	asked := waitKind(ctx, hostEvents, together.EventStreamRequest)
	if asked.TrackID != track.ID {
		fail("stream request", fmt.Errorf("asked for %d, want %d", asked.TrackID, track.ID))
	}
	fmt.Println("stream request ok")

	if err := guest.AddPending(track); err != nil {
		fail("guest pending", err)
	}
	pending := waitKind(ctx, hostEvents, together.EventPending)
	if len(pending.Pending) != 1 {
		fail("host pending", fmt.Errorf("%+v", pending.Pending))
	}
	// Any member may start a requested song; the host's queue takes it.
	if err := guest.PlayPending(pending.Pending[0].ID); err != nil {
		fail("guest play pending", err)
	}
	played := waitKind(ctx, hostEvents, together.EventPlay)
	if played.Track == nil || played.Track.ID != track.ID {
		fail("host play", fmt.Errorf("%+v", played.Track))
	}
	after := waitPendingEmpty(ctx, guestEvents)
	if len(after.Pending) != 0 {
		fail("guest pending cleared", fmt.Errorf("%+v", after.Pending))
	}
	fmt.Println("pending ok")

	extraEvents := make(chan together.Event, 16)
	extra := together.New(together.Options{
		Relay: *relay, Code: room.Code, ClientID: "probe-extra", Name: "第三人",
		OnEvent: func(ev together.Event) { extraEvents <- ev },
	})
	go extra.Run(ctx)
	waitKind(ctx, extraEvents, together.EventWelcome)
	time.Sleep(200 * time.Millisecond)
	if err := host.Kick("probe-extra"); err != nil {
		fail("host kick", err)
	}
	kicked := waitKind(ctx, extraEvents, together.EventStatus)
	if !errors.Is(kicked.Err, together.ErrKicked) {
		fail("kicked member", fmt.Errorf("unexpected event: %+v", kicked))
	}
	waitMemberGone(ctx, hostEvents, "probe-extra")
	fmt.Println("kick ok")

	badCtx, badCancel := context.WithCancel(ctx)
	badEvents := make(chan together.Event, 16)
	bad := together.New(together.Options{
		Relay: *relay, Code: room.Code, ClientID: "probe-bad", Name: "坏头像",
		Avatar:  "https://evil.example/a.jpg",
		OnEvent: func(ev together.Event) { badEvents <- ev },
	})
	go bad.Run(badCtx)
	waitKind(ctx, badEvents, together.EventWelcome)
	if got := waitMember(ctx, hostEvents, "probe-bad").Avatar; got != "" {
		fail("avatar sanitize", fmt.Errorf("a third-party avatar URL survived: %q", got))
	}
	_ = bad.Leave()
	badCancel()
	waitMemberGone(ctx, hostEvents, "probe-bad")
	fmt.Println("avatar ok")

	if err := host.End(); err != nil {
		fail("host end", err)
	}
	ended := waitStatus(ctx, guestEvents, together.ErrEnded)
	if !errors.Is(ended.Err, together.ErrEnded) {
		fail("room ended", fmt.Errorf("unexpected event: %+v", ended))
	}
	fmt.Println("end ok")

	cancel()
	fmt.Println("PASS")
}

func waitStatus(ctx context.Context, events <-chan together.Event, target error) together.Event {
	for {
		select {
		case ev := <-events:
			if ev.Kind == together.EventStatus && errors.Is(ev.Err, target) {
				return ev
			}
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "timed out waiting for %v\n", target)
			os.Exit(1)
		}
	}
}

// staleCheck joins a raw socket that never pings, as a crashed app would
// leave behind, and waits for the room's sweep to drop it.
func staleCheck(relay string) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	room, err := together.CreateRoom(ctx, relay, together.DefaultHTTPClient())
	if err != nil {
		fail("create room", err)
	}
	fmt.Println("room", room.Code)

	hostEvents := make(chan together.Event, 64)
	host := together.New(together.Options{
		Relay: relay, Code: room.Code, HostKey: room.HostKey, ClientID: "probe-stale-host", Name: "房主",
		OnEvent: func(ev together.Event) { hostEvents <- ev },
	})
	go host.Run(ctx)
	waitKind(ctx, hostEvents, together.EventWelcome)

	base := strings.TrimSuffix(strings.Replace(strings.Replace(relay, "https://", "wss://", 1), "http://", "ws://", 1), "/")
	raw := fmt.Sprintf("%s/ws?code=%s&client=probe-stale&name=%s", base, room.Code, url.QueryEscape("幽灵"))
	conn, _, err := websocket.Dial(ctx, raw, &websocket.DialOptions{HTTPClient: together.DefaultHTTPClient()})
	if err != nil {
		fail("dial stale client", err)
	}
	defer conn.CloseNow()

	waitMember(ctx, hostEvents, "probe-stale")
	fmt.Println("stale member listed; waiting for the sweep")
	waitMemberGone(ctx, hostEvents, "probe-stale")
	fmt.Println("STALE PASS")
	_ = host.End()
}

// stayHost keeps a room open with one track, so a guest app can be tested
// against it: the guest's own account decides whether it may play.
func stayHost(relay string, id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()

	room, err := together.CreateRoom(ctx, relay, together.DefaultHTTPClient())
	if err != nil {
		fail("create room", err)
	}
	events := make(chan together.Event, 16)
	host := together.New(together.Options{
		Relay: relay, Code: room.Code, HostKey: room.HostKey, ClientID: "probe-stay-host", Name: "房主",
		OnEvent: func(ev together.Event) { events <- ev },
	})
	go host.Run(ctx)
	waitKind(ctx, events, together.EventWelcome)

	t := together.Track{ID: id, Name: fmt.Sprintf("Track %d", id), Duration: 269_000}
	if err := host.Publish(together.Playback{Track: &t, Playing: true, PositionMs: 0}); err != nil {
		fail("publish", err)
	}
	fmt.Println("STAY_ROOM", room.Code)
	time.Sleep(45 * time.Second)
	_ = host.End()
	fmt.Println("STAY_END")
}

func waitPlayback(ctx context.Context, events <-chan together.Event, playing bool) together.Event {
	for {
		select {
		case ev := <-events:
			if ev.Kind == together.EventPlayback && ev.Playback != nil && ev.Playback.Playing == playing {
				return ev
			}
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "timed out waiting for playback playing=%v\n", playing)
			os.Exit(1)
		}
	}
}

// hostGoneCheck leaves a guest in a room whose host drops without ending
// it, and waits for the grace period to end the room.
func hostGoneCheck(relay string) {
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	room, err := together.CreateRoom(ctx, relay, together.DefaultHTTPClient())
	if err != nil {
		fail("create room", err)
	}
	hostCtx, dropHost := context.WithCancel(ctx)
	hostEvents := make(chan together.Event, 32)
	host := together.New(together.Options{
		Relay: relay, Code: room.Code, HostKey: room.HostKey, ClientID: "probe-gone-host", Name: "房主",
		OnEvent: func(ev together.Event) { hostEvents <- ev },
	})
	go host.Run(hostCtx)
	waitKind(ctx, hostEvents, together.EventWelcome)

	guestEvents := make(chan together.Event, 32)
	guest := together.New(together.Options{
		Relay: relay, Code: room.Code, ClientID: "probe-gone-guest", Name: "访客",
		OnEvent: func(ev together.Event) { guestEvents <- ev },
	})
	go guest.Run(ctx)
	waitKind(ctx, guestEvents, together.EventWelcome)

	fmt.Println("HOST_DROPS", room.Code)
	dropHost()
	ended := waitStatus(ctx, guestEvents, together.ErrEnded)
	if ended.Err == nil {
		fail("host gone", fmt.Errorf("no end"))
	}
	fmt.Println("HOST_GONE_PASS")
}

// fullCheck fills a room to its ten members and checks the eleventh is
// refused with the room-full error, not a reconnect loop.
func fullCheck(relay string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	room, err := together.CreateRoom(ctx, relay, together.DefaultHTTPClient())
	if err != nil {
		fail("create room", err)
	}
	events := make(chan together.Event, 256)
	var host *together.Client
	for i := 0; i < 10; i++ {
		opts := together.Options{
			Relay: relay, Code: room.Code, ClientID: fmt.Sprintf("probe-full-%d", i), Name: fmt.Sprintf("第%d人", i+1),
			OnEvent: func(ev together.Event) { events <- ev },
		}
		if i == 0 {
			opts.HostKey = room.HostKey
		}
		c := together.New(opts)
		if i == 0 {
			host = c
		}
		go c.Run(ctx)
	}
	for i := 0; i < 10; i++ {
		waitKind(ctx, events, together.EventWelcome)
	}

	extraEvents := make(chan together.Event, 8)
	extra := together.New(together.Options{
		Relay: relay, Code: room.Code, ClientID: "probe-full-extra", Name: "第十一人",
		OnEvent: func(ev together.Event) { extraEvents <- ev },
	})
	go extra.Run(ctx)
	waitStatus(ctx, extraEvents, together.ErrRoomFull)
	fmt.Println("FULL_PASS")
	_ = host.End()
}

// controlRoom joins as a guest and asks the room to hold at 30 seconds,
// paused: a real app hosting it should follow.
func controlRoom(relay, code string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	events := make(chan together.Event, 32)
	guest := together.New(together.Options{
		Relay: relay, Code: code, ClientID: "probe-control", Name: "控制访客",
		OnEvent: func(ev together.Event) { events <- ev },
	})
	go guest.Run(ctx)
	welcome := waitKind(ctx, events, together.EventWelcome)
	if welcome.Welcome == nil || welcome.Welcome.Playback == nil || welcome.Welcome.Playback.Track == nil {
		fail("control welcome", fmt.Errorf("no host playback yet"))
	}
	pb := *welcome.Welcome.Playback
	pb.Playing = false
	pb.PositionMs = 30_000
	pb.UpdatedAt, pb.Rev = 0, 0
	if err := guest.Publish(pb); err != nil {
		fail("control publish", err)
	}
	time.Sleep(2 * time.Second)
	fmt.Println("CONTROL_PASS")
}

// soakCheck keeps a host and a guest together for minutes, with an anchor
// every five seconds, to catch dropped sockets, clock drift and stale rooms.
func soakCheck(relay string, minutes int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(minutes)*time.Minute+time.Minute)
	defer cancel()

	room, err := together.CreateRoom(ctx, relay, together.DefaultHTTPClient())
	if err != nil {
		fail("create room", err)
	}
	hostEvents := make(chan together.Event, 256)
	guestEvents := make(chan together.Event, 256)
	host := together.New(together.Options{
		Relay: relay, Code: room.Code, HostKey: room.HostKey, ClientID: "soak-host", Name: "房主",
		OnEvent: func(ev together.Event) { hostEvents <- ev },
	})
	guest := together.New(together.Options{
		Relay: relay, Code: room.Code, ClientID: "soak-guest", Name: "访客",
		OnEvent: func(ev together.Event) { guestEvents <- ev },
	})
	go host.Run(ctx)
	go guest.Run(ctx)
	waitKind(ctx, hostEvents, together.EventWelcome)
	waitKind(ctx, guestEvents, together.EventWelcome)

	track := together.Track{ID: 102296994, Name: "soak", Duration: 269_000}
	start := time.Now()
	for i := 0; time.Since(start) < time.Duration(minutes)*time.Minute; i++ {
		pb := together.Playback{Track: &track, Playing: true, PositionMs: time.Since(start).Milliseconds()}
		if err := host.Publish(pb); err != nil {
			fmt.Println("SOAK publish", err)
			time.Sleep(5 * time.Second)
			continue
		}
		waitPlayback(ctx, guestEvents, true)
		if i%12 == 0 {
			fmt.Println("SOAK", time.Since(start).Round(time.Second), "host", host.Online(), "guest", guest.Online(), "skew_ms", guest.ServerNow()-time.Now().UnixMilli())
		}
		time.Sleep(5 * time.Second)
	}
	if err := guest.Chat("soak chat"); err != nil {
		fail("soak chat", err)
	}
	waitKind(ctx, hostEvents, together.EventChat)
	fmt.Println("SOAK_PASS")
	_ = host.End()
}

func waitMember(ctx context.Context, events <-chan together.Event, client string) together.Member {
	for {
		select {
		case ev := <-events:
			if ev.Kind != together.EventMembers {
				continue
			}
			for _, m := range ev.Members {
				if m.Client == client {
					return m
				}
			}
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "timed out waiting for %s in the member list\n", client)
			os.Exit(1)
		}
	}
}

func waitMemberGone(ctx context.Context, events <-chan together.Event, client string) {
	for {
		select {
		case ev := <-events:
			if ev.Kind != together.EventMembers {
				continue
			}
			gone := true
			for _, m := range ev.Members {
				if m.Client == client {
					gone = false
				}
			}
			if gone {
				return
			}
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "timed out waiting for %s to leave the member list\n", client)
			os.Exit(1)
		}
	}
}

// waitMemberName waits for the member list to show client under name, as
// after a profile update.
func waitMemberName(ctx context.Context, events <-chan together.Event, client, name string) together.Member {
	for {
		select {
		case ev := <-events:
			if ev.Kind != together.EventMembers {
				continue
			}
			for _, m := range ev.Members {
				if m.Client == client && m.Name == name {
					return m
				}
			}
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "timed out waiting for %s to be named %s\n", client, name)
			os.Exit(1)
		}
	}
}

func memberAvatar(members []together.Member, client string) string {
	for _, m := range members {
		if m.Client == client {
			return m.Avatar
		}
	}
	return ""
}

func waitPendingEmpty(ctx context.Context, events <-chan together.Event) together.Event {
	for {
		select {
		case ev := <-events:
			if ev.Kind == together.EventPending && len(ev.Pending) == 0 {
				return ev
			}
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "timed out waiting for an empty song request list")
			os.Exit(1)
		}
	}
}

func waitKind(ctx context.Context, events <-chan together.Event, kind string) together.Event {
	for {
		select {
		case ev := <-events:
			if ev.Kind == kind {
				return ev
			}
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "timed out waiting for %s\n", kind)
			os.Exit(1)
		}
	}
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}
