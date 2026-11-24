package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"lilt/internal/lyrics"
	"lilt/internal/qqmusic"
	"lilt/internal/together"
)

// The room borrows QQ 音乐电脑版「一起听」的样子: a mint canvas, the record
// turning on its sleeve at the left, the song and its words at the right,
// and the room's people along the top. The song request desk and the chat
// keep their own cards, below or beside, because they are what this client
// adds to that page.

// togetherPage is the room, or the way into one: before joining, a card to
// create a room and one to join with a room code.
func (a *app) togetherPage(c *ui.Context) {
	top, bottom := a.insets(c)
	if a.together.joined {
		a.togetherRoom(c, top, bottom)
		return
	}
	ui.Scroll(c.Key("together-page")).TrackScroll(&a.pageState().scroll).Grow(1).
		Padding(top+pageGap, pagePad, bottom+48, pagePad).Children(func() {
		a.startMark(c)
		defer a.endMark(c)
		a.heading(c, "一起听", "两个人，同一首歌")
		a.togetherEntry(c)
	})
}

func (a *app) togetherEntry(c *ui.Context) {
	p := a.pal
	st := &a.together
	ui.Row(c).Gap(16).AlignItems(ui.Stretch).Children(func() {
		a.togetherEntryCard(c, "创建房间", "开一间房，把房间号发给朋友", iconPlus, func() {
			label := "创建房间"
			if st.busy {
				label = "创建中…"
			}
			if pillButton(c.Key("together-create"), iconTogether, label, p.accent, p.onAccent).Disabled(st.busy).Clicked() {
				a.togetherCreate()
			}
		})

		ui.Column(c).Grow(1).Basis(0).Padding(22).Gap(12).Radius(22).Background(p.sidebar).Border(1, p.hairline).Children(func() {
			ui.Box(c).Size(40, 40).Radius(12).Center().Background(p.accent.Alpha(0.14)).Children(func() {
				ui.Icon(c, iconUser).Size(18, 18).TextColor(p.accent)
			})
			ui.Text(c, "加入房间").FontSize(20).FontWeight(700)
			ui.Text(c, "粘贴房间号，或朋友发来的整段邀请").FontSize(12.5).TextColor(p.muted).MaxLines(2)
			code := ui.TextInput(c.Key("together-code"), &st.joinCode).Placeholder("8 位房间号").FontSize(16).Height(36)
			if code.Submitted() {
				a.togetherJoin()
			}
			ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
				if pillButton(c.Key("together-join"), iconForward, "加入", p.hover, p.text).Clicked() {
					a.togetherJoin()
				}
			})
		})
	})

	ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(18, 0, 0, 0).Children(func() {
		ui.Icon(c, iconInfo).Size(14, 14).TextColor(p.muted)
		ui.Text(c, "两边都需新版 Lilt；房间里有一个人是会员，会员歌曲就能一起听").FontSize(12).TextColor(p.muted).Grow(1)
		if a.profile == nil {
			if ui.Button(c.Key("together-login"), "登录 QQ 音乐").Clicked() {
				a.openLogin()
			}
		}
	})

	// The relay, for when the built-in one is unreachable: the same room
	// server can be run by whoever listens.
	ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(10, 0, 0, 0).Children(func() {
		ui.Text(c, "房间服务器").FontSize(11).FontWeight(600).TextColor(p.faint)
		in := ui.TextInput(c.Key("together-relay"), &a.settings.TogetherRelay).Placeholder(together.DefaultRelay).FontSize(11.5).Height(28).Width(320)
		if in.Submitted() {
			a.settings.save()
			a.toast = "房间服务器已保存"
		}
		ui.Text(c, "留空使用默认").FontSize(11).TextColor(p.faint)
	})

	if st.err != "" {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Margin(10, 0, 0, 0).Children(func() {
			ui.Text(c, st.err).FontSize(12.5).TextColor(p.muted).MaxLines(2).Grow(1)
			if pillButton(c.Key("together-retry"), iconTogether, "重试", p.hover, p.text).Clicked() {
				a.togetherCreate()
			}
		})
	}
}

