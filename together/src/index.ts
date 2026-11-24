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

interface Chat {
  id: string;
  client: string;
  name: string;
  avatar: string;
  text: string;
  at: number;
}

interface Meta {
  code: string;
  hostKey: string;
  createdAt: number;
}

interface Attachment {
  client: string;
  name: string;
  avatar: string;
  host: boolean;
  joinedAt: number;
  messages: number;
  windowAt: number;
}

const CHAT_KEEP = 100;
const PENDING_MAX = 50;
const MEMBER_MAX = 10;
const CODE_LENGTH = 8;
const CODE_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const IDLE_MS = 24 * 60 * 60 * 1000;
// How long a room lives on after its host disappears, so a short
// disconnect can reconnect; a crashed host ends it after this.
const HOST_GRACE_MS = 2 * 60 * 1000;
const TEXT_MAX = 500;
const NAME_MAX = 24;

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

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json", "cache-control": "no-store", "access-control-allow-origin": "*" },
  });
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
function cleanAvatar(value: unknown): string {
  if (typeof value !== "string" || value === "") return "";
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return "";
  }
  if (url.protocol !== "https:" && url.protocol !== "http:") return "";
  const host = url.hostname.toLowerCase();
  const ok = host === "qlogo.cn" || host.endsWith(".qlogo.cn") || host === "gtimg.cn" || host.endsWith(".gtimg.cn");
  return ok ? url.toString().slice(0, 500) : "";
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
      picUrl: text(albumRaw.picUrl, 500),
    },
    duration,
    fee: Math.max(0, Math.min(Math.floor(number(v.fee)), 9)),
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
  private removed = new Set<string>();
  private loaded = false;

  constructor(ctx: DurableObjectState, env: Env) {
    this.ctx = ctx;
    this.env = env;
  }

  private async load(): Promise<void> {
    if (this.loaded) return;
    const [meta, playback, pending, chat] = await Promise.all([
      this.ctx.storage.get<Meta>("meta"),
      this.ctx.storage.get<Playback>("playback"),
      this.ctx.storage.get<Pending[]>("pending"),
      this.ctx.storage.get<Chat[]>("chat"),
    ]);
    this.meta = meta ?? null;
    this.playback = playback ?? null;
    this.pending = pending ?? [];
    this.chat = chat ?? [];
    this.loaded = true;
  }

  async fetch(request: Request): Promise<Response> {
    await this.load();
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
      this.meta = { code, hostKey, createdAt: Date.now() };
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
    const hostKey = url.searchParams.get("hostKey") ?? "";
    const host = hostKey !== "" && hostKey === this.meta.hostKey;
    this.removed.delete(client);
    if (host) await this.ctx.storage.delete("hostGoneAt");

    if (this.members().length >= MEMBER_MAX) return new Response("room full", { status: 409 });

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

    const pair = new WebSocketPair();
    const server = pair[1];
    this.ctx.acceptWebSocket(server, [`client:${client}`]);
    server.serializeAttachment({
      client,
      name,
      avatar,
      host,
      joinedAt: Date.now(),
      messages: 0,
      windowAt: Date.now(),
    } satisfies Attachment);
    server.send(JSON.stringify({
      t: "welcome",
      serverTime: Date.now(),
      code: this.meta.code,
      you: { client, host },
      members: this.members(),
      playback: this.playback,
      pending: this.pending,
      chat: this.chat,
      features: ["profile", "stream", "desk"],
    }));
    this.broadcastMembers();
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
        this.broadcast(JSON.stringify({ t: "request", text: action, by: att.client }));
        return;
      }
      case "chat": {
        const body = cleanText(msg.text);
        if (body === "") return;
        const message: Chat = { id: crypto.randomUUID(), client: att.client, name: att.name, avatar: att.avatar, text: body, at: Date.now() };
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
      // whoever can for the link; the server only passes the question on.
      case "stream.request": {
        const id = Math.floor(number(msg.id));
        if (id <= 0) return this.fail(ws, "bad_track");
        this.broadcastExcept(att.client, JSON.stringify({ t: "stream.request", id, by: att.client }));
        return;
      }
      case "pending.add": {
        if (this.pending.length >= PENDING_MAX) return this.fail(ws, "pending_full");
        const track = sanitizeTrack(msg.track);
        if (!track) return this.fail(ws, "bad_track");
        this.pending.push({ id: crypto.randomUUID(), track, by: att.client, byName: att.name, at: Date.now() });
        await this.ctx.storage.put("pending", this.pending);
        this.broadcast(JSON.stringify({ t: "pending", pending: this.pending }));
        return;
      }
      case "pending.remove": {
        const id = text(msg.text, 64);
        const item = this.pending.find((p) => p.id === id);
        if (!item) return;
        if (!att.host && item.by !== att.client) return this.fail(ws, "not_allowed");
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
        this.broadcast(JSON.stringify({ t: "play", track: item.track, by: att.client }));
        return;
      }
      case "kick": {
        if (!att.host) return this.fail(ws, "not_host");
        const target = text(msg.text, 48);
        if (target === att.client || target === "") return;
        this.removed.add(target);
        for (const other of this.ctx.getWebSockets()) {
          const member = other.deserializeAttachment() as Attachment | null;
          if (member?.client === target) {
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
        if (!att.host) return this.fail(ws, "not_host");
        await this.closeRoom();
        return;
      }
      default:
        return this.fail(ws, "unknown_type");
    }
  }

  async webSocketClose(ws: WebSocket, code: number, reason: string, wasClean: boolean): Promise<void> {
    try {
      ws.close(code === 1000 ? 1000 : 1001, reason);
    } catch {
      // already closed
    }
    this.broadcastMembers();
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
        return att?.host === true && !this.removed.has(att.client);
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
    this.removed.clear();
    this.loaded = false;
  }

  private members() {
    const byClient = new Map<string, { client: string; name: string; avatar: string; host: boolean; joinedAt: number }>();
    for (const ws of this.ctx.getWebSockets()) {
      const att = ws.deserializeAttachment() as Attachment | null;
      if (!att || this.removed.has(att.client)) continue;
      const existing = byClient.get(att.client);
      if (!existing || att.joinedAt > existing.joinedAt) {
        byClient.set(att.client, { client: att.client, name: att.name, avatar: att.avatar, host: att.host, joinedAt: att.joinedAt });
      }
    }
    return Array.from(byClient.values()).sort((a, b) => a.joinedAt - b.joinedAt);
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
    if (request.method === "OPTIONS") {
      return new Response(null, {
        status: 204,
        headers: {
          "access-control-allow-origin": "*",
          "access-control-allow-methods": "GET,POST,OPTIONS",
          "access-control-allow-headers": "content-type",
        },
      });
    }
    if (url.pathname === "/health") return json({ ok: true, time: Date.now() });
    if (url.pathname === "/") {
      return new Response(HOME, { headers: { "content-type": "text/html; charset=utf-8" } });
    }
    // Does a room with this code still exist? Joining asks first, so a typo
    // or an expired room is answered at once, not by a failing socket.
    if (url.pathname === "/room" && request.method === "GET") {
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