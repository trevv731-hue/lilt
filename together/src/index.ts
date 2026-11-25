// Lilt 一起听房间服务。
//
// 一台 Durable Object 是一间房：成员、播放锚点、点歌台与最近聊天都放在
// 它里面。房主用创建房间时拿到的 hostKey 证明身份，其余连接都是访客；
// 服务器只同步控制信令，音频永远由各自的客户端从 QQ 音乐取。

interface Env {
  ROOMS: DurableObjectNamespace;
}

interface Track {
  id: number;
  mid: string;
  mediaMid: string;
  name: string;
  artists: { id: number; name: string }[];
  album: { id: number; mid: string; name: string; picUrl: string };
  duration: number;
  fee: number;
}

interface Playback {
  track: Track | null;
  playing: boolean;
  positionMs: number;
  updatedAt: number;
  rev: number;
}

interface Pending {
  id: string;
  track: Track;
  by: string;
  byName: string;
  at: number;
}

// Stream is a signed play link one member resolved for the room's track.
// QQ 音乐 signs it for the account that asked, but the link plays for
// whoever holds it, so one membership covers the room.
interface Stream {
  trackId: number;
  url: string;
  type: string;
  size: number;
  level: string;
  at: number;
  // by and byName are the member whose account signed the link: the room
  // shows whose membership is playing, and the server stamps them itself
  // so one member cannot name another.
  by?: string;
  byName?: string;
}

// QueueState is the host's queue, mirrored so a guest's client shows and
// plays the same list instead of a single borrowed track.
interface QueueState {
  tracks: Track[];
  index: number;
  rev: number;
  at: number;
  // shuffle is the room's play mode: the host decides, and a guest holds the
  // queue in the same order rather than shuffling its own copy. QQ 音乐
  // keeps playMode and songRandomIndex in the room the same way.
  shuffle?: boolean;
}

interface Chat {
  id: string;
  client: string;
  name: string;
  avatar: string;
  text: string;
  at: number;
  // sys marks the room's own lines: who came, who left, who holds it now.
  sys?: boolean;
}

interface Meta {
  code: string;
  hostKey: string;
  createdAt: number;
  // hostClient is who holds the room now: the creator's client at first,
  // then whoever the host handed the room to. It is the private client id,
  // which the room never exposes.
  hostClient?: string;
  // secret keys the public handles the room shows: a member's handle is a
  // hash of the connection's private client id, so nobody can read another
  // member's id from the room and reconnect as them.
  secret?: string;
}

interface Attachment {
  client: string;
  // handle is what the room sees of this member: a hash of client, keyed by
  // the room's secret, stable across reconnects.
  handle: string;
  name: string;
  avatar: string;
  host: boolean;
  joinedAt: number;
  messages: number;
  windowAt: number;
  // shares and askAt pace the messages that reach everyone else, so one
  // member cannot turn the room into a megaphone. shares is by track id:
  // a share for a new song is never held back by the last one.
  shares?: Record<string, number>;
  askAt?: number;
  plays?: number;
  playAt?: number;
}

const CHAT_KEEP = 100;
// A signed link outlives the song in the room: keeping a few lets a member
// who seeks back or joins late play without anyone signing again.
const STREAM_KEEP = 40;
const STREAM_TTL = 40 * 60 * 1000;
const PENDING_MAX = 50;
const MEMBER_MAX = 10;
const QUEUE_MAX = 60;
const CODE_LENGTH = 8;
const CODE_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const IDLE_MS = 24 * 60 * 60 * 1000;
// How long a room lives on after its host disappears, so a short
// disconnect can reconnect; a crashed host ends it after this.
const HOST_GRACE_MS = 2 * 60 * 1000;
const TEXT_MAX = 500;
const NAME_MAX = 24;
// The room sweeps quiet members every 30 seconds, so it asks for a ping
// roughly twice that often: QQ 音乐 carries the same number in its room as
// heartBeatInterval, and a client obeys the room rather than guessing.
const HEARTBEAT_MS = 12_000;