// togetherEntryCard is one of the two cards before joining: an icon, a
// title, a line about it, and what to do.
func (a *app) togetherEntryCard(c *ui.Context, title, sub string, icon *ui.SVG, body func()) {
	p := a.pal
	ui.Column(c).Grow(1).Basis(0).Padding(22).Gap(12).Radius(22).
		LinearGradient(ui.LinearGradient{Angle: 155, Stops: []ui.GradientStop{
			{Color: togetherCardTop(p.dark), At: 0},
			{Color: togetherCardBottom(p.dark), At: 1},
		}}).Border(1, p.hairline).Children(func() {
		ui.Box(c).Size(40, 40).Radius(12).Center().Background(p.accent.Alpha(0.14)).Children(func() {
			ui.Icon(c, icon).Size(18, 18).TextColor(p.accent)
		})
		ui.Text(c, title).FontSize(20).FontWeight(700)
		ui.Text(c, sub).FontSize(12.5).TextColor(p.muted).MaxLines(2)
		if a.profile != nil {
			ui.Row(c).Gap(9).AlignItems(ui.Center).Children(func() {
				a.togetherAvatar(c, a.profile.AvatarURL, a.profile.Nickname, 26)
				ui.Text(c, a.togetherNickname()).FontSize(14).FontWeight(600).SingleLine().MaxWidth(200)
			})
		} else {
			in := ui.TextInput(c.Key("together-name"), &a.settings.TogetherName).Placeholder("昵称").FontSize(14).Height(36)
			if in.Changed() {
				a.settings.save()
			}
		}
		body()
	})
}

// togetherRoom is the room itself, filled with the room's own canvas: the
// record and the song above, the desk and the chat below, or beside them
// on a wide window.
func (a *app) togetherRoom(c *ui.Context, top, bottom float32) {
	w, _ := c.Size()
	wide := w >= 1400
	ui.Column(c).Fill().Background(togetherCanvas(a.pal.dark)).
		Padding(top+12, 28, bottom+14, 28).Gap(14).Children(func() {
		a.togetherTopBar(c)
		if wide {
			ui.Row(c).Grow(1).Gap(18).AlignItems(ui.Stretch).Children(func() {
				a.togetherStage(c)
				ui.Column(c).Width(356).Shrink(0).Gap(14).Children(func() {
					a.togetherRequests(c)
					a.togetherChatCard(c)
				})
			})
			return
		}
		a.togetherStage(c)
		ui.Row(c).Grow(1).Gap(18).AlignItems(ui.Stretch).Children(func() {
			a.togetherRequests(c)
			a.togetherChatCard(c)
		})
	})
}

// togetherTopBar says whose room this is and who is in it, as QQ 音乐 puts
// its listeners along the top of the page.
func (a *app) togetherTopBar(c *ui.Context) {
	p := a.pal
	st := &a.together
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		label := "结束一起听"
		if !st.host {
			label = "离开一起听"
		}
		if pillButton(c.Key("together-leave"), iconSignOut, label, p.hover, p.text).Clicked() {
			a.togetherLeaveRoom()
		}

		if name := st.hostName(); name != "" {
			ui.Text(c, name).FontSize(15).FontWeight(700).SingleLine().MaxWidth(220)
		}

		ui.Row(c).Height(28).PaddingX(10).Gap(8).Radius(999).AlignItems(ui.Center).Background(p.sidebar).Border(1, p.hairline).Children(func() {
			ui.Text(c, "房间号").FontSize(10.5).FontWeight(600).TextColor(p.faint)
			ui.Text(c, st.code).FontSize(12.5).FontWeight(700).LetterSpacing(1.6).TextColor(p.text)
		})

		if len(st.members) > 0 {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				for _, m := range st.members {
					a.togetherMember(c, m)
				}
			})
		}

		color := p.muted
		line := a.togetherStatusLine()
		if st.status == together.StatusOnline && st.err == "" && len(st.members) <= 1 {
			line = "等待好友加入中…"
		}
		if st.status == together.StatusOnline && st.err == "" && len(st.members) > 1 {
			color = p.accent
			line = fmt.Sprintf("已连接 · %d 人在听", len(st.members))
		}
		ui.Text(c, line).FontSize(12).TextColor(color).SingleLine()

		ui.Box(c).Grow(1)
		if pillButton(c.Key("together-copy"), iconCopy, "复制邀请", p.hover, p.text).Clicked() {
			a.togetherCopyInvite()
		}
	})
}

