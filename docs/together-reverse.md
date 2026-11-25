# QQ 音乐「一起听」怎么工作，Lilt 复刻了哪些

这份笔记来自对 `D:\用户\trevv\下载\QQMusic`（QQ 音乐 PC 22.82）的静态分析：字符串表、RTTI 名、XML 资源名与调试日志。目的只有一个：让 Lilt 的「一起听」在操作和界面上像 QQ 音乐那一页，同时把「两个人各自播各自的」改成一个房间一台播放器。

## 1. 逆向看到的对象与流程

`QQMusic.dll` 里有一个独立的逻辑类，类名与调试串把它说得很清楚：

```
CListenTogetherDataLogic
  OnStartListenTogether
  OnLTRoomEntry_CGI / OnLTRoomQuit_CGI
  OnLTRoomHeartBeat_CGI / OnLTRoomSongOper_CGI
  OnRoomMsgWnsPush(cmd, nSubCmd, showID)
  OnServerChangePlayOffset
  ParseRoomCreateRsp / ParseRoomSongOperRsp
  DealServerError(nErrCode)
```

也就是说，QQ 音乐的「一起听」由四类 CGI 加一路推送构成：

| 名称 | 作用 | Lilt 对应 |
|---|---|---|
| `LTRoomEntry` | 进房/建房，拿到房间与当前房间状态 | `POST /room`、`welcome` |
| `LTRoomQuit` | 退房 | `bye` + 关连接 |
| `LTRoomHeartBeat` | 心跳，服务端据此判断人在不在 | `ping`/`pong` 每 15 秒，服务端 60 秒清扫 |
| `LTRoomSongOper` | 歌单操作：切歌、点歌、随机、播放/暂停 | `playback`、`request`、`pending.*`、`queue` |
| WNS 推送 `OnRoomMsgWnsPush` | 服务端主动广播房间变化 | WebSocket `playback/members/pending/chat/stream/queue/you` |

服务端广播的消息体在字符串表里留下了字段名，正好拼出房间状态机：

```
roomInfo  songList  songIndex  songStartTime  songOffset  songDuration
songPlayVersion  songRandomIndex  maxSongNumber
playingSongInfo  songPlayingInfo  songPlaying  operUin  songInfo
```

- `songList` + `songIndex`：**房间有一份所有人的共同队列**，不是一人一首。
- `songStartTime` / `songOffset` / `songDuration`：服务端记「这首歌从服务器时间几点开始、已经走了多少」。
- `songPlayVersion`：每次换歌/跳进度自增，客户端据此丢掉旧消息（Lilt 里是锚点的 `rev`）。
- `operUin`：谁做的这次操作，用来在界面上标「谁切了歌」。
- `OnServerChangePlayOffset`：服务端认为你的进度不对时，直接下发新的 offset —— **位置以服务端为准**，不是以房主本机时钟为准。

界面侧：

```
ListenTogetherFrame   SongViewLTTheme   btnCloseListenTogether
平台 XML：platformxtml:QQMusic_Playlist.xml|PlayListViewNew 等
网页：/frame/listen_together/invitation  /summary  /confirm
```

即一间房有自己的主题（`SongViewLTTheme`）、自己的播放列表视图、邀请/回顾/确认三个网页。Lilt 用原生 UI 复刻这三块：房间页顶部成员条 + 邀请复制、黑胶唱机与歌词、点歌台/队列。

## 2. 由此得出的四条规则（Lilt 已照此实现）

1. **音频永不上传**。QQ 音乐只同步控制信令（`songList/songIndex/songOffset`），每个客户端自己取流。Lilt 同样只同步锚点，音频各自从 QQ 音乐签链接。
2. **位置以服务端时间为准**。锚点带服务器收到时的 `updatedAt`，客户端用 ping 估出的服务器时钟算「现在应该到哪」，偏差超过阈值才 seek，避免来回拉扯。
3. **房间有一份共同队列**。所以「下一首」不该是访客本机自己往后跳，而是回到房主的队列执行，再把新锚点广播出来。
4. **操作权是房间的属性**。谁持有房间（房主）决定队列与选歌；房主不在时房间有宽限期。

## 3. 会员共享：为什么「一人有会员两人都能听」

QQ 音乐签出的播放直链（`vkey`）是发给请求它的那个账号的，但链接本身不校验「谁在放」：拿到链接的人都能下载同一份音频文件。因此房间不需要共享账号，只需要**共享一条链接**：

```
谁的账号签得出 -> 把 {trackId, url, type, size, level} 交给房间 -> 其他人直接复用
```

Lilt 原来的实现把这条链接塞进聊天（隐藏分片），只在会解析的一方是房主时才生效，而且聊天有 500 字节上限，长 FLAC 链接要切很多片；现在改为房间协议的一条 `stream` 消息：

