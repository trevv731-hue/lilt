package together

// 一起听的线上协议。房主客户端发布播放锚点，服务器盖上自己的时间戳后
// 广播；访客按收到的锚点推算此刻应该播到哪里。

const DefaultRelay = "wss://together.bdprk.com"

type Artist struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Album struct {
	ID     int64  `json:"id"`
	MID    string `json:"mid"`
	Name   string `json:"name"`
	PicURL string `json:"picUrl"`
}

// Track is the part of a QQ Music track the room needs to show it and to
// let each client resolve its own stream.
type Track struct {
	ID       int64    `json:"id"`
	MID      string   `json:"mid,omitempty"`
	MediaMID string   `json:"mediaMid,omitempty"`
	Name     string   `json:"name"`
	Artists  []Artist `json:"artists"`
	Album    Album    `json:"album"`
	Duration int64    `json:"duration"` // milliseconds
	// Fee is QQ Music's marker of a song its members keep: 1 for those.
	Fee int `json:"fee,omitempty"`
}

type Playback struct {
	Track      *Track `json:"track"`
	Playing    bool   `json:"playing"`
	PositionMs int64  `json:"positionMs"`
	// UpdatedAt is the server's clock when it received the anchor.
	UpdatedAt int64 `json:"updatedAt"`
	Rev       int64 `json:"rev"`
}

type Member struct {
	Client   string `json:"client"`
	Name     string `json:"name"`
	Avatar   string `json:"avatar,omitempty"`
	Host     bool   `json:"host"`
	JoinedAt int64  `json:"joinedAt"`
}

type Chat struct {
	ID     string `json:"id"`
	Client string `json:"client"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
	Text   string `json:"text"`
	At     int64  `json:"at"`
	// System marks the room's own lines: who came, who left, who holds it.
	System bool `json:"sys,omitempty"`
}

// Stream is a signed play link one member resolved for the room's track.
// QQ Music signs it for the account that asked, but the link plays for
// whoever holds it, so one membership covers the room.
type Stream struct {
	TrackID int64  `json:"trackId"`
	URL     string `json:"url"`
	Type    string `json:"type,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Level   string `json:"level,omitempty"`
	At      int64  `json:"at,omitempty"`
	// By and ByName are the member whose account signed the link, as the
	// server records them: the room shows whose membership it is playing.
	By     string `json:"by,omitempty"`
	ByName string `json:"byName,omitempty"`
}

// Queue is the host's queue, mirrored so a guest plays the same list.
type Queue struct {
	Tracks []Track `json:"tracks"`
	Index  int     `json:"index"`
	Rev    int64   `json:"rev,omitempty"`
	// Shuffle is the room's play mode, as QQ 音乐 keeps playMode and
	// songRandomIndex in the room rather than in one client: whoever holds
	// the queue decides, and everyone sees the same mode.
	Shuffle bool `json:"shuffle,omitempty"`
}

type Pending struct {
	ID     string `json:"id"`
	Track  Track  `json:"track"`
	By     string `json:"by"`
	ByName string `json:"byName"`
	At     int64  `json:"at"`
}

type You struct {
	Client string `json:"client"`
	Host   bool   `json:"host"`
}

type Welcome struct {
	ServerTime int64     `json:"serverTime"`
	Code       string    `json:"code"`
	You        You       `json:"you"`
	Members    []Member  `json:"members"`
	Playback   *Playback `json:"playback"`
	Pending    []Pending `json:"pending"`
	Chat       []Chat    `json:"chat"`
	// Stream is the link the room already holds for its track, so a member
	// who joins late needs no one to answer.
	Stream *Stream `json:"stream"`
	// Streams are the links the room still holds for its recent tracks, so
	// a member who seeks back or joins late needs no one to sign again.
	Streams []Stream `json:"streams,omitempty"`
	// Queue is the host's queue, as the room last heard it.
	Queue *Queue `json:"queue"`
	// Features are what this relay understands beyond the first protocol:
	// profile updates, stream requests, and the desk playing through the
	// host's queue. An older relay sends none, and the app falls back to
	// what it did before.
	Features []string `json:"features,omitempty"`
	// HeartbeatMs is how often the room wants to hear from this member, as
	// QQ 音乐 carries heartBeatInterval in the room: the server knows when
	// it sweeps absent members, so it sets the beat. Zero on an older
	// relay, and the client keeps its own.
	HeartbeatMs int64 `json:"heartbeatMs,omitempty"`
}

// wire is one message either way; the server picks the fields it needs.
type wire struct {
	T          string    `json:"t"`
	ID         int64     `json:"id,omitempty"`
	Name       string    `json:"name,omitempty"`
	Avatar     string    `json:"avatar,omitempty"`
	T0         int64     `json:"t0,omitempty"`
	T1         int64     `json:"t1,omitempty"`
	ServerTime int64     `json:"serverTime,omitempty"`
	Code       string    `json:"code,omitempty"`
	You        *You      `json:"you,omitempty"`
	Members    []Member  `json:"members,omitempty"`
	Playback   *Playback `json:"playback,omitempty"`
	Pending    []Pending `json:"pending,omitempty"`
	Chat       []Chat    `json:"chat,omitempty"`
	Message    *Chat     `json:"message,omitempty"`
	Track      *Track    `json:"track,omitempty"`
	Text       string    `json:"text,omitempty"`
	Features   []string  `json:"features,omitempty"`
	Stream     *Stream   `json:"stream,omitempty"`
	Streams    []Stream  `json:"streams,omitempty"`
	Queue      *Queue    `json:"queue,omitempty"`
}