// togetherStage is the record and the song: the heart of the page.
func (a *app) togetherStage(c *ui.Context) {
	p := a.pal
	ui.Box(c).Grow(1).Shrink(1).MinWidth(0).Padding(22).Radius(28).
		LinearGradient(ui.LinearGradient{Angle: 150, Stops: []ui.GradientStop{
			{Color: togetherCardTop(p.dark), At: 0},
			{Color: togetherCardBottom(p.dark), At: 1},
		}}).Border(1, p.hairline).Shadow(0, 18, 40, -20, p.cardShade).Children(func() {
		ui.Row(c).Gap(26).AlignItems(ui.Center).Children(func() {
			a.togetherVinyl(c, togetherVinylSize(c))
			a.togetherTrackInfo(c)
		})
	})
}

// togetherVinylSize is how big the record card is: the window's height has
// to hold it, the song and the two cards under it without a scroll.
func togetherVinylSize(c *ui.Context) float32 {
	w, h := c.Size()
	size := float32(250)
	if h >= 880 {
		size = 300
	}
	if w >= 1600 && h >= 940 {
		size = 330
	}
	return size
}

// togetherVinyl is the sleeve card QQ 音乐 turns: a record with the cover
// as its label, a tonearm resting on it, and a play button at its corner.
func (a *app) togetherVinyl(c *ui.Context, size float32) {
	p := a.pal
	st := &a.together
	var label *ui.Bitmap
	playing := false
	if st.playback != nil && st.playback.Track != nil {
		label = bitmap(a.art.get(qqmusic.Sized(st.playback.Track.Album.PicURL, 400), artCover))
		playing = st.playback.Playing
	}
	card := ui.Box(c).Size(size, size).Shrink(0).Radius(size*0.09).
		Background(togetherSleeve(p.dark)).Shadow(0, 18, 36, -14, p.cardShade)
	card.Children(func() {
		ui.Box(c).Absolute().Left(size*0.08).Top(size*0.08).Size(size*0.84, size*0.84).Radius(size*0.42).
			PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
			spin := &a.togetherSpin
			turn := float64(0)
			if playing {
				turn = 0.5
			}
			if spin.step(pt.Now(), turn) {
				pt.AnimationFrameRate(dailyRate)
			}
			drawRecord(pt, r, label, float32(spin.angle))
		})
		ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough().
			Draw(func(pt *ui.Painter, r ui.Rect) { drawTonearm(pt, r) })
		ui.Box(c).Absolute().Right(size*0.08).Bottom(size*0.08).Size(48, 48).Radius(24).Center().
			Background(togetherSleeve(p.dark)).Shadow(0, 6, 16, -4, p.cardShade).Children(func() {
			if playButton(c.Key("together-vinyl-play"), a.pl.Playing(), 18, p.accent).Clicked() {
				a.togetherTogglePlay()
			}
		})
	})
	if card.Clicked() {
		a.togetherTogglePlay()
	}
}

// togetherTrackInfo is the song, its words, and the room's transport.
func (a *app) togetherTrackInfo(c *ui.Context) {
	p := a.pal
	st := &a.together
	pb := st.playback
	ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(10).Children(func() {
		if pb == nil || pb.Track == nil {
			ui.Text(c, "等待一起听").FontSize(11.5).FontWeight(600).TextColor(p.muted).LetterSpacing(0.4)
			ui.Text(c, "把房间号发给朋友，一起点一首歌").FontSize(26).FontWeight(700).LetterSpacing(-0.6).MaxLines(2)
			ui.Text(c, "房间里的任何成员都能播放、暂停和切歌").FontSize(13).TextColor(p.muted).MaxLines(2)
			return
		}
		tr := qqmusicTrack(*pb.Track)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr.Name).FontSize(29).FontWeight(750).LetterSpacing(-0.8).MaxLines(2)
			if pb.Track.Fee == 1 {
				a.vipBadge(c)
			}
		})
		ui.Text(c, tr.ArtistNames()).FontSize(14).TextColor(p.muted).SingleLine()
		if tr.Album.Name != "" {
			ui.Text(c, tr.Album.Name).FontSize(11.5).TextColor(p.faint).SingleLine()
		}
		a.togetherLyricPreview(c, tr)
		a.togetherTransport(c, tr)
		if !st.host && playbackNeedsPermission(a.pl.Err()) {
			ui.Text(c, "你的账号可能需要 VIP；正在等房间里的会员分享音源").FontSize(11.5).TextColor(p.muted).MaxLines(2)
		}
	})
}