const HOME = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Lilt 一起听</title>
<style>
:root{color-scheme:light dark}
body{margin:0;min-height:100vh;display:grid;place-items:center;font:15px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;background:#f7faf8;color:#1d2620}
main{max-width:520px;padding:40px 32px;background:#fff;border:1px solid #e3ece6;border-radius:20px;box-shadow:0 20px 60px rgba(20,50,30,.07)}
h1{font-size:26px;margin:0 0 8px}
p{color:#5a6b60;margin:0 0 14px}
code{background:#eef5f0;border-radius:6px;padding:2px 6px}
ol{padding-left:22px;color:#33413a}
@media(prefers-color-scheme:dark){body{background:#0f1311;color:#e5efe8}main{background:#151a17;border-color:#26302a}h1{color:#e5efe8}p,ol{color:#9fb0a4}code{background:#1e2621}}
</style></head><body><main>
<h1>Lilt 一起听</h1>
<p>这是 Lilt 的房间服务。两个人各自打开 Lilt，用同一个房间号就能同步听歌。</p>
<ol>
<li>打开 Lilt，左侧进入「一起听」</li>
<li>一个人点「创建房间」，把 8 位房间号发给另一个人</li>
<li>另一个人在「加入房间」里输入房间号</li>
</ol>
<p>两个人都可以播放、暂停、拖动进度；下一首由房主的队列执行。房间里有一个人是会员，会员曲目就能放给所有人：谁的账号签得出播放链接，就把链接共享给房间。</p>
</main></body></html>`;

// roomCreates paces room creation in this isolate: a room is a Durable
// Object of its own, so a script that opens thousands of them must not get
// thousands of them. Cloudflare's own rate limiting is the real guard; this
// keeps an unlucky minute from turning into a bill.
let roomCreates = { at: 0, count: 0 };

function allowRoomCreate(now: number): boolean {
  if (now - roomCreates.at > 60_000) roomCreates = { at: now, count: 0 };
  roomCreates.count++;
  return roomCreates.count <= 60;
}

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json", "cache-control": "no-store", "x-content-type-options": "nosniff" },
  });
}

// ipWindows paces what one address may ask of the worker: per isolate, so a
// flood spread over the world is Cloudflare's to stop, but one machine
// hammering this one is stopped here.
type Window = { at: number; count: number };
const ipWindows = new Map<string, Window>();

function allowClient(ip: string, kind: string, limit: number, now: number): boolean {
  const key = `${kind}:${ip}`;
  const w = ipWindows.get(key);
  if (!w || now - w.at > 60_000) {
    if (ipWindows.size > 10_000) {
      for (const [k, v] of ipWindows) if (now - v.at > 120_000) ipWindows.delete(k);
    }
    ipWindows.set(key, { at: now, count: 1 });
    return true;
  }
  w.count++;
  return w.count <= limit;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function randomString(bytes: number): string {
  const b = new Uint8Array(bytes);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

function randomCode(): string {
  const b = new Uint8Array(CODE_LENGTH);
  crypto.getRandomValues(b);
  let out = "";
  for (const x of b) out += CODE_ALPHABET[x % CODE_ALPHABET.length];
  return out;
}

function cleanName(value: unknown, fallback = "听众"): string {
  if (typeof value !== "string") return fallback;
  const name = value.replace(/[\u0000-\u001f\u007f]/g, "").trim().slice(0, NAME_MAX);
  return name || fallback;
}

// cleanAvatar keeps only QQ's own portrait hosts: clients draw these, so
// an arbitrary URL from a stranger is not worth fetching.
// cleanImageURL keeps only the hosts QQ Music serves portraits and covers
// from: clients fetch these, so an arbitrary URL from a stranger would let
// them point somebody's app at a host of their choosing.
function cleanImageURL(value: unknown): string {
  if (typeof value !== "string" || value === "") return "";
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return "";
  }
  if (url.protocol !== "https:" && url.protocol !== "http:") return "";
  const host = url.hostname.toLowerCase();
  const ok =
    host === "qlogo.cn" || host.endsWith(".qlogo.cn") ||
    host === "gtimg.cn" || host.endsWith(".gtimg.cn") ||
    host === "qq.com" || host.endsWith(".qq.com");
  return ok ? url.toString().slice(0, 500) : "";
}

function cleanAvatar(value: unknown): string {
  return cleanImageURL(value);
}

// handleFor is the public name of a connection: the room shows and targets
// it, while the client id it hashes stays private to the server and the
// member itself.
async function handleFor(secret: string, client: string): Promise<string> {
  const data = new TextEncoder().encode(secret + ":" + client);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return Array.from(new Uint8Array(digest).slice(0, 8), (x) => x.toString(16).padStart(2, "0")).join("");
}

function cleanText(value: unknown): string {
  if (typeof value !== "string") return "";
  return value.replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, "").trim().slice(0, TEXT_MAX);
}

function number(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function text(value: unknown, max: number): string {
  if (typeof value !== "string") return "";
  return value.replace(/[\u0000-\u001f\u007f]/g, "").slice(0, max);
}

function sanitizeTrack(value: unknown): Track | null {
  if (!value || typeof value !== "object") return null;
  const v = value as Record<string, unknown>;
  const id = Math.floor(number(v.id));
  if (id <= 0) return null;
  const artists = Array.isArray(v.artists)
    ? v.artists.slice(0, 10).map((a) => {
        const item = (a ?? {}) as Record<string, unknown>;
        return { id: Math.floor(number(item.id)), name: text(item.name, 60) };
      }).filter((a) => a.name !== "")
    : [];
  const albumRaw = (v.album ?? {}) as Record<string, unknown>;
  const duration = Math.max(0, Math.min(number(v.duration), 24 * 60 * 60 * 1000));
  return {
    id,
    mid: text(v.mid, 64),
    mediaMid: text(v.mediaMid, 64),
    name: text(v.name, 200) || "未知歌曲",
    artists,
    album: {
      id: Math.floor(number(albumRaw.id)),
      mid: text(albumRaw.mid, 64),
      name: text(albumRaw.name, 120),
      picUrl: cleanImageURL(albumRaw.picUrl),
    },
    duration,
    fee: Math.max(0, Math.min(Math.floor(number(v.fee)), 9)),
  };
}

function sanitizeStream(value: unknown): Stream | null {
  if (!value || typeof value !== "object") return null;
  const v = value as Record<string, unknown>;
  const trackId = Math.floor(number(v.trackId));
  const url = text(v.url, 2000);
  if (trackId <= 0 || url === "") return null;
  const lower = url.toLowerCase();
  // Only what a music CDN serves: a stranger's link is not worth playing.
  if (!lower.startsWith("http://") && !lower.startsWith("https://")) return null;
  // And only QQ Music's own CDNs: a room member must not be able to send
  // the others' clients off to fetch an arbitrary address.
  let host: string;
  try {
    host = new URL(url).hostname.toLowerCase();
  } catch {
    return null;
  }
  const own = host === "qq.com" || host.endsWith(".qq.com") || host === "qqmusic.qq.com" || host.endsWith(".qqmusic.qq.com");
  if (!own) return null;
  return {
    trackId,
    url,
    type: text(v.type, 16),
    size: Math.max(0, Math.floor(number(v.size))),
    level: text(v.level, 16),
    at: Date.now(),
  };
}

function sanitizeQueue(value: unknown, rev: number): QueueState | null {
  if (!value || typeof value !== "object") return null;
  const v = value as Record<string, unknown>;
  if (!Array.isArray(v.tracks)) return null;
  const tracks: Track[] = [];
  for (const raw of v.tracks.slice(0, QUEUE_MAX)) {
    const track = sanitizeTrack(raw);
    if (track) tracks.push(track);
  }
  if (tracks.length === 0) return null;
  return {
    tracks,
    index: Math.max(0, Math.min(Math.floor(number(v.index)), tracks.length - 1)),
    rev,
    at: Date.now(),
    shuffle: v.shuffle === true,
  };
}

function sanitizePlayback(value: unknown, rev: number): Playback | null {
  if (!value || typeof value !== "object") return null;
  const v = value as Record<string, unknown>;
  const track = v.track == null ? null : sanitizeTrack(v.track);
  if (v.track != null && track === null) return null;
  return {
    track,
    playing: v.playing === true,
    positionMs: Math.max(0, Math.min(Math.floor(number(v.positionMs)), 24 * 60 * 60 * 1000)),
    updatedAt: Date.now(),
    rev,
  };
}

export class RoomDurable {
  private ctx: DurableObjectState;
  private env: Env;
  private meta: Meta | null = null;
  private playback: Playback | null = null;
  private pending: Pending[] = [];
  private chat: Chat[] = [];
  private stream: Stream | null = null;
  // streams holds the links the room still has, by track: the latest is
  // stream, and the rest answer a request for a song the room played
  // before, or a member who joins late.
  private streams: Record<string, Stream> = {};
  private queue: QueueState | null = null;
  private removed = new Set<string>();
  private loaded = false;

  constructor(ctx: DurableObjectState, env: Env) {
    this.ctx = ctx;
    this.env = env;
  }

  private async load(): Promise<void> {
    if (this.loaded) return;
    const [meta, playback, pending, chat, stream, streams, queue] = await Promise.all([
      this.ctx.storage.get<Meta>("meta"),
      this.ctx.storage.get<Playback>("playback"),
      this.ctx.storage.get<Pending[]>("pending"),
      this.ctx.storage.get<Chat[]>("chat"),
      this.ctx.storage.get<Stream>("stream"),
      this.ctx.storage.get<Record<string, Stream>>("streams"),
      this.ctx.storage.get<QueueState>("queue"),
    ]);
    this.meta = meta ?? null;
    this.playback = playback ?? null;
    this.pending = pending ?? [];
    this.chat = chat ?? [];
    this.stream = stream ?? null;
    this.streams = this.freshStreams(streams ?? {});
    this.queue = queue ?? null;
    this.loaded = true;
  }

  async fetch(request: Request): Promise<Response> {
    await this.load();
    // A room made before handles existed gets its key on the first visit;
    // its hostClient is a private client id then, which keeps working.
    if (this.meta && !this.meta.secret) {
      this.meta.secret = randomString(24);
      await this.ctx.storage.put("meta", this.meta);
    }
    const url = new URL(request.url);
    if (url.pathname === "/check") {
      return json({ exists: this.meta !== null });
    }
    if (url.pathname === "/init") {
      if (this.meta) return json({ error: "exists" }, 409);
      const body = (await request.json().catch(() => ({}))) as Record<string, unknown>;
      const code = text(body.code, 16).toUpperCase();
      const hostKey = text(body.hostKey, 128);
      if (code === "" || hostKey === "") return json({ error: "bad init" }, 400);
      this.meta = { code, hostKey, createdAt: Date.now(), secret: randomString(24) };
      await this.ctx.storage.put("meta", this.meta);
      await this.ctx.storage.put("emptyAt", Date.now());
      await this.ctx.storage.setAlarm(Date.now() + IDLE_MS);
      return json({ ok: true });
    }
    if (url.pathname !== "/ws") return new Response("not found", { status: 404 });
    if (request.headers.get("Upgrade") !== "websocket") return json({ error: "expected websocket" }, 400);
    if (!this.meta) return new Response("room not found", { status: 404 });

    const client = text(url.searchParams.get("client"), 48);
    if (client === "") return new Response("bad client", { status: 400 });
    const name = cleanName(url.searchParams.get("name"));
    const avatar = cleanAvatar(url.searchParams.get("avatar"));
    // The key rides a header, which proxies do not put in their logs; the
    // query is still read for clients that predate the header.
    const hostKey = request.headers.get("x-lilt-host-key") ?? url.searchParams.get("hostKey") ?? "";
    // The creator's key holds the room only until someone holds it: after a
    // hand-over the key must not take it back.
    const keyHost = hostKey !== "" && hostKey === this.meta.hostKey && this.meta.hostClient === undefined;
    const host = keyHost || this.meta.hostClient === client;
    this.removed.delete(client);
    // The creator's client becomes the room's holder on its first visit,
    // and keeps it across reconnects and a hand-over alike.
    if (keyHost && this.meta.hostClient !== client) {
      this.meta.hostClient = client;
      await this.ctx.storage.put("meta", this.meta);
    }
    if (host) await this.ctx.storage.delete("hostGoneAt");

    if (this.members().length >= MEMBER_MAX) return new Response("room full", { status: 409 });

    // A client that already has a socket is reconnecting, not joining.
    const known = this.ctx.getWebSockets().some((socket) => {
      const att = socket.deserializeAttachment() as Attachment | null;
      return att?.client === client;
    });

    // 同一客户端重连时顶掉旧连接，避免幽灵成员。
    for (const ws of this.ctx.getWebSockets()) {
      const att = ws.deserializeAttachment() as Attachment | null;
      if (att?.client === client) {
        try {
          ws.close(4001, "replaced");
        } catch {
          // already closed
        }
      }
    }

    const handle = await handleFor(this.meta.secret ?? "", client);
    const pair = new WebSocketPair();
    const server = pair[1];
    this.ctx.acceptWebSocket(server, [`client:${client}`]);
    server.serializeAttachment({
      client,
      handle,
      name,
      avatar,
      host,
      joinedAt: Date.now(),
      messages: 0,
      windowAt: Date.now(),
      shares: {},
      askAt: 0,
    } satisfies Attachment);
    server.send(JSON.stringify({
      t: "welcome",
      serverTime: Date.now(),
      code: this.meta.code,
      you: { client: handle, host },
      members: this.members(),
      playback: this.playback,
      pending: this.pending,
      chat: this.chat,
      // The link for the track that plays, not merely the last one shared,
      // so a late joiner gets the song it is joining; the rest ride along
      // in streams for a seek back.
      stream: this.playback?.track ? this.findStream(this.playback.track.id) : null,
      streams: this.streamList(),
      queue: this.queue,
      // The beat the room wants, so a member is never swept for being quiet
      // between two of its own ticks.
      heartbeatMs: HEARTBEAT_MS,
      features: ["profile", "stream", "desk", "transfer", "stream2", "queue", "playMode"],
    }));
    this.broadcastMembers();
    if (!known) await this.systemChat(`${name} 加入了房间`);
    await this.ctx.storage.delete("emptyAt");
    await this.ctx.storage.setAlarm(Date.now() + 30_000);
    return new Response(null, { status: 101, webSocket: pair[0] });
  }

  async webSocketMessage(ws: WebSocket, raw: string | ArrayBuffer): Promise<void> {
    await this.load();
    if (!this.meta) {
      try {
        ws.close(4004, "ended");
      } catch {
        // already closed
      }
      return;
    }
    const att = ws.deserializeAttachment() as Attachment | null;
    if (!att) return;
    if (typeof raw !== "string") return this.fail(ws, "binary_unsupported");
    if (raw.length > 16 * 1024) return this.fail(ws, "too_large");

    // 简单的每连接限速：一秒最多 20 条。
    const now = Date.now();
    att.messages = now - att.windowAt > 1000 ? 1 : att.messages + 1;
    if (now - att.windowAt > 1000) att.windowAt = now;
    if (att.messages > 20) return this.fail(ws, "rate_limited");
    ws.serializeAttachment(att);

    let msg: Record<string, unknown>;
    try {
      msg = JSON.parse(raw) as Record<string, unknown>;
    } catch {
      return this.fail(ws, "bad_json");
    }

    switch (msg.t) {
      case "ping": {
        ws.send(JSON.stringify({ t: "pong", id: number(msg.id), t0: number(msg.t0), t1: Date.now() }));
        return;
      }
      case "playback": {
        // Dragging a progress bar publishes a burst; ten a second is well
        // above that, and low enough that one member cannot make the room
        // seek for everyone else all evening.
        att.plays = now - (att.playAt ?? 0) > 1000 ? 1 : (att.plays ?? 0) + 1;
        att.playAt = now;
        if (att.plays > 10) return this.fail(ws, "rate_limited");
        ws.serializeAttachment(att);
        const rev = (this.playback?.rev ?? 0) + 1;
        const pb = sanitizePlayback(msg.playback, rev);
        if (!pb) return this.fail(ws, "bad_playback");
        this.playback = pb;
        await this.ctx.storage.put("playback", pb);
        this.broadcast(JSON.stringify({ t: "playback", playback: pb }));
        return;
      }
      case "request": {
        const action = text(msg.text, 16);
        if (action !== "next" && action !== "prev") return this.fail(ws, "bad_request");
        this.broadcast(JSON.stringify({ t: "request", text: action, by: att.handle }));
        return;
      }
      case "chat": {
        const body = cleanText(msg.text);
        if (body === "") return;
        const message: Chat = { id: crypto.randomUUID(), client: att.handle, name: att.name, avatar: att.avatar, text: body, at: Date.now() };
        this.chat.push(message);
        if (this.chat.length > CHAT_KEEP) this.chat = this.chat.slice(-CHAT_KEEP);
        await this.ctx.storage.put("chat", this.chat);
        this.broadcast(JSON.stringify({ t: "chat", message }));
        return;
      }
      // The QQ Music account behind a connection can finish restoring after
      // the room was joined; the member's name and portrait follow it.
      case "profile": {
        const name = cleanName(msg.name, att.name);
        const avatar = cleanAvatar(msg.avatar);
        att.name = name;
        if (avatar !== "") att.avatar = avatar;
        ws.serializeAttachment(att);
        this.broadcastMembers();
        return;
      }
      // A member whose own account may not resolve the room's track asks
      // whoever can for the link; the server passes the question on, and
      // hands over the link it already holds so a late joiner needs no one
      // to answer.
      case "stream.request": {
        const id = Math.floor(number(msg.id));
        if (id <= 0) return this.fail(ws, "bad_track");
        // Answer from what the room already holds, so a member who seeks
        // back needs no one to sign the song again; the others are asked
        // in case the room's link is stale.
        const held = this.findStream(id);
        if (id !== (this.playback?.track?.id ?? 0) && !held) return this.fail(ws, "bad_track");
        if (held) {
          // A link the room already holds is always handed over: that
          // answer costs one message, not a request to everyone.
          try {
            ws.send(JSON.stringify({ t: "stream", stream: held }));
          } catch {
            // a closing socket
          }
        }
        // Asking the room costs everyone a signature, so it is paced; a
        // member who already got the cached link needs no second ask.
        if (now - (att.askAt ?? 0) < 2000) return;
        att.askAt = now;
        ws.serializeAttachment(att);
        this.broadcastExcept(att.client, JSON.stringify({ t: "stream.request", id, by: att.handle }));
        return;
      }
      // Whoever's account can sign the room's track shares the link; any
      // member may, so one membership covers everyone.
      case "stream": {
        const shared = sanitizeStream(msg.stream);
        if (!shared) return this.fail(ws, "bad_stream");
        // Only the song the room plays can be offered: a member must not be
        // able to fill the room's cache with links to anything else.
        if (shared.trackId !== (this.playback?.track?.id ?? 0)) return this.fail(ws, "bad_stream");
        // One share of a song every two seconds is enough: a fresh song
        // is a fresh key, so skipping fast is never held back.
        att.shares ??= {};
        if (now - (att.shares[String(shared.trackId)] ?? 0) < 2000) return this.fail(ws, "rate_limited");
        att.shares[String(shared.trackId)] = now;
        const keys = Object.keys(att.shares);
        if (keys.length > 8) {
          for (const key of keys.sort((a, b) => (att.shares![a] ?? 0) - (att.shares![b] ?? 0)).slice(0, keys.length - 8)) {
            delete att.shares[key];
          }
        }
        ws.serializeAttachment(att);
        // The server names the signer, not the message: one member cannot
        // claim another's membership.
        shared.by = att.handle;
        shared.byName = att.name;
        this.stream = shared;
        this.streams[String(shared.trackId)] = shared;
        this.trimStreams();
        await this.ctx.storage.put("stream", shared);
        await this.ctx.storage.put("streams", this.streams);
        this.broadcast(JSON.stringify({ t: "stream", stream: shared }));
        return;
      }
      // The host mirrors its queue, so a guest plays the same list rather
      // than a single borrowed track.
      case "queue": {
        if (!this.isHost(att)) return this.fail(ws, "not_host");
        const rev = (this.queue?.rev ?? 0) + 1;
        const q = sanitizeQueue(msg.queue, rev);
        if (!q) return this.fail(ws, "bad_queue");
        this.queue = q;
        await this.ctx.storage.put("queue", q);
        this.broadcast(JSON.stringify({ t: "queue", queue: q }));
        return;
      }
      // The host may hand the room to anyone in it. The server remembers
      // who holds it, so the new host's client needs no secret key.
      case "transfer": {
        if (!this.isHost(att)) return this.fail(ws, "not_host");
        const target = text(msg.text, 48);
        if (target === "" || target === att.handle) return this.fail(ws, "bad_target");
        const member = this.members().find((m) => m.client === target);
        const holder = this.ctx.getWebSockets().map((socket) => socket.deserializeAttachment() as Attachment | null).find((a) => a?.handle === target);
        if (!member || !holder) return this.fail(ws, "no_member");
        this.meta.hostClient = holder.client;
        await this.ctx.storage.put("meta", this.meta);
        this.broadcastMembers();
        this.broadcastYou();
        await this.systemChat(`${member.name} 成为了新房主`);
        return;
      }
      case "pending.add": {
        if (this.pending.length >= PENDING_MAX) return this.fail(ws, "pending_full");
        const track = sanitizeTrack(msg.track);
        if (!track) return this.fail(ws, "bad_track");
        this.pending.push({ id: crypto.randomUUID(), track, by: att.handle, byName: att.name, at: Date.now() });
        await this.ctx.storage.put("pending", this.pending);
        this.broadcast(JSON.stringify({ t: "pending", pending: this.pending }));
        return;
      }
      case "pending.remove": {
        const id = text(msg.text, 64);
        const item = this.pending.find((p) => p.id === id);
        if (!item) return;
        if (!this.isHost(att) && item.by !== att.handle) return this.fail(ws, "not_allowed");
        this.pending = this.pending.filter((p) => p.id !== id);
        await this.ctx.storage.put("pending", this.pending);
        this.broadcast(JSON.stringify({ t: "pending", pending: this.pending }));
        return;
      }
      case "pending.play": {
        // Any member may start a requested song. The host's queue takes
        // it and publishes the anchor, so the queue the room plays from
        // stays the host's.
        const id = text(msg.text, 64);
        const item = this.pending.find((p) => p.id === id);
        if (!item) return;
        this.pending = this.pending.filter((p) => p.id !== id);
        await this.ctx.storage.put("pending", this.pending);
        this.broadcast(JSON.stringify({ t: "pending", pending: this.pending }));
        this.broadcast(JSON.stringify({ t: "play", track: item.track, by: att.handle }));
        return;
      }
      case "kick": {
        if (!this.isHost(att)) return this.fail(ws, "not_host");
        const target = text(msg.text, 48);
        if (target === att.handle || target === "") return;
        for (const other of this.ctx.getWebSockets()) {
          const member = other.deserializeAttachment() as Attachment | null;
          if (member?.handle === target && member.client) {
            this.removed.add(member.client);
            try {
              other.close(4003, "kicked");
            } catch {
              // already closed
            }
          }
        }
        this.broadcastMembers();
        return;
      }
      case "bye": {
        this.removed.add(att.client);
        try {
          ws.close(4005, "bye");
        } catch {
          // already closed
        }
        this.broadcastMembers();
        return;
      }
      case "end": {
        if (!this.isHost(att)) return this.fail(ws, "not_host");
        await this.closeRoom();
        return;
      }
      default:
        return this.fail(ws, "unknown_type");
    }
  }

  async webSocketClose(ws: WebSocket, code: number, reason: string, wasClean: boolean): Promise<void> {
    const att = ws.deserializeAttachment() as Attachment | null;
    try {
      ws.close(code === 1000 ? 1000 : 1001, reason);
    } catch {
      // already closed
    }
    this.broadcastMembers();
    // A replaced or removed socket is not someone leaving; neither is a
    // reconnect, which already has another socket of the same client.
    const quiet = code === 4001 || code === 4003 || code === 4004 || code === 4005;
    const elsewhere = this.ctx.getWebSockets().some((socket) => {
      const other = socket.deserializeAttachment() as Attachment | null;
      return other?.client === att?.client;
    });
    if (att && !quiet && !elsewhere && !this.removed.has(att.client)) {
      this.removed.add(att.client);
      this.broadcastMembers();
      await this.systemChat(`${att.name} 离开了房间`);
    }
    if (this.members().length === 0) {
      if ((await this.ctx.storage.get("emptyAt")) === undefined) {
        await this.ctx.storage.put("emptyAt", Date.now());
      }
      await this.ctx.storage.setAlarm(Date.now() + IDLE_MS);
    } else {
      await this.ctx.storage.setAlarm(Date.now() + 30_000);
    }
  }

  async webSocketError(ws: WebSocket): Promise<void> {
    const att = ws.deserializeAttachment() as Attachment | null;
    if (att) this.removed.add(att.client);
    this.broadcastMembers();
  }

  // alarm sweeps members whose pings stopped, then keeps the room alive
  // while anyone remains; an empty room is kept for a day, then deleted.
  async alarm(): Promise<void> {
    await this.load();
    const now = Date.now();
    let active = 0;
    for (const ws of this.ctx.getWebSockets()) {
      const att = ws.deserializeAttachment() as Attachment | null;
      if (!att || this.removed.has(att.client)) continue;
      if (now - att.windowAt > 60_000) {
        this.removed.add(att.client);
        try {
          ws.close(4006, "stale");
        } catch {
          // already closed
        }
        continue;
      }
      active++;
    }
    if (active > 0) {
      await this.ctx.storage.delete("emptyAt");
      const hostPresent = this.ctx.getWebSockets().some((socket) => {
        const att = socket.deserializeAttachment() as Attachment | null;
        return att !== null && this.isHost(att) && !this.removed.has(att.client);
      });
      if (hostPresent) {
        await this.ctx.storage.delete("hostGoneAt");
      } else if (this.meta) {
        const goneAt = await this.ctx.storage.get<number>("hostGoneAt");
        if (goneAt === undefined) {
          await this.ctx.storage.put("hostGoneAt", now);
        } else if (now - goneAt >= HOST_GRACE_MS) {
          await this.closeRoom();
          return;
        }
      }
      this.broadcastMembers();
      await this.ctx.storage.setAlarm(now + 30_000);
      return;
    }
    if (this.members().length > 0) {
      await this.ctx.storage.setAlarm(now + 30_000);
      return;
    }
    const emptyAt = await this.ctx.storage.get<number>("emptyAt");
    if (emptyAt !== undefined && now - emptyAt >= IDLE_MS) {
      await this.ctx.storage.deleteAll();
      this.loaded = false;
      this.meta = null;
      this.playback = null;
      this.pending = [];
      this.chat = [];
      this.stream = null;
      this.streams = {};
      this.queue = null;
      this.removed.clear();
      return;
    }
    if (emptyAt === undefined) {
      await this.ctx.storage.put("emptyAt", now);
    }
    await this.ctx.storage.setAlarm(now + IDLE_MS);
  }

  private async closeRoom(): Promise<void> {
    this.broadcast(JSON.stringify({ t: "end" }));
    for (const ws of this.ctx.getWebSockets()) {
      try {
        ws.close(4004, "ended");
      } catch {
        // already closed
      }
    }
    await this.ctx.storage.deleteAll();
    this.meta = null;
    this.playback = null;
    this.pending = [];
    this.chat = [];
    this.stream = null;
    this.streams = {};
    this.queue = null;
    this.removed.clear();
    this.loaded = false;
  }

  // freshStreams drops what has aged out: a vkey is short-lived, and a room
  // kept alive for a day must not hand out yesterday's link.
  private freshStreams(source: Record<string, Stream>): Record<string, Stream> {
    const cut = Date.now() - STREAM_TTL;
    const kept: Record<string, Stream> = {};
    for (const [key, value] of Object.entries(source)) {
      if (value && value.url && (value.at ?? 0) > cut) kept[key] = value;
    }
    return kept;
  }

  // trimStreams keeps the most recent links only.
  private trimStreams(): void {
    const entries = Object.entries(this.streams).sort((a, b) => (a[1].at ?? 0) - (b[1].at ?? 0));
    while (entries.length > STREAM_KEEP) {
      const [key] = entries.shift()!;
      delete this.streams[key];
    }
  }

  // findStream is the link the room holds for a track, if it is still good.
  private findStream(id: number): Stream | null {
    this.streams = this.freshStreams(this.streams);
    return this.streams[String(id)] ?? (this.stream?.trackId === id ? this.stream : null);
  }

  // streamList is every link the room still holds, for the welcome.
  private streamList(): Stream[] {
    return Object.values(this.freshStreams(this.streams));
  }

  // isHost reads who holds the room from the room itself, so a hand-over
  // takes effect for both clients at once, not only on reconnect.
  private isHost(att: Attachment): boolean {
    const holder = this.meta?.hostClient ?? "";
    if (holder !== "") return att.client === holder;
    return att.host === true;
  }

  private members() {
    const holder = this.meta?.hostClient ?? "";
    const byClient = new Map<string, { client: string; name: string; avatar: string; host: boolean; joinedAt: number }>();
    for (const ws of this.ctx.getWebSockets()) {
      const att = ws.deserializeAttachment() as Attachment | null;
      if (!att || this.removed.has(att.client)) continue;
      const host = holder !== "" ? att.client === holder : att.host === true;
      const existing = byClient.get(att.client);
      if (!existing || att.joinedAt > existing.joinedAt) {
        byClient.set(att.client, { client: att.handle, name: att.name, avatar: att.avatar, host, joinedAt: att.joinedAt });
      }
    }
    return Array.from(byClient.values()).sort((a, b) => a.joinedAt - b.joinedAt);
  }

  // broadcastYou tells each member whether the room is theirs now.
  private broadcastYou(): void {
    for (const ws of this.ctx.getWebSockets()) {
      const att = ws.deserializeAttachment() as Attachment | null;
      if (!att) continue;
      try {
        ws.send(JSON.stringify({ t: "you", you: { client: att.handle, host: this.isHost(att) } }));
      } catch {
        // a closing socket
      }
    }
  }

  // systemChat records a line the room says itself, as QQ 音乐 does for
  // who came, who left and who holds it.
  private async systemChat(text: string): Promise<void> {
    const message: Chat = {
      id: crypto.randomUUID(),
      client: "room",
      name: "一起听",
      avatar: "",
      text,
      at: Date.now(),
      sys: true,
    };
    this.chat.push(message);
    if (this.chat.length > CHAT_KEEP) this.chat = this.chat.slice(-CHAT_KEEP);
    await this.ctx.storage.put("chat", this.chat);
    this.broadcast(JSON.stringify({ t: "chat", message }));
  }

  private broadcastMembers(): void {
    this.broadcast(JSON.stringify({ t: "members", members: this.members() }));
  }

  private broadcast(payload: string): void {
    for (const ws of this.ctx.getWebSockets()) {
      try {
        ws.send(payload);
      } catch {
        // a closing socket
      }
    }
  }

  // broadcastExcept leaves out one member: the one who asked, or who knows.
  private broadcastExcept(client: string, payload: string): void {
    for (const ws of this.ctx.getWebSockets()) {
      const att = ws.deserializeAttachment() as Attachment | null;
      if (att?.client === client) continue;
      try {
        ws.send(payload);
      } catch {
        // a closing socket
      }
    }
  }

  private fail(ws: WebSocket, code: string): void {
    try {
      ws.send(JSON.stringify({ t: "error", code }));
    } catch {
      // a closing socket
    }
  }
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    const now = Date.now();
    const ip = request.headers.get("cf-connecting-ip") ?? "unknown";
    if (request.method === "OPTIONS") {
      // The native clients need no preflight, and nothing here is meant to
      // be driven by a web page.
      return new Response(null, { status: 204 });
    }
    if (request.url.length > 2048) return new Response("uri too long", { status: 414 });
    // A browser always names its origin; a native client does not. Refusing
    // it keeps a page from quietly driving this relay for a visitor.
    if (request.headers.get("origin") !== null && url.pathname !== "/") {
      return new Response("forbidden", { status: 403 });
    }
    if (url.pathname === "/health") return json({ ok: true, time: now });
    if (url.pathname === "/") {
      return new Response(HOME, {
        headers: {
          "content-type": "text/html; charset=utf-8",
          "content-security-policy": "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
          "x-content-type-options": "nosniff",
          "referrer-policy": "no-referrer",
        },
      });
    }
    // Does a room with this code still exist? Joining asks first, so a typo
    // or an expired room is answered at once, not by a failing socket.
    if (url.pathname === "/room" && request.method === "GET") {
      if (!allowClient(ip, "check", 60, now)) return json({ error: "rate_limited" }, 429);
      const code = (url.searchParams.get("code") ?? "").toUpperCase();
      if (!new RegExp(`^[${CODE_ALPHABET}]{${CODE_LENGTH}}$`).test(code)) {
        return json({ exists: false }, 400);
      }
      try {
        const stub = env.ROOMS.get(env.ROOMS.idFromName(code));
        const res = await stub.fetch("https://room/check", { signal: AbortSignal.timeout(6000) });
        if (!res.ok) return json({ exists: false }, 502);
        return json(await res.json(), 200);
      } catch {
        return json({ exists: false }, 502);
      }
    }
    if (url.pathname === "/room" && request.method === "POST") {
      if (!allowClient(ip, "create", 10, now)) return json({ error: "rate_limited" }, 429);
      if (!allowRoomCreate(now)) return json({ error: "too_many_rooms" }, 429);
      const hostKey = randomString(24);
      let lastStatus = 502;
      // A cold Durable Object can refuse its first initialisation, and two
      // clients creating rooms at once can collide on a code: try another
      // code, and another attempt, before telling the listener it failed.
      for (let attempt = 0; attempt < 8; attempt++) {
        const code = randomCode();
        const stub = env.ROOMS.get(env.ROOMS.idFromName(code));
        try {
          const res = await stub.fetch("https://room/init", {
            method: "POST",
            body: JSON.stringify({ code, hostKey }),
            signal: AbortSignal.timeout(8000),
          });
          if (res.status === 409) continue;
          if (!res.ok) {
            lastStatus = res.status;
            await res.text().catch(() => "");
            await sleep(120 + attempt * 90);
            continue;
          }
          const wsBase = `${url.protocol === "https:" ? "wss" : "ws"}://${url.host}/ws`;
          return json({ code, hostKey, ws: `${wsBase}?code=${code}` });
        } catch {
          lastStatus = 504;
          await sleep(120 + attempt * 90);
        }
      }
      return json({ error: "room_create_failed", status: lastStatus }, 503);
    }
    if (url.pathname === "/ws") {
      if (!allowClient(ip, "ws", 30, now)) return new Response("rate limited", { status: 429 });
      if (request.headers.get("Upgrade") !== "websocket") return json({ error: "expected websocket" }, 400);
      const code = (url.searchParams.get("code") ?? "").toUpperCase();
      if (!new RegExp(`^[${CODE_ALPHABET}]{${CODE_LENGTH}}$`).test(code)) {
        return new Response("bad code", { status: 400 });
      }
      const stub = env.ROOMS.get(env.ROOMS.idFromName(code));
      return stub.fetch(request);
    }
    return new Response("not found", { status: 404 });
  },
};