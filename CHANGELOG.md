# 更新日志

## 0.6.1

- 「一起听」不再有弹窗：房间要说的话都落在页面自己的一行（位置就是 QQ 音乐房间描述那一行），六秒后自己淡掉；点歌、切歌、房主控制、断线重连、复制邀请都在这一行里出现，不再有遮住唱片的提示框
- 限速挡在发生之前：客户端自己按 150ms 发布锚点（≤6.7 次/秒）、按 2.1s 重复分享同一首歌的链接，拖动进度条只在停下的那一下发；`rate_limited` 因此不会发生，万一收到也一个字都不说，下一拍跟心跳补上
- 房间里两个人在放歌时，歌曲上方显示「我们正在一起听」，对应 QQ 音乐房间窗口里的 weListen 状态；只有一个听众或暂停时不显示
- 新增房间结束卡片：房间结束、被移出或房主退房后，页面留下这次一起听听了多久、几首歌、和谁，可「再来一间」（对应 QQ 音乐的 `endCard`/`endToast`）
- 心跳间隔改由房间给出（`heartbeatMs`，服务端 12 秒一次、房间 30 秒清扫），客户端不再写死 15 秒（对应 `heartBeatInterval`）
- 播放模式属于房间：房主的随机/顺序随队列一起镜像，访客按同一模式走队列，不再各自随机（对应 `playMode`/`songRandomIndex`）
- `docs/together-reverse.md` 补上第二轮静态分析：房间 JSON 全字段、WNS 推送 `cmd 300/301/302`、服务端错误只报 11000–11003 其余沉默（房间里没有「操作太频繁」这类客户端提示的原因）
- 「一起听」真正同步：房主播放器的每次变化在 60–120ms 内发布（合并拖动进度的一串 seek），不再等 4 秒心跳；访客按服务器时钟跟随
- 房主把自己的队列镜像到房间，访客持有同一份队列并在正确索引落点，切歌不再退回「单曲重新加载」
- 会员音源真正共享：房间里任何成员的账号签得出直链就交给房间（`stream`），服务端按曲目缓存并在进房时补发；没有会员的一端点歌或播放时，有会员的那台客户端会当场代签，即使它自己没在播这首
- 修掉非会员听到 30 秒试听版导致的「各播各的」：`fee=1/4` 的曲子用 Range 探测 CDN 真实字节数，试听绝不进缓存、绝不分享，非会员端先等房间的完整链接，会员端分享后自动切换成同一份文件
- 房间里的音源优先于各自解析，两边播同一份字节；同一首歌两个会员各有一条链接时不再互相覆盖，界面显示「音源来自「XX」的会员」
- 新增房主转让：成员菜单「转让房主」，服务端记录房间持有者，旧房主的 hostKey 转让后失效，播放不中断
- 房间界面简化：顶部只留房间名、房间号、同步状态、成员、复制邀请与离开，去掉重复的邀请入口与状态文字；点歌台/聊天收紧间距与圆角
- 服务端只接受 QQ 音乐 CDN 的直链，防止房间成员让别人的客户端去取任意地址
- 新增独立配置档：`Lilt.exe --profile guest`（或 `LILT_PROFILE`），一台电脑可同时开两个账号分别登录，设置、登录态、缓存与单实例锁互不影响，方便两个人（或主号与副号）一起听
- 更像「一个人在房间里放歌」：每个账号优先用自己签的链接，只有签不出来的那端才用房间里的链接；代签只在收到请求时发生，失败后 10 分钟内不再问 QQ；试听探测只在房间里做，普通播放的请求与以往一致
- 安全加固：房间只显示成员句柄（不可用来冒充/顶号）、房主密钥走 header、拒绝浏览器 Origin 请求、按 IP 限速与 URL 长度限制、封面/头像只取 QQ 域名、公网房间服务器必须 wss
- `docs/together-reverse.md` 记录对 QQ 音乐 PC 版「一起听」的静态分析（LTRoomEntry/Quit/HeartBeat/SongOper + WNS 推送 + 服务端 offset）与本项目的对应实现