// vipBadge marks a song QQ 音乐 keeps for members.
func (a *app) vipBadge(c *ui.Context) {
	p := a.pal
	ui.Text(c, "VIP").FontSize(10).FontWeight(800).LetterSpacing(0.4).TextColor(p.accent).
		PaddingX(7).Height(18).Radius(999).Center().Background(p.accent.Alpha(0.14))
}

// togetherLyricPreview shows the line being sung and its neighbours, or,
// without words, the line QQ 音乐 puts under a song it has none for. The
// words are the player's, so they only show while it plays the room's own
// track.
func (a *app) togetherLyricPreview(c *ui.Context, tr qqmusic.Track) {
	p := a.pal
	cur := a.pl.Current()
	if cur == nil || cur.ID != tr.ID {
		ui.Text(c, "QQ音乐 听我想听").FontSize(20).FontWeight(600).TextColor(p.faint).Margin(4, 0, 4, 0)
		return
	}
	lines, ok := a.pl.Lyrics()
	if !ok || len(lines) == 0 {
		ui.Text(c, "QQ音乐 听我想听").FontSize(20).FontWeight(600).TextColor(p.faint).Margin(4, 0, 4, 0)
		return
	}
	pos := a.pl.Position()
	at := lyrics.Index(lines, pos)
	ui.Column(c).Gap(5).Margin(4, 0, 4, 0).Children(func() {
		for i := at - 1; i <= at+1; i++ {
			if i < 0 || i >= len(lines) || strings.TrimSpace(lines[i].Text) == "" {
				continue
			}
			line := ui.Text(c, lines[i].Text).FontSize(13).SingleLine()
			if i == at {
				line.FontSize(15).FontWeight(700).TextColor(p.text)
			} else {
				line.TextColor(p.faint)
			}
		}
	})
}

// togetherTransport is the room's play, pause and skip: both listeners may
// use them; a skip asks whoever holds the queue to move it.
func (a *app) togetherTransport(c *ui.Context, tr qqmusic.Track) {
	p := a.pal
	st := &a.together
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		if iconButton(c.Key("together-prev"), iconPrev, 16, p.muted, "上一首").Clicked() {
			if st.host {
				a.pl.Prev()
			} else {
				a.togetherPrevRequest()
			}
		}
		if playButton(c.Key("together-play"), a.pl.Playing(), 22, p.accent).Clicked() {
			a.togetherTogglePlay()
		}
		if iconButton(c.Key("together-next"), iconNext, 16, p.muted, "下一首").Clicked() {
			if st.host {
				a.pl.Next()
			} else {
				a.togetherNextRequest()
			}
		}
		ui.Box(c).Grow(1)
	})
	a.togetherProgress(c, tr)
}

func (a *app) togetherProgress(c *ui.Context, tr qqmusic.Track) {
	p := a.pal
	st := &a.together
	pos := a.pl.Position()
	if st.playback != nil && st.client != nil {
		// Only a host playing the room's own track knows where it is
		// better than the anchor does.
		cur := a.pl.Current()
		if !st.host || cur == nil || cur.ID != tr.ID {
			pos = together.Expected(st.playback, st.client.ServerNow())
		}
	}
	dur := tr.Length()
	share := float32(0)
	if dur > 0 {
		share = float32(pos) / float32(dur)
	}
	share = min(max(share, 0), 1)
	ui.Column(c).Gap(5).Children(func() {
		ui.Box(c).Height(4).Radius(2).Background(radixSage(p.dark, 4)).Children(func() {
			ui.Box(c).Height(4).Radius(2).WidthPercent(share * 100).Background(p.accent)
		})
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, clock(pos)).FontSize(11).TextColor(p.muted)
			ui.Box(c).Grow(1)
			ui.Text(c, clock(dur)).FontSize(11).TextColor(p.muted)
		})
	})
}