- 任何成员解析出当前歌曲的直链都会分享（`stream`）；
- 服务端按曲目保存（最近 40 首、40 分钟），`welcome` 同时下发当前那条 `stream` 和全部有效的 `streams`，新进房的人不必等任何人；
- 播不出来的成员发 `stream.request`，服务端先把手上那条直接回给它；其他成员收到后除了重发自己手上的，还会**用自己的账号当场调签链接接口**——所以没有会员的人点歌，有会员的那台即使没在播这首也能供源；
- 服务端给链接署名（`by`/`byName`），客户端显示「音源来自「某某」的会员」；
- 客户端 `player.waitSharedStream` 从「每 150ms 轮询」改成「链接一到就唤醒」，所以换歌时不会先报错再等；
- 取流被 CDN 以 401/403 拒绝时，带 `Referer: https://y.qq.com/` 重试一次。

### 试听版：非会员最容易踩的坑

QQ 对没有权限的账号，很多时候不是返回「没有链接」，而是**给一条 30 秒试听**，而接口里的 `size` 仍然是整首的大小。两个人都用这条链接，就会出现「各播各的」：30 秒一到，一边停了、一边还在放。所以：

- 对 `fee=1/4` 的曲目，签完链接后用 `Range: bytes=0-1` 问 CDN 真实的 `Content-Range` 总长度；
- 真实长度不足整首一半 → 判为试听（`Stream.Trial`）；
- 试听**不分享、不进缓存**；非会员端先等房间里的完整链接（`trialWait`），等不到才退回试听并在界面说明；
- 房间已有链接时，两端都优先用房间那一份；只有收到 `stream.request`（说明现有链接不好用）才允许用自己的链接重新出价，避免两个会员每秒互相覆盖。

## 4. 同步问题的真正原因与修法

原来的代码「房主每 4 秒发一次锚点、访客每秒跟一次」，理论上能同步，但实际体验像两个独立播放器，原因有三个：

1. **播放器变化只挂了 4 秒心跳**。`playerChanged` 里房主直接 `Publish`，但 `publishDue` 会因为「同曲同状态且不足 4 秒」把事件丢掉；拖动进度条时这一串 seek 也会被压成一次或不发。现在房主与访客的用户操作都走 `togetherPublishSoon`：60–120ms 合并一次，立即发。
2. **访客只有一首「借来的歌」**。访客的队列被替换成单曲 `PlayQueue([track], 0)`，所以房主下一首时访客要重新加载、位置也对不上。现在房主把自己的队列镜像到房间（`queue` 消息），访客直接建立同一份队列并在正确索引落点。
3. **取流失败时没有真正的等待语义**。访客先加载、失败、报「可能需要 VIP」，链接随后才到；现在玩家在房间里会等共享链接（最多 12 秒），链接一到就继续加载，不再先出错误提示。

另外补了 QQ 音乐式的可见状态：

- 顶部「已同步 / 同步中…」小胶囊（按 400ms 漂移阈值判定）；
- 歌曲下方「音源来自房间里的会员」；
- 房主名字 + 「XX 的一起听」房间标题、成员头像条、成员菜单里的「转让房主」。

## 5. 房主转让

QQ 音乐把「谁持有房间」放在房间状态里，而不是放在连接本身（否则一断线房间就死）。Lilt 的服务端同样把持有者记在房间里：

- `meta.hostClient` 记录当前房主；
- `transfer` 消息由房主发出，服务端校验后改写 `hostClient`，然后给每个人单独发 `you`；
- 新房主立刻发布一次锚点，房间继续放，不中断；
- 创建房间时的 `hostKey` 只在「还没人接管」时有效，转让后旧房主既不能结束房间也不能踢人；
- 成员菜单里出现「转让房主」，只有房主能看到。

## 6. 仍未复刻的部分

- QQ 音乐房间里的「随机播放」「一起听回顾（summary）」网页、房间封面与歌单主题：需要服务端额外状态，先留空。
- QQ 音乐用 WNS 长连接推送；Lilt 用 WebSocket + Durable Object，语义等价（增量广播 + 心跳 + 服务端时间戳）。
## 7. 第二轮静态分析：房间协议的全貌

第一轮的结论来自字符串表；这一轮把 `QQMusic.dll` 的 `.text` 与 `.rdata` 对照着读，把「一起听」房间的数据结构、推送命令、错误码补全了。方法记在这里，方便下次再核对：

```bash
# 1) 从 UTF-16 字符串表里找出房间相关的类名、断言、字段
strings -e l -n 5 QQMusic.dll | grep -i "LTRoom\|ListenTogether"
# 2) 找到字段/日志串在 .rdata 里的地址，再在 .text 里反查谁 push 了它（xref）
objdump -d --start-address=0x1023a000 --stop-address=0x1023d600 -M intel QQMusic.dll
# 3) 顺着 xref 反汇编：OnRoomMsgWnsPush / DealServerError / ParseRoomSongOperRsp
```

`CListenTogetherDataLogic` 这一块没有加壳，断言串都在，所以从字符串地址反查调用点可以一路读到分派逻辑。

### 7.1 房间状态就是一份 JSON（`roomInfo`）

字段名都在 `.rdata` 里连成一片，正好是 QQ 音乐服务端下发的房间对象：

