package together

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"lilt/internal/network"
)

var (
	ErrRoomNotFound = errors.New("房间不存在或已过期")
	ErrRoomFull     = errors.New("房间人数已满")
	ErrOffline      = errors.New("还没有连上房间服务器")
	ErrKicked       = errors.New("你被房主移出了房间")
	ErrEnded        = errors.New("房主结束了房间")
	ErrReplaced     = errors.New("另一个连接接管了同一间房")
	ErrLeft         = errors.New("已离开房间")
)

// statusKicked is the close code the server sends a member the host removed.
const (
	statusKicked   = websocket.StatusCode(4003)
	statusEnded    = websocket.StatusCode(4004)
	statusReplaced = websocket.StatusCode(4001)
	statusBye      = websocket.StatusCode(4005)
)

// The room's heartbeat: the server sweeps a member it has not heard from in
// a minute, so the beat it asks for is bounded here too. QQ 音乐 carries the
// same number in its room as heartBeatInterval.
const (
	DefaultHeartbeat = 15 * time.Second
	MinHeartbeat     = 5 * time.Second
	MaxHeartbeat     = 30 * time.Second
)

type Status int

const (
	StatusConnecting Status = iota
	StatusOnline
	StatusOffline
)

func (s Status) String() string {
	switch s {
	case StatusOnline:
		return "已连接"
	case StatusConnecting:
		return "连接中"
	default:
		return "未连接"
	}
}

// Event kinds delivered to OnEvent, from the client's own goroutine: the
// app posts them to the main thread before touching its state.
const (
	EventStatus        = "status"
	EventWelcome       = "welcome"
	EventMembers       = "members"
	EventPlayback      = "playback"
	EventPending       = "pending"
	EventChat          = "chat"
	EventRequest       = "request"
	EventStreamRequest = "stream_request"
	// EventPlay is a member starting a requested song: the host's queue
	// takes it, and its anchor carries everyone else.
	EventPlay   = "play"
	EventStream = "stream"
	EventQueue  = "queue"
	// EventYou says whether the room is this member's to hold now, as after
	// the host hands it over.
	EventYou         = "you"
	EventServerError = "server_error"
)

type Event struct {
	Kind     string
	Status   Status
	Err      error
	Welcome  *Welcome
	Members  []Member
	Playback *Playback
	Pending  []Pending
	Chat     *Chat
	// Action is a next or prev request another member made.
	Action string
	// TrackID is the track another member asked the room to share a stream
	// for, as a guest without the membership that track needs would.
	TrackID int64
	// Track is the song another member asked to play from the desk.
	Track *Track
	// Stream is a link another member resolved for the room's track.
	Stream *Stream
	// Queue is the host's queue, as the host last mirrored it.
	Queue *Queue
	// You says whether this member holds the room now.
	You *You
	// Code is the server's error code, such as not_host or room_full.
	Code string
}

type Options struct {
	// Relay is the room server: wss://together.bdprk.com, or
	// http://127.0.0.1:8787 while developing.
	Relay    string
	Code     string
	HostKey  string
	ClientID string
	Name     string
	// Avatar is the listener's QQ Music portrait, shown in the room.
	Avatar  string
	OnEvent func(Event)
	HTTP    *http.Client
}

type CreatedRoom struct {
	Code    string `json:"code"`
	HostKey string `json:"hostKey"`
	WS      string `json:"ws"`
}

type Client struct {
	opts Options
	http *http.Client

	mu       sync.Mutex
	write    sync.Mutex
	conn     *websocket.Conn
	online   bool
	closed   bool
	offsetMs int64
	bestRTT  time.Duration
	clockAt  time.Time
	pings    map[int64]time.Time
	pingSeq  int64
	// heartbeat is how often this member pings the room, and lastPing is
	// when it last did. The room sets the beat (QQ 音乐's
	// heartBeatInterval); until it does, fifteen seconds is the default
	// the server also sweeps on.
	heartbeat time.Duration
	lastPing  time.Time
}

func DefaultHTTPClient() *http.Client {
	return &http.Client{Transport: network.Transport(), Timeout: 25 * time.Second}
}

func New(opts Options) *Client {
	hc := opts.HTTP
	if hc == nil {
		hc = DefaultHTTPClient()
	}
	return &Client{opts: opts, http: hc, pings: map[int64]time.Time{}}
}