// togetherRequests is the song request desk: a search, what it found, and
// what the room asked for. Any member may start a requested song.
func (a *app) togetherRequests(c *ui.Context) {
	p := a.pal
	st := &a.together
	ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Padding(16).Gap(8).Radius(20).
		Background(p.sidebar).Border(1, p.hairline).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "点歌台").FontSize(15).FontWeight(700)
			ui.Text(c, fmt.Sprintf("%d", len(st.pending))).FontSize(10.5).FontWeight(700).TextColor(p.muted).
				PaddingX(7).Height(18).Radius(999).Center().Background(p.hover)
			ui.Box(c).Grow(1)
			ui.Text(c, "谁都能点，谁都能播").FontSize(10.5).TextColor(p.faint).SingleLine()
		})
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("together-song"), &st.requestQuery).Placeholder("搜索歌曲，点歌到房间").FontSize(12.5).Grow(1).Height(32)
			if in.Submitted() {
				a.togetherSearchSongs()
			}
			if iconButton(c.Key("together-song-go"), iconSearch, 14, p.muted, "搜索").Clicked() {
				a.togetherSearchSongs()
			}
		})
		ui.Scroll(c.Key("together-requests-body")).Grow(1).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				if st.requestErr != "" {
					ui.Text(c, st.requestErr).FontSize(11.5).TextColor(p.muted)
				}
				if st.requestLoading {
					ui.Text(c, "搜索中…").FontSize(11.5).TextColor(p.muted)
				}
				for i := range st.requestResults {
					if i >= 5 {
						break
					}
					a.togetherSearchRow(c, i, st.requestResults[i])
				}
				if len(st.pending) == 0 {
					ui.Text(c, "还没有人点歌").FontSize(12).TextColor(p.muted).Margin(6, 0, 0, 0)
					return
				}
				for i, req := range st.pending {
					a.togetherRequestRow(c, i, req)
				}
			})
		})
	})
}

// togetherSearchRow is one song a search found, with the button that asks
// the room for it.
func (a *app) togetherSearchRow(c *ui.Context, i int, tr qqmusic.Track) {
	p := a.pal
	row := ui.Row(c).Height(38).PaddingX(6).Gap(8).Radius(8).AlignItems(ui.Center)
	row.Children(func() {
		a.cover(c, tr.Album.PicURL, 90, 28, 6)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, tr.Name).FontSize(12).FontWeight(600).SingleLine()
			ui.Text(c, tr.ArtistNames()).FontSize(10.5).TextColor(p.muted).SingleLine()
		})
		b := iconButton(c.Key("together-add-"+tr.MID), iconPlus, 13, p.accent, "点歌")
		if b.Clicked() {
			a.togetherRequestSong(tr)
		}
	})
	row.Background(row.AnimateColor("bg", ui.Transparent, ui.Animation{Duration: 120 * time.Millisecond}))
	if row.Hovered() {
		row.Background(row.AnimateColor("bg", p.hover, ui.Animation{Duration: 120 * time.Millisecond}))
	}
}

func (a *app) togetherRequestRow(c *ui.Context, i int, req together.Pending) {
	p := a.pal
	st := &a.together
	ui.Row(c).Height(44).PaddingX(8).Gap(8).Radius(9).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("%d", i+1)).FontSize(11).TextColor(p.faint).Width(16)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, req.Track.Name).FontSize(12.5).FontWeight(600).SingleLine()
			ui.Text(c, togetherArtists(req.Track)+" · "+req.ByName).FontSize(10.5).TextColor(p.muted).SingleLine()
		})
		if ui.Button(c.Key("together-play-"+req.ID), "播放").Clicked() {
			a.togetherPlayPending(req)
		}
		if st.host || req.By == st.clientID {
			iconButton(c.Key("together-drop-"+req.ID), iconX, 12, p.muted, "移除").OnClick(func() {
				a.togetherRemovePending(req)
			})
		}
	})
}