## 0.6.0

- 新增「一起听」：创建或加入房间，两个人同步播放同一首歌；两人都可播放、暂停、拖动进度，下一首/上一首由房主队列执行
- 房间界面按 QQ 音乐电脑版「一起听」重做：薄荷色画布、黑胶唱片与唱臂、右侧歌词与走带控制、顶部成员头像与状态
- 房间成员一律显示各自 QQ 音乐账号的昵称与头像，会话稍后恢复时会自动补上，不再所有人都显示「我」
- 会员改为「房间里任意一人有会员即可」：谁解析出会员曲目的播放直链，就把链接分享给房间里的另一方
- 点歌台重做：内置歌曲搜索，任何成员都能点歌、都能点「播放」；房主队列执行并立即切歌
- 断线自动重连、服务器时钟校准与进度漂移校正；任意一方的控制都会同步给另一方
- 创建房间增加重试与更明确的失败提示，可在「一起听」页填写自定义房间服务器
- 房主点离开或退出应用会结束房间，房主断线 2 分钟未回也会结束，异常断线的成员 60 秒内清理
- 播放会话保留上一份备份，窗口隐藏/最小化时也保存一次，主文件损坏时自动回退

## 0.5.1

- 桌面端专辑页面新增发行信息展示，较短发行信息直接内联显示在元数据栏，完整信息支持按需浮层查看
- 新增「不透明侧边栏」外观开关，并修复 Windows 亚克力弹出浮层显示异常
- Windows 任务栏歌词支持自动适配居左任务栏，并新增显示专辑封面开关
- 修复 QQ 登录过期后自动续期失败的问题，并增强本地加密会话存储的可靠性
- 重新整理设置页面分区结构，调整部分设置项布局