// CreateRoom asks the relay for a room code and the host key that proves
// who owns it. Creating a room is the one step a listener cannot retry by
// hand mid-song, so a flaky hop is worth another try before saying it
// failed: the relay can be cold, and its Durable Object may refuse the
// first initialisation.
func CreateRoom(ctx context.Context, relay string, hc *http.Client) (CreatedRoom, error) {
	if err := ValidateRelay(relay); err != nil {
		return CreatedRoom{}, err
	}
	base, err := httpBase(relay)
	if err != nil {
		return CreatedRoom{}, err
	}
	if hc == nil {
		hc = DefaultHTTPClient()
	}
	var lastErr error
	for attempt := 0; attempt < createAttempts; attempt++ {
		if attempt > 0 {
			if !wait(ctx, time.Duration(attempt)*createBackoff) {
				break
			}
		}
		room, err := createRoomOnce(ctx, base, hc)
		if err == nil {
			return room, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = ctx.Err()
	}
	return CreatedRoom{}, lastErr
}

const (
	createAttempts = 3
	createBackoff  = 900 * time.Millisecond
	createTimeout  = 9 * time.Second
)

func createRoomOnce(ctx context.Context, base string, hc *http.Client) (CreatedRoom, error) {
	reqCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	body := strings.NewReader(`{"name":"Lilt"}`)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/room", body)
	if err != nil {
		return CreatedRoom{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return CreatedRoom{}, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 16<<10))
	if err != nil {
		return CreatedRoom{}, err
	}
	if res.StatusCode != http.StatusOK {
		return CreatedRoom{}, fmt.Errorf("创建房间失败（%d）%s", res.StatusCode, roomErrorText(data))
	}
	var out CreatedRoom
	if err := json.Unmarshal(data, &out); err != nil {
		return CreatedRoom{}, err
	}
	if out.Code == "" || out.HostKey == "" {
		return CreatedRoom{}, errors.New("房间服务器返回了空房间号")
	}
	return out, nil
}

// roomErrorText is what the relay said went wrong, when it says.
func roomErrorText(data []byte) string {
	var out struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &out) != nil || out.Error == "" {
		return ""
	}
	return "：" + out.Error
}

// Run keeps a connection to the room, reconnecting until ctx ends.
func (c *Client) Run(ctx context.Context) {
	backoff := 700 * time.Millisecond
	for {
		if ctx.Err() != nil {
			c.emit(Event{Kind: EventStatus, Status: StatusOffline})
			return
		}
		c.emit(Event{Kind: EventStatus, Status: StatusConnecting})
		raw, err := c.url()
		if err != nil {
			c.emit(Event{Kind: EventStatus, Status: StatusOffline, Err: err})
			return
		}
		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		opts := &websocket.DialOptions{HTTPClient: c.http}
		if c.opts.HostKey != "" {
			// The key stays out of the URL: a query string is the part of a
			// handshake proxies and logs are most likely to keep.
			opts.HTTPHeader = http.Header{"X-Lilt-Host-Key": {c.opts.HostKey}}
		}
		conn, res, err := websocket.Dial(dialCtx, raw, opts)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			reason := roomError(err, res)
			c.emit(Event{Kind: EventStatus, Status: StatusOffline, Err: reason})
			if errors.Is(reason, ErrRoomNotFound) || errors.Is(reason, ErrRoomFull) {
				return
			}
			if !wait(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 12*time.Second)
			continue
		}
		conn.SetReadLimit(256 << 10)
		c.setConn(conn)
		backoff = 700 * time.Millisecond
		c.resetClock()
		c.emit(Event{Kind: EventStatus, Status: StatusOnline})
		go c.pingLoop(ctx, conn)
		err = c.readLoop(ctx, conn)
		c.setConn(nil)
		_ = conn.CloseNow()
		if ctx.Err() != nil {
			return
		}
		if status := websocket.CloseStatus(err); status == statusKicked || status == statusEnded || status == statusReplaced || status == statusBye {
			reason := ErrKicked
			switch status {
			case statusEnded:
				reason = ErrEnded
			case statusReplaced:
				reason = ErrReplaced
			case statusBye:
				reason = ErrLeft
			}
			c.emit(Event{Kind: EventStatus, Status: StatusOffline, Err: reason})
			return
		}
		c.emit(Event{Kind: EventStatus, Status: StatusOffline, Err: err})
		if !wait(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 12*time.Second)
	}
}

func (c *Client) Online() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.online
}

// ServerNow is this moment on the room server's clock, as the ping samples
// estimated it.
func (c *Client) ServerNow() int64 {
	c.mu.Lock()
	offset := c.offsetMs
	c.mu.Unlock()
	return time.Now().UnixMilli() + offset
}

func (c *Client) Publish(pb Playback) error {
	out := pb
	out.UpdatedAt, out.Rev = 0, 0
	return c.send(wire{T: "playback", Playback: &out})
}

func (c *Client) Chat(text string) error {
	return c.send(wire{T: "chat", Text: text})
}

// SetProfile tells the room the name and portrait to show for this member:
// the QQ Music account's, which may only arrive once the session restores
// after the room was joined.
func (c *Client) SetProfile(name, avatar string) error {
	return c.send(wire{T: "profile", Name: name, Avatar: avatar})
}

// RequestStream asks the room for a stream it resolved for trackID: a guest
// whose account may not play the track asks whoever can, and a member with
// the link answers with the host's own hint.
func (c *Client) RequestStream(trackID int64) error {
	return c.send(wire{T: "stream.request", ID: trackID})
}

// ShareStream gives the room a signed link this account resolved, so a
// member whose account may not play the track can still listen.
func (c *Client) ShareStream(s Stream) error {
	if s.TrackID <= 0 || s.URL == "" {
		return nil
	}
	return c.send(wire{T: "stream", Stream: &s})
}

// PublishQueue mirrors the host's queue to the room, so a guest's client
// shows and plays the same list rather than one borrowed track.
func (c *Client) PublishQueue(q Queue) error {
	if len(q.Tracks) == 0 {
		return nil
	}
	return c.send(wire{T: "queue", Queue: &q})
}

// Transfer hands the room to another member; only the host may.
func (c *Client) Transfer(client string) error {
	if client == "" {
		return nil
	}
	return c.send(wire{T: "transfer", Text: client})
}

func (c *Client) AddPending(t Track) error {
	return c.send(wire{T: "pending.add", Track: &t})
}

func (c *Client) RemovePending(id string) error {
	return c.send(wire{T: "pending.remove", ID: 0, Text: id})
}

func (c *Client) PlayPending(id string) error {
	return c.send(wire{T: "pending.play", Text: id})
}

// Kick asks the server to remove a member; only the host may.
func (c *Client) Kick(client string) error {
	return c.send(wire{T: "kick", Text: client})
}

// Request asks the room to move to the next or previous track. The host
// applies it, since the queue is the host's.
func (c *Client) Request(action string) error {
	return c.send(wire{T: "request", Text: action})
}

// End closes the room for everyone; only the host may. It gives up quickly:
// the app may call it as it quits, where waiting long would hold up the exit.
func (c *Client) End() error {
	if err := c.sendWithin(500*time.Millisecond, wire{T: "end"}); err != nil {
		return err
	}
	// The write returns once the frame is buffered; the app closes the
	// connection right after, so wait for the server to answer by closing
	// it, which says the room is gone.
	c.waitOffline(700 * time.Millisecond)
	return nil
}

func (c *Client) waitOffline(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		online := c.online
		c.mu.Unlock()
		if !online {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Leave says goodbye so the room drops this member at once, rather than
// waiting to notice a socket that went quiet.
func (c *Client) Leave() error {
	if err := c.sendWithin(500*time.Millisecond, wire{T: "bye"}); err != nil {
		return err
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- conn.Close(websocket.StatusNormalClosure, "bye") }()
	select {
	case err := <-done:
		return err
	case <-time.After(500 * time.Millisecond):
		_ = conn.CloseNow()
		return nil
	}
}

func (c *Client) send(msg wire) error {
	return c.sendWithin(10*time.Second, msg)
}

func (c *Client) sendWithin(timeout time.Duration, msg wire) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrOffline
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c.write.Lock()
	defer c.write.Unlock()
	return conn.Write(ctx, websocket.MessageText, data)
}

func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg wire
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.T {
		case "welcome":
			c.noteServerTime(msg.ServerTime)
			w := &Welcome{
				ServerTime: msg.ServerTime,
				Code:       msg.Code,
				Members:    msg.Members,
				Playback:   msg.Playback,
				Pending:    msg.Pending,
				Chat:       msg.Chat,
				Stream:     msg.Stream,
				Streams:    msg.Streams,
				Queue:      msg.Queue,
				Features:   msg.Features,
			}
			if msg.You != nil {
				w.You = *msg.You
			}
			c.emit(Event{Kind: EventWelcome, Welcome: w})
		case "you":
			if msg.You != nil {
				you := *msg.You
				c.emit(Event{Kind: EventYou, You: &you})
			}
		case "stream":
			if msg.Stream != nil {
				c.emit(Event{Kind: EventStream, Stream: msg.Stream})
			}
		case "queue":
			if msg.Queue != nil {
				c.emit(Event{Kind: EventQueue, Queue: msg.Queue})
			}
		case "members":
			c.emit(Event{Kind: EventMembers, Members: msg.Members})
		case "playback":
			c.emit(Event{Kind: EventPlayback, Playback: msg.Playback})
		case "pending":
			c.emit(Event{Kind: EventPending, Pending: msg.Pending})
		case "chat":
			if msg.Message != nil {
				c.emit(Event{Kind: EventChat, Chat: msg.Message})
			}
		case "request":
			c.emit(Event{Kind: EventRequest, Action: msg.Text})
		case "stream.request":
			if msg.ID > 0 {
				c.emit(Event{Kind: EventStreamRequest, TrackID: msg.ID})
			}
		case "play":
			if msg.Track != nil {
				c.emit(Event{Kind: EventPlay, Track: msg.Track})
			}
		case "pong":
			c.notePong(msg.ID, msg.T1)
		case "error":
			c.emit(Event{Kind: EventServerError, Code: msg.Code})
		}
	}
}