// togetherChatCard is the room's chat: what its members said, the quick
// faces, and the field to say more.
func (a *app) togetherChatCard(c *ui.Context) {
	p := a.pal
	st := &a.together
	ui.Column(c).Grow(1).Shrink(1).MinWidth(0).MinHeight(210).Padding(14).Gap(8).Radius(20).
		Background(p.sidebar).Border(1, p.hairline).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "聊天").FontSize(15).FontWeight(700)
			ui.Text(c, fmt.Sprintf("%d 人", max(1, len(st.members)))).FontSize(10.5).FontWeight(700).TextColor(p.muted).
				PaddingX(7).Height(18).Radius(999).Center().Background(p.hover)
		})
		if len(st.chat) == 0 {
			ui.Text(c, "还没有消息").FontSize(12.5).TextColor(p.muted).Grow(1)
		} else {
			ui.List(c, &st.chatList, len(st.chat), func(i int) { a.togetherChatLine(c, st.chat[i]) }).Grow(1)
		}
		ui.Row(c).Gap(2).Children(func() {
			for _, emoji := range []string{"😀", "😂", "🥰", "👏", "🎵", "❤️", "🔥", "🌙"} {
				b := roundButton(c.Key("together-emoji-"+emoji), 18, p.text, emoji)
				b.Children(func() { ui.Text(c, emoji).FontSize(16) })
				if b.Clicked() {
					st.chatDraft += emoji
				}
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("together-chat"), &st.chatDraft).Placeholder("说点什么…").FontSize(13).Grow(1).Height(34)
			if in.Submitted() {
				a.togetherSendChat()
			}
			b := iconButton(c.Key("together-send"), iconSend, 15, p.accent, "发送")
			if strings.TrimSpace(st.chatDraft) == "" {
				b.Disabled(true)
			}
			if b.Clicked() {
				a.togetherSendChat()
			}
		})
	})
}

// togetherAvatar draws a member's QQ Music portrait over their initial,
// which shows until the portrait loads, or instead of one.
func (a *app) togetherAvatar(c *ui.Context, url, name string, size float32) ui.Element {
	p := a.pal
	box := ui.Box(c).Size(size, size).Radius(size / 2).Center().Background(p.accent.Alpha(0.18))
	box.Children(func() {
		ui.Text(c, togetherInitial(name)).FontSize(size * 0.45).FontWeight(700).TextColor(p.accent)
		if strings.TrimSpace(url) == "" {
			return
		}
		bmp := bitmap(a.art.get(qqmusic.Sized(url, 90), artCover))
		in := artIn(box, bmp)
		artImage(c, bmp, size/2, in)
	})
	return box
}

func (a *app) togetherMember(c *ui.Context, m together.Member) {
	p := a.pal
	chip := ui.Row(c).Height(28).PaddingX(6).Gap(6).Radius(999).AlignItems(ui.Center).Background(p.sidebar).Border(1, p.hairline)
	chip.Children(func() {
		a.togetherAvatar(c, m.Avatar, m.Name, 20)
		ui.Text(c, m.Name).FontSize(12).FontWeight(600).SingleLine().MaxWidth(110)
		if m.Host {
			ui.Text(c, "房主").FontSize(9.5).FontWeight(700).TextColor(p.accent).PaddingX(5).Height(16).Radius(999).Center().Background(p.accent.Alpha(0.12))
		}
	})
	if a.together.host && m.Client != a.together.clientID {
		chip.ContextMenu(func(menu *ui.Menu) {
			if menu.Item("移出房间").Chosen() {
				a.togetherKick(m)
			}
		})
	}
}

