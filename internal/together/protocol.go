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
	// Features are what this relay understands beyond the first protocol:
	// profile updates, stream requests, and the desk playing through the
	// host's queue. An older relay sends none, and the app falls back to
	// what it did before.
	Features []string `json:"features,omitempty"`
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
}
