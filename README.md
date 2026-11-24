# Lilt

Lilt 是基于 MyGo 云音乐改造的第三方 QQ 音乐客户端，使用 Go 原生界面与播放器，以纯 Go 直接连接 QQ 音乐服务。
![截图1](assets/image.png)

## 功能

- QQ / 微信扫码登录
- 每日推荐、个性化歌曲货架、推荐歌单、排行榜、我的歌单
- 已购单曲与数字专辑
- 搜索单曲、歌手、专辑，支持搜索联想
- 在线播放受支持的音乐，支持 Hi-Res FLAC 音源与自动音质降级
- LRC 歌词与同步翻译，正在播放页的「律动」视图与歌曲评论
- macOS 刘海播放器：在屏幕顶部刘海处显示迷你播放器，悬停展开
- 音量平衡模式，减小不同歌曲之间的音量差异
- 桌面歌词：拖拽定位、锁定后鼠标穿透；Linux Wayland 支持 layer-shell 定位
- 一起听：创建或加入房间，同步播放、进度、点歌台与聊天；两人都能控制播放/暂停/进度，点歌台谁都能点、谁都能播；房间里任意一人是会员，会员歌曲就能一起听

> 未登录可浏览和播放 QQ 允许匿名收听的歌曲。左下角「登录 QQ 音乐」可选择使用手机 QQ 或微信扫码。会员与版权权限由 QQ 音乐服务决定。

## 构建

需要 Go 1.27.3+，无需 C 工具链。

依赖已固定在 `vendor/`；更新依赖后运行 `go mod vendor`。

> `vendor/github.com/egoist/mygo` 带有本项目依赖的本地补丁，直接 `go mod vendor` 会被 `go.mod` 里的 v0.9.2 覆盖并导致构建失败（例如缺少 `audio.Player.SetPrimary`）。新增依赖时用 `go mod vendor -o <临时目录>`，只把新模块复制进 `vendor/` 并更新 `modules.txt`。

```bash
go tool mygo dev     # 开发模式运行
go tool mygo build -platform windows/amd64  # 打包到 build/
go test ./internal/...                      # 运行测试
go tool mygo vet .
```

## 致谢

- [MyGo](https://github.com/egoist/mygo)：原生 UI 与应用框架
- [mygo-cloudmusic](https://github.com/egoist/mygo-cloudmusic)：网易云原生界面与播放器
- [qq-music-api](https://github.com/Rain120/qq-music-api)：提供登陆、个人内容获取、歌曲播放等
- [Linux DO](https://linux.do/) 社区

**本项目与 QQ 音乐官方无关，音乐版权归各权利人所有。**

## 许可证

[AGPL-3.0](LICENSE)