func (a *app) togetherChatLine(c *ui.Context, msg together.Chat) {
	p := a.pal
	ui.Row(c).Gap(8).AlignItems(ui.Start).Padding(4, 0, 4, 0).Children(func() {
		a.togetherAvatar(c, msg.Avatar, msg.Name, 22)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, msg.Name).FontSize(11.5).FontWeight(600).TextColor(p.muted)
				ui.Text(c, time.UnixMilli(msg.At).Format("15:04")).FontSize(10).TextColor(p.faint)
			})
			ui.Text(c, msg.Text).FontSize(13).TextColor(p.text)
		})
	})
}

func (a *app) togetherStatusLine() string {
	st := &a.together
	if st.err != "" {
		return st.err
	}
	switch st.status {
	case together.StatusOnline:
		if !st.host && !st.hasHost() {
			return "房主已离开，等待重连…"
		}
		return "已连接"
	case together.StatusConnecting:
		return "连接中…"
	default:
		return "连接断开，正在重连…"
	}
}

func (st *togetherState) hasHost() bool {
	for _, m := range st.members {
		if m.Host {
			return true
		}
	}
	return false
}

// hostName is the room's name, as QQ 音乐 names a room after the listener
// who opened it: their QQ Music nickname, exactly as it is.
func (st *togetherState) hostName() string {
	for _, m := range st.members {
		if m.Host {
			return m.Name
		}
	}
	return ""
}

func (a *app) togetherSendChat() {
	st := &a.together
	text := strings.TrimSpace(st.chatDraft)
	if text == "" || st.client == nil {
		return
	}
	st.chatDraft = ""
	client := st.client
	go func() { _ = client.Chat(text) }()
}

func togetherInitial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return string(r)
	}
	return "?"
}

// playbackNeedsPermission reports whether the player failed because this
// account may not play the track, as a guest without the room's membership.
func playbackNeedsPermission(err string) bool {
	return strings.Contains(err, "VIP") || strings.Contains(err, "会员") || strings.Contains(err, "购买") || strings.Contains(err, "权限")
}

// togetherArtists is the room track's artists' names, which has no
// qqmusic.Track to ask.
func togetherArtists(t together.Track) string {
	names := make([]string, len(t.Artists))
	for i, a := range t.Artists {
		names[i] = a.Name
	}
	return strings.Join(names, ", ")
}

// The room's canvas and cards: mint, as QQ 音乐 draws a room, and a paper
// card for the record, dark or light with the appearance.
func togetherCanvas(dark bool) ui.Color {
	if dark {
		return ui.Hex("#0D1411")
	}
	return ui.Hex("#E7F6EF")
}

func togetherCardTop(dark bool) ui.Color {
	if dark {
		return ui.Hex("#18231D")
	}
	return ui.Hex("#FFFFFF")
}

func togetherCardBottom(dark bool) ui.Color {
	if dark {
		return ui.Hex("#101A15")
	}
	return ui.Hex("#E1F5EA")
}

func togetherSleeve(dark bool) ui.Color {
	if dark {
		return ui.Hex("#1C2721")
	}
	return ui.Hex("#FFFFFF")
}

// drawTonearm paints the arm over a record card: its pivot at the top
// right, and its head resting on the record's edge.
func drawTonearm(pt *ui.Painter, r ui.Rect) {
	cx, cy := r.X+r.W*0.5, r.Y+r.H*0.5
	rad := r.W * 0.42
	px, py := r.X+r.W*0.84, r.Y+r.H*0.07
	ex, ey := cx+rad*0.60, cy-rad*0.42
	pt.Line(px, py, ex, ey, 5, ui.Hex("#C3CBD1"))
	pt.Fill(ui.Rect{X: px - 13, Y: py - 13, W: 26, H: 26}, ui.Hex("#E9EDF0"), 13)
	pt.Stroke(ui.Rect{X: px - 13, Y: py - 13, W: 26, H: 26}, ui.Hex("#BEC8CF"), 13, 1.5)
	pt.Fill(ui.Rect{X: px - 5, Y: py - 5, W: 10, H: 10}, ui.Hex("#AEB8BF"), 5)
	pt.Fill(ui.Rect{X: ex - 7, Y: ey - 4, W: 14, H: 21}, ui.Hex("#B6C0C7"), 5)
}