func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) {
	// One second of granularity, so a room that asks for a slower or
	// quicker beat is obeyed without rebuilding the connection.
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	c.sendPing()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			c.mu.Lock()
			current := c.conn == conn
			gap := c.heartbeat
			last := c.lastPing
			c.mu.Unlock()
			if !current {
				return
			}
			if gap <= 0 {
				gap = DefaultHeartbeat
			}
			if now.Sub(last) >= gap {
				c.sendPing()
			}
		}
	}
}

// SetHeartbeat is the beat the room asks for: fast enough that a member
// who closes the lid is noticed, slow enough not to be a chat every few
// seconds.
func (c *Client) SetHeartbeat(d time.Duration) {
	if d < MinHeartbeat {
		d = MinHeartbeat
	}
	if d > MaxHeartbeat {
		d = MaxHeartbeat
	}
	c.mu.Lock()
	c.heartbeat = d
	c.mu.Unlock()
}

func (c *Client) sendPing() {
	c.mu.Lock()
	c.pingSeq++
	id := c.pingSeq
	now := time.Now()
	c.pings[id] = now
	c.lastPing = now
	c.mu.Unlock()
	_ = c.send(wire{T: "ping", ID: id, T0: now.UnixMilli()})
}

// noteServerTime seeds the clock before the first ping comes back: a guest
// that follows the welcome at once would otherwise read the room with this
// machine's clock.
func (c *Client) noteServerTime(serverTime int64) {
	if serverTime <= 0 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	if c.bestRTT == 0 {
		c.offsetMs = serverTime - now.UnixMilli()
		c.clockAt = now
	}
	c.mu.Unlock()
}

func (c *Client) notePong(id int64, serverTime int64) {
	c.mu.Lock()
	sent, ok := c.pings[id]
	if ok {
		delete(c.pings, id)
	}
	c.mu.Unlock()
	if !ok || serverTime <= 0 {
		return
	}
	now := time.Now()
	rtt := now.Sub(sent)
	if rtt < 0 || rtt > 10*time.Second {
		return
	}
	offset := serverTime - (sent.UnixMilli()+now.UnixMilli())/2
	c.mu.Lock()
	if c.bestRTT == 0 || rtt <= c.bestRTT || now.Sub(c.clockAt) > 2*time.Minute {
		c.offsetMs, c.bestRTT, c.clockAt = offset, rtt, now
	}
	c.mu.Unlock()
}

func (c *Client) resetClock() {
	c.mu.Lock()
	c.offsetMs, c.bestRTT, c.clockAt = 0, 0, time.Time{}
	c.pings = map[int64]time.Time{}
	c.mu.Unlock()
}

func (c *Client) setConn(conn *websocket.Conn) {
	c.mu.Lock()
	c.conn = conn
	c.online = conn != nil
	c.mu.Unlock()
}

func (c *Client) emit(ev Event) {
	if c.opts.OnEvent != nil {
		c.opts.OnEvent(ev)
	}
}

func (c *Client) url() (string, error) {
	if err := ValidateRelay(c.opts.Relay); err != nil {
		return "", err
	}
	u, err := url.Parse(c.opts.Relay)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("房间服务器地址 %q 需要是 http、https、ws 或 wss", c.opts.Relay)
	}
	path := strings.TrimSuffix(u.Path, "/")
	if path == "" {
		path = "/ws"
	} else if !strings.HasSuffix(path, "/ws") {
		path += "/ws"
	}
	u.Path = path
	q := u.Query()
	q.Set("code", strings.ToUpper(strings.TrimSpace(c.opts.Code)))
	q.Set("client", c.opts.ClientID)
	q.Set("name", c.opts.Name)
	if c.opts.Avatar != "" {
		q.Set("avatar", c.opts.Avatar)
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}

func httpBase(relay string) (string, error) {
	u, err := url.Parse(relay)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("房间服务器地址 %q 需要是 http、https、ws 或 wss", relay)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.Path = strings.TrimSuffix(u.Path, "/ws")
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimSuffix(u.String(), "/"), nil
}

func roomError(err error, res *http.Response) error {
	if res != nil {
		switch res.StatusCode {
		case http.StatusNotFound:
			return ErrRoomNotFound
		case http.StatusConflict:
			return ErrRoomFull
		}
	}
	return err
}

func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
