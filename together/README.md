# 一起听房间服务

Cloudflare Worker + Durable Object，一台 Durable Object 是一间房：成员、播放锚点、点歌台和最近 100 条聊天都放在里面。服务器只同步控制信令，音频由各自的客户端从 QQ 音乐取流。

## 接口

| 路径 | 用途 |
|---|---|
| `POST /room` | 创建房间，返回 `{code, hostKey, ws}`；服务端会换房间号重试，冷启动或撞号不再直接失败 |
| `GET /room?code=` | 查询房间是否还在，加入前先问一次 |
| `GET /ws?code=&client=&name=&avatar=[&hostKey=]` | WebSocket 房间连接 |
| `GET /health` | 健康检查 |
| `GET /` | 使用说明页 |

`hostKey` 只在创建房间时返回一次，房主客户端用它执行管理动作：点歌台播放、移人、结束房间；访客连接不带。播放锚点任何成员都可以发，服务器按到达顺序后写覆盖。

`avatar` 只接受 `qlogo.cn` / `gtimg.cn` 的 http(s) 地址，客户端据此显示 QQ 音乐头像；名字来自 QQ 音乐登录态，未登录时才是本地昵称。

## 消息

客户端 → 服务端：`ping`、`playback`（任何成员）、`request`（next/prev）、`chat`、`profile`（改名/换头像）、`stream.request`（求一条播放直链）、`pending.add`、`pending.remove`、`pending.play`（任何成员）、`kick`（仅房主）、`end`（仅房主）。

服务端 → 客户端：`welcome`（带 `features`）、`members`、`playback`（带服务器时间戳）、`request`、`stream.request`、`play`（点歌台开始播放）、`pending`、`chat`、`pong`、`end`、`error`。

## 同步与控制

- 房主每 4 秒发一次播放锚点 `{track, playing, positionMs}`；服务器收到时写入自己的 `updatedAt`，客户端不依赖房主时钟。
- 房间里任何一个成员解析出当前歌曲的播放直链，都会通过聊天里的隐藏信令分享；没有会员的一方直接复用这条直链，所以“谁有会员谁分享”，不要求房主是会员。
- 谁的账号都解析不出来时，客户端发 `stream.request`，其他成员收到后重新分享一次。
- 客户端每 15 秒 `ping/pong` 估算服务器时钟偏移，取最小 RTT 样本；欢迎消息的 `serverTime` 用于第一时间播种时钟。
- 访客的目标位置 = `positionMs + (serverNow - updatedAt)`（播放中）；偏差超过 400ms 才 seek，且 5 秒冷却；加载中的 seek 只更新落点，不计入冷却，加载完成后立即校正。
- 播放/暂停/拖动进度由任何成员发起，服务器按到达顺序后写覆盖；客户端应用远端状态时置 `applying`，不回声发布。
- 下一首/上一首由访客发 `request`，房主队列执行后再发布新锚点；选歌仍由房主队列与点歌台决定。
- 点歌台的「播放」任何成员都能点：服务器广播 `play`，房主把它插进自己的队列并立即播放，再发布锚点。旧版服务端没有 `play` 时，客户端退回“本机播放并发布锚点”。
- 房主每 4 秒重发锚点；访客不发表周期锚点，只在本地用户操作后发布。

## 部署

```bash
cd together
npx wrangler deploy
```

`wrangler.jsonc` 默认部署到 `together.bdprk.com`（自定义域名）。如果域名不在当前 Cloudflare 账号，删掉 `routes` 一行，使用 `lilt-together.<account>.workers.dev`。

客户端默认连接 `wss://together.bdprk.com`；可在 `settings.json` 里用 `togetherRelay` 改成别的地址，本地调试用 `http://127.0.0.1:8787`。

## 本地联调

```bash
cd together
npx wrangler dev --ip 127.0.0.1 --port 8787

# 另一个终端
cd ..
go run ./cmd/together-probe -relay http://127.0.0.1:8787
go run ./cmd/together-probe -relay https://together.bdprk.com
```

探针会建房、加入房主/访客/第三人，校验同步、共享控制、聊天、点歌、踢人、房主结束房间、房主断线宽限与头像透传。

其他模式：`-join CODE` 只作访客加入（可加 `-chat 文本` 发消息）；`-control CODE` 加入后把进度设为 30 秒并暂停；`-stale` 验证无心跳成员清理；`-hostgone` 验证房主掉线宽限；`-full` 验证第 11 人被拒；`-stay -track ID` 当房主并循环发布一首歌，供客户端测试；`-soak 分钟` 房主每 5 秒发一次锚点，验证长时间连接、时钟和房间稳定性。

## 数据与清理

- 房间空置 24 小时后自动删除；房主点离开或退出 App 会立即发送 `end`，房间随即删除；
- 房主断线超过 2 分钟没有回来，房间自动结束，访客会收到“房主结束了房间”；
- 成员上限 10、点歌台 50 首、聊天保留 100 条；
- 客户端每 15 秒校准一次服务器时钟，房主每 4 秒发一次播放锚点；
- 两人都能播放、暂停、拖动进度；下一首/上一首由访客发 `request`、房主队列执行；
- 选歌仍由房主队列与点歌台决定，访客的播放列表/电台操作在客户端被拦下。

## 常见问题

- **访客没有声音**：先看访客端是否提示“可能需要 VIP”。房间里只要有一个人的账号能签出这首歌的直链，链接就会分享给所有人；两个人都不是会员时，会员曲目仍然放不了。
- **点了下一首没反应**：下一首由房主队列执行；确认房主客户端仍在线。房主断线 2 分钟房间会结束。
- **点歌台点了播放没反应**：旧版服务端只允许房主播放，且只把歌排到下一首；升级服务端后任何成员点播放都会立刻切歌。
- **进度短暂有偏差**：客户端每秒校正一次，超过 400ms 才 seek，且有 5 秒冷却；切歌加载完成后会立即再校准一次。
- **房间号无效**：房间号 8 位，字母数字，不含 I/O/0/1；空置 24 小时后失效。
- **播放队列丢了**：`session.json` 旁保留 `session.json.bak`；主文件损坏时客户端会自动回退。