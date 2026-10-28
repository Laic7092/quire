# 窗口外壳、macOS 集成与界面语言

三件只在「打包 / 换平台 / 加语言」时才需要看的事，代码都在这个地方，所以放一起。

## 窗口外壳

所有平台是同一套 Books 式外壳：`TitleBarStyle: TitleBarHidden`，没有系统标题栏，
视图一直画到窗口顶边，自定义顶栏就是标题栏。阅读页的顶栏整条可拖动；书库把窗口按钮的
位置让给源列表头部和右上角工具栏。全屏时 `c.TitleBar()` 归零，布局自动收回。按钮在哪
一侧由平台决定（macOS 交通灯在左、Windows 标题按钮在右、Linux 跟桌面布局），视图按
`c.TitleBar().Left/.Right` 让位。

默认窗口 787×590（最小 640×420），打开书之后窗口变成那张纸。

## macOS

- **菜单栏**：文件（打开… `⌘O`、打开最近、关闭窗口）、编辑、显示（书库侧栏 `⌘B`、
  目录 `⌘T`、搜索 `⌘F`、按书名排序、显示设置 `⌘,`、全屏幕）、窗口、帮助。
  `⌘C` 故意没有菜单项：macOS 的菜单快捷键会先于视图吃掉按键，而页面选区和搜索框都要
  自己处理它，所以页面上的复制走「编辑 › 复制选中的文字」，该行只在有选区时可用。
- **打开文件**：Finder 双击、拖到 Dock 图标、系统/应用内的「打开最近」，以及第二次启动，
  都由 `App.OnOpenFile` 交给已经运行的窗口；读过的书写进 `App.AddRecentDocument`。
- **关窗不退出**：关掉窗口后应用留在 Dock；点图标或用菜单命令会重建窗口，
  打开的书、阅读位置和浮出的面板都还在。
- **字体**：字体查找已包含 `PingFang.ttc` / `Songti.ttc`；状态文件在
  `~/Library/Application Support/quire/state.json`。

## 打包

```sh
go tool mygo build -platform darwin/universal
```

产物是 `build/darwin-universal/quire.app`（arm64 + amd64）。MyGo 不需要 cgo，
Linux/Windows 上也能交叉编译出这个 bundle，但没有签名、不会生成 `.dmg`。要分发就在 Mac 上
配置 `macos.signingIdentity`（Developer ID）与 `macos.notarize`，或命令行加 `-sign`；
只想出 app 加 `-skip-dmg`。`go run .` 有同样的隐藏标题栏和菜单栏，
但 Finder 的文件关联只在打包后注册（`mygo.json` 的 `fileAssociations`）。

## 界面语言

文案走 MyGo 的 catalogs：`mygo.json` 的 `i18n` 指向 `locales/`，源语言是**英文**
（`en.json`，带每条文案的说明与占位符类型），`zh-Hans.json` 是简体中文。
`go tool mygo generate .`（`mygo dev` / `mygo build` 也会）生成 `locales/catalog_gen.go`：
每个 key 一个类型化函数。

```go
ui.Text(c, locales.PanelContents())       // "Contents" / "目录"
ui.Text(c, locales.LibraryCount(3))       // "3 books" / "3 本书"
```

- 语言跟随系统，没有切换入口：catalogs 的语言就是 app 的语言。MyGo 自己的控件文案
  （搜索框的 Clear、菜单角色）由生成文件里 import 的 `mygo/locales/<tag>` 跟着走。
- 运行时语言改变时（macOS 系统设置），`OnLocaleChanged` 重做窗口标题和菜单；视图每帧
  重建，面板自己就跟着换了。书籍内容与 TXT 合成章节名（「第 N 节」）不进 catalogs。
- 新增一门语言：往 `locales/` 放一份 `<tag>.json`（key 与 `en.json` 相同），跑
  `go tool mygo i18n check .` 再 `go tool mygo generate .`。缺翻译回落到源语言。
- `locales/` 同时是一个 Go 包，`*.json` 会被 embed，所以不要往里放别的东西。