| 字段 | 含义 | Lilt 对应 |
|---|---|---|
| `roomInfo` | 房间本体 | 房间对象 |
| `showID` / `groupID` / `groupCMD` | 房间对外号、群号与群命令 | `code` |
| `createUin` / `createTime` / `closeTime` / `addedUin` | 建房人、建房/关房时间、加入者 | `Host`、`gate` |
| `joinUin` / `updateTimeST` / `startTimeST` | 最近加入者、更新时间、计时起点 | 锚点 `updatedAt` |
| `songList` + `songIndex` | **共同队列**与当前下标 | `queue` |
| `songPlaying` / `songPlayingInfo` / `playingSongInfo` / `songInfo` / `trackInfo` | 当前曲目（几个视图各取所需） | `playback.track` |
| `playStatus` | 播放/暂停 | `playback.playing` |
| `songStartTime` / `songOffset` / `songDuration` | 服务端时钟的起点、已走时长、总时长 | `updatedAt` + `positionMs` |
| `songPlayVersion` / `mediaVersion` | 版本号，用来丢弃旧推送 | `rev` |
| `playMode` / `songRandomIndex` | 播放模式（顺序/随机）与随机下标 | `queue.shuffle` |
| `maxSongNumber` | 队列上限 | 服务端 `QUEUE_MAX`/`PENDING_MAX` |
| `heartBeatInterval` | **心跳间隔由房间决定** | `Welcome.heartbeatMs` |
| `supportChangeScene` | 换场景（页面）房间不散 | 常驻连接 |
| `auth` | 播放权限 | 会员判定 |
| `auditionStart` | 试听片段起点 | CDN Range 探测 |
| `card` / `endCard` / `endToast` | 房间卡片、结束卡片与结束提示 | 结束卡片 |
| `users[].encryptUin` / `nick` / `headUrl` | 成员（加密 uin、昵称、头像） | `Member` |
| `operUin` | 这次操作是谁做的 | `Chat.by` / `Anchor` |
| `toast` | **服务端要客户端显示的一句话** | 房间内的提示行 |
| `localContext` / `ClientContext` / `openUDID` | 客户端上下文与设备号 | `clientID` |

### 7.2 推送：WNS `roomMsg`

`OnRoomMsgWnsPush(showID, cmd, subCmd, data)` 先按 `cmd` 分派，`cmd` 只有三个值（`sub eax,0x12c` 后 `je/je/jne`）：

```text
300 -> 房间状态（整包 roomInfo / 进房、退房、换歌）
301 -> 房间事件：subCmd 0 = 成员变化，1 = 播放状态，2 = 另一台设备接管
302 -> 房间消息/聊天
其它 -> "not support msg, cmd=%d, subcmd=%d"
```

同一份代码里还有 `OnRoomMsgWnsPush same uin, return`，即推送先按 uin 过滤：不是自己的房间直接丢掉。subCmd 2 分支里用的那句话是「你已在另一设备加入一起听，本次互动终止」，弹出的按钮是「我知道了」——和 Lilt 的 `ErrReplaced` 是同一件事。

### 7.3 服务端错误：只报四类，其余沉默

`DealServerError(nErrCode)` 把错误码减去 11000 后跳表，只有 4 个分支：

```text
11000 / 11002 / 11003 -> 直接把服务端给的那句话弹出来（3 秒），不做别的
11001                 -> 弹完这句，再刷新房间
其它                   -> 什么也不做
```

也就是说：**QQ 音乐对大部分房间错误是沉默的**，只有服务端明确带了一句话才显示一次。这解释了两件事：

1. 房间里的提示是服务端给的文案（`toast` 字段），不是客户端自己编的；
2. 客户端从不因为「操作太快」去教育用户——限速在服务端，超了就直接丢，界面上没有「操作太频繁」这种弹窗。

Lilt 之前把每个 `server_error` 都翻成一句 toast，还把限速也翻了出来，这是两边的差异所在。

## 8. 由此改掉的两处（0.6.1）

1. **房间里不再有弹窗**。所有房间内的话都落在页面自己的一行（`togetherRoomNote`，位置就是 QQ 音乐 `LTDescription` 的位置）：点歌、切歌、拒绝、断线、房主转让都在这一行里出现，六秒后自己淡掉；房间结束、被移出、房主退房则留下一张结束卡片（对应 `endCard`/`endToast`），写明听了多久、几首歌、和谁。真正的错误用暖色，其余用灰色，不再遮住唱片。
2. **把限速挡在发生之前**。服务端每秒最多 20 条消息、`playback` 每秒最多 10 条、同一首歌的链接两秒内只能给一次。客户端现在自己按 150ms 发布锚点（≤6.7 次/秒）、按 2.1s 分享同一首歌的链接，拖动进度条只在停下的那一下发出去。`rate_limited` 于是不会发生；万一仍然收到，客户端一个字都不说，下一秒跟着心跳补上。

顺带学过来的还有两处：

- **心跳由房间决定**（`heartBeatInterval`）：服务端在 `welcome` 里给出 `heartbeatMs`（现在 12 秒，房间每 30 秒清扫一次），客户端按这个节奏 ping，不再自己写死 15 秒。
- **播放模式属于房间**（`playMode` / `songRandomIndex`）：房主的队列把 `shuffle` 一起镜像到房间，访客按同一个模式走队列，而不是各自随机。