> 感谢 [@FQDianYan](https://github.com/FQDianYan)、[@CWalkene](https://github.com/CWalkene) 对本版本的贡献（PR #48、#49、#50）。

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.1/lilt-setup-0.5.1-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.1/lilt-0.5.1-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.1/lilt_0.5.1_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.1/lilt-0.5.1-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.1/lilt-setup-0.5.1-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.1/lilt-0.5.1-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.1/lilt_0.5.1_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.1/lilt-0.5.1-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.5.0

- 侧边栏「为你推荐」更名为「首页」，在「精选歌单」新增「歌单广场」入口，支持推荐、官方歌单、编辑精选与全部分类浏览
- 接入 Hi-Res FLAC 播放并支持恢复歌词浏览位置，修复拖动进度条后 FLAC 播放卡顿，并优化正在播放页面音质标签样式
- 双行桌面歌词与任务栏歌词改为两行交替逐字高亮，两行唱完再平滑切换下一组；设置中新增歌词字体选择（实验性）
- macOS 刘海歌词支持逐字进度填色，修复 QRC 歌词含双引号时被截断的问题
- 专辑封面、歌词、歌单与已购音乐支持本地磁盘缓存；待播清单更名为「播放列表」并支持移除歌曲
- 修复登录恢复与凭据续期异常，优化播放启动、首页加载及封面解码性能，升级 MyGo 至 v0.9.2

> 感谢 [@FQDianYan](https://github.com/FQDianYan)、[@CWalkene](https://github.com/CWalkene)、[@YoungTreeTree](https://github.com/YoungTreeTree) 对本版本的贡献（PR #36、#37、#38、#41、#43、#44）。

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.0/lilt-setup-0.5.0-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.0/lilt-0.5.0-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.0/lilt_0.5.0_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.0/lilt-0.5.0-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.0/lilt-setup-0.5.0-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.0/lilt-0.5.0-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.0/lilt_0.5.0_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.5.0/lilt-0.5.0-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.4.4

- 正在播放页面支持自由浏览歌词，点击「回到当前」恢复跟随
- 优化桌面及任务栏歌词渲染，降低资源占用

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.4/lilt-setup-0.4.4-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.4/lilt-0.4.4-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.4/lilt_0.4.4_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.4/lilt-0.4.4-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.4/lilt-setup-0.4.4-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.4/lilt-0.4.4-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.4/lilt_0.4.4_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.4/lilt-0.4.4-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.4.3

- 支持搜索自己歌单中的歌曲
- 修复 Linux Wayland 桌面歌词的定位、层级及锁定后鼠标穿透问题
- 关闭应用时遵循播放淡入淡出设置
- 优化 Windows 任务栏歌词

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.3/lilt-setup-0.4.3-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.3/lilt-0.4.3-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.3/lilt_0.4.3_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.3/lilt-0.4.3-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.3/lilt-setup-0.4.3-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.3/lilt-0.4.3-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.3/lilt_0.4.3_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.3/lilt-0.4.3-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.4.2

- 正在播放页面支持调整歌词字号
- 个人资料显示 QQ 音乐会员到期时间
- 改进 QQ 与微信登录凭证续期
- 修复桌面端跨采样率手动切歌时旧歌曲声音残留，并平滑关闭旧音频输出
- 打磨多处细节

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.2/lilt-setup-0.4.2-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.2/lilt-0.4.2-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.2/lilt_0.4.2_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.2/lilt-0.4.2-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.2/lilt-setup-0.4.2-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.2/lilt-0.4.2-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.2/lilt_0.4.2_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.2/lilt-0.4.2-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.4.1

- 降低音频播放的内存占用，未写入歌曲缓存的音频优先使用临时文件，播放结束后自动清理
- 优化封面加载与复用，按需生成主题色和模糊背景，减少重复下载、解码及内存占用
- 降低正在播放页面及展开、收起动画的渲染内存占用，复用裁剪缓冲并及时释放不再使用的 GPU 资源
- 升级 MyGo 至 v0.8.3，合入上游内存管理与渲染优化

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.1/lilt-setup-0.4.1-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.1/lilt-0.4.1-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.1/lilt_0.4.1_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.1/lilt-0.4.1-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.1/lilt-setup-0.4.1-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.1/lilt-0.4.1-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.1/lilt_0.4.1_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.1/lilt-0.4.1-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.4.0

- Linux 新增实验性桌面歌词(仅部分桌面环境测试)
- 桌面歌词支持自定义颜色和透明度
- 待播清单改为浮动面板
- 新增播放操作淡入淡出开关
- 优化播放和歌词显示的资源占用
- 打磨优化部分细节及体验
- 优化 Windows 任务栏歌词
- 调整应用图标样式和 macOS 图标尺寸，修复 macOS 刘海播放器展开时出现白边的问题
- 桌面端音频输出支持跟随音源采样率，设备冷启动、切换采样率或唤醒时进行短暂静音预热
- 正在播放页面新增从播放器封面展开的过渡动画，降低过渡时的内存占用
- 修复正在播放页面的播放时间可能停止更新的问题
- 修复部分音频后端在歌曲自然结束后可能无法继续播放的问题

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.0/lilt-setup-0.4.0-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.0/lilt-0.4.0-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.0/lilt_0.4.0_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.0/lilt-0.4.0-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.0/lilt-setup-0.4.0-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.0/lilt-0.4.0-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.0/lilt_0.4.0_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.4.0/lilt-0.4.0-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.3.0

- Windows 和 macOS 新增实验性桌面歌词，独立显示逐字高亮的当前歌词，支持拖动、调整宽度和字号、锁定位置及悬停播放控制
- Windows 新增实验性任务栏歌词，在空间允许的横向主任务栏中显示当前歌词与封面；点击封面可打开播放控制浮层，进行上一首、播放/暂停、下一首和进度跳转；灵感来源[TaskbarLyrics](https://github.com/ANYNC/TaskbarLyrics)
- 设置中可统一选择桌面或任务栏歌词，并可从播放栏或托盘快速开关；关闭歌词后会保留当前模式和桌面歌词偏好
- 歌曲菜单新增「下一首播放」，可将歌曲直接插入当前播放项之后

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.3.0/lilt-setup-0.3.0-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.3.0/lilt-0.3.0-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.3.0/lilt_0.3.0_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.3.0/lilt-0.3.0-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.3.0/lilt-setup-0.3.0-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.3.0/lilt-0.3.0-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.3.0/lilt_0.3.0_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.3.0/lilt-0.3.0-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.2.2

- 逐字歌词完全适配 QQ 音乐官方 QRC 逐字时间轴，暂停或回跳后高亮仍紧跟播放位置
- 音量平衡从单一开关扩展为关闭、安静、标准、高音量四档，旧设置的开启对应「标准」挡位
- 修复主窗口关闭到托盘后，再次点击应用快捷方式会新开一个应用的问题
- 音量控件上鼠标滚轮改为每次滚动 5%，触控板滑动保持连续变化

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.2/lilt-setup-0.2.2-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.2/lilt-0.2.2-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.2/lilt_0.2.2_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.2/lilt-0.2.2-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.2/lilt-setup-0.2.2-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.2/lilt-0.2.2-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.2/lilt_0.2.2_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.2/lilt-0.2.2-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.2.1

- 以补丁形式修复MyGo Windows 层在多个缩放比例不同的显示器之间恢复窗口时，窗口位置和尺寸可能逐次漂移的问题
- 优化歌单与专辑完整简介浮层的滚动条间距，避免内容与滚动条过于贴近
- 升级上游 MyGo 0.7.4，改善窗口首次显示时的白屏闪烁、原生菜单交互及 macOS 闲置功耗

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.1/lilt-setup-0.2.1-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.1/lilt-0.2.1-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.1/lilt_0.2.1_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.1/lilt-0.2.1-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.1/lilt-setup-0.2.1-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.1/lilt-0.2.1-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.1/lilt_0.2.1_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.1/lilt-0.2.1-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.2.0

- 首页新增更多样式歌曲推荐，对齐官方，并优化首页布局
- Windows 和 Linux 的设置页改为在主窗口中打开，macOS依然对齐系统设计
- 优化底部播放栏的悬停、按压和键盘聚焦反馈，音量面板展开时保持按钮高亮
- 合并上游，优化封面缓存，减少返回已浏览页面时的重复加载，复用播放页仍持有的封面

### 下载指南

| 处理器架构 | Windows | macOS | Linux |
| :--- | :--- | :--- | :--- |
| amd64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.0/lilt-setup-0.2.0-amd64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.0/lilt-0.2.0-amd64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.0/lilt_0.2.0_amd64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.0/lilt-0.2.0-linux-amd64.tar.gz) |
| arm64 | [安装包 .exe](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.0/lilt-setup-0.2.0-arm64.exe) | [安装包 .dmg](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.0/lilt-0.2.0-arm64.dmg) | [.deb](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.0/lilt_0.2.0_arm64.deb) · [.tar.gz](https://github.com/Amamiya23/mygo-qyin/releases/download/v0.2.0/lilt-0.2.0-linux-arm64.tar.gz) |

> Linux：Ubuntu / Debian 等支持 deb 的发行版选 `.deb`，其他发行版选 `.tar.gz`。

## 0.1.8

- 打磨多处细节
- 新增音量平衡，设置中默认开启，减小不同歌曲之间的音量差异
- 进度条新增「波浪」特效，可在设置中切换标准、烟雾和波浪三种样式
- 支持更改歌曲缓存目录，重启后自动迁移已有缓存
- 改善歌曲、歌手和专辑的搜索稳定性，接口受限时自动切换备用来源，并优化分页和失败重试
- 优化搜索联想，快速输入或切换关键词时及时取消旧请求，避免显示过期建议；搜索历史弹窗与搜索框对齐
- 当前歌曲滚出列表后，可通过定位按钮快速返回；在歌单中再次点击当前歌曲可暂停或继续播放
- 歌单和专辑简介过长时，可通过悬停或键盘聚焦查看完整内容
- 优化播放页、律动和选歌视图中的按钮反馈，悬停时图标更清晰

## 0.1.7

- 修复「今日为你推荐」重复精选歌单的问题，改为独立读取账号首页推荐
- 修正排行榜内容类型筛选，排除无法按歌曲加载的专用榜单，避免打开后显示空列表
- 从歌单返回「为你推荐」或排行榜时恢复原来的滚动位置，前进和重新访问页面也会保留各自的位置
- macOS 刘海播放器新增同步歌词：虚拟刘海收起时显示当前歌词，展开播放时在歌名下显示歌词
- 修复刘海播放器在暂停后拖动播放进度时，歌词未及时刷新的问题

## 0.1.6

- 启用应用内自动更新，支持检查新版本、查看更新说明、下载安装和重启生效；旧版用户需手动安装一次本版本
- 新增微信扫码登录，可在登录窗口切换 QQ / 微信
- 我的主页显示会员状态，支持查询失败后重试
- 「我喜欢」支持单曲 / 专辑切换，专辑页可收藏与取消收藏
- 「我喜欢」单曲与专辑共用大封面页头，切换时保持布局稳定，分别记住滚动位置，并增加标签切换动效
- 侧栏新增「已购音乐」，登录后可分别浏览已购单曲与数字专辑，支持分页、刷新和专辑跳转
- 修复收藏歌单名称显示及取消收藏功能
- 修复登录后歌手页的专辑列表加载失败
- 修复部分歌曲的翻译歌词缺失，以及歌曲评论无法加载和翻页的问题
- 正在播放的标签保留在右侧内容区上方，在切换页面和评论数量变化时保持位置稳定，大窗口中的封面与内容居中显示
- 正在播放的封面、歌名和进度条左侧对齐，调整标签间距并缩短选歌封面墙顶部的模糊过渡
- 修复 Windows 右键菜单宽度计算不足，导致歌手名、专辑名等文字右侧被裁切的问题
- Windows 侧边栏默认使用亚克力，保留已保存的 Mica 选择；macOS 和 Linux 不显示 Windows 材质设置
- 关闭主窗口的设置按平台显示后台运行或托盘选项，Windows/Linux 无法创建托盘时关闭窗口直接退出

## 0.1.5

- 修复登录后搜索歌曲、歌手和专辑返回空结果的问题
- 搜索接口拒绝或返回异常格式时正确报告失败，避免误显示为没有结果
- 项目更名为 Lilt，统一应用名称、安装包、便携包和网站品牌
- 首次启动自动迁移旧 Q 音版的设置、播放队列和缓存，沿用原应用标识以保留登录态
- Windows 侧边栏支持在 Mica / 亚克力间切换，亚克力使用 40% 磨砂遮罩与低对比控件
- 已喜欢歌曲的爱心及点击动效统一使用 Radix 红色，保留绿色主题和未收藏时的空心样式

## QQ 音乐 0.1.4

- 修复歌单歌曲行、侧栏迷你播放器和正在播放详情等位置的封面缺失
- 封面请求使用 QQ CDN 支持的尺寸档位，避免任意尺寸地址返回 404

## QQ 音乐 0.1.3

- 修复 Windows 已配置系统代理时，客户端仍绕过代理直连而导致二维码获取中断的问题
- 登录、曲库、封面与音频下载共用系统代理选择，并遵循环境变量优先级和代理绕过规则
- 取码失败保留具体原因，支持失败后再次刷新并显示新二维码

## QQ 音乐 0.1.2

- 修复登录响应的有效期字段以数字字符串返回时被错误拒绝的问题
- 区分 QQ 音乐服务拒绝码、响应解析、缺失凭据及网络连接错误，便于定位实际失败阶段

## QQ 音乐 0.1.1

- 修复扫码确认失败后错误地址撑出对话框的问题，按登录步骤显示简短提示
- 修复已经扫描时出现错误却无法刷新二维码的问题
- 授权 GET 在 TLS 建连中断、请求尚未发出时重新连接一次
