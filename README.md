# Quire

用 MyGo 原生 UI（`package ui`，没有 webview / TypeScript 前端）写的 EPUB / TXT 阅读器，
分页与滚动两种模式，外观照 macOS Books 26 的阅读页摆。

断行、分页、行位置和字形位置全部来自本地 `github.com/laix/layx` 的 `render` 引擎；
本仓库只做四件事：把书变成 `render.Document`、把 `DisplayList` 画到 `ui.Painter`、
把窗口和面板摆出来、把状态存下来。两个 layx module 都是本地 `replace`
（`github.com/laix/layx` 排版、`github.com/laix/layx/epub` 解析 EPUB），改上层库不用发版。

## 名字与图标

**Quire**：装订时对折起来的一叠纸，一帖。大开本摊开的两页，正好就是一张纸对折的样子，
阅读器就叫这个名字。`resources/icon.py` 画的是同一件事：墨色圆角底上摊开的一帖，右页
折角是走开的那一页，正在读的那一行用强调色。

脚本是唯一的一份几何——Pillow 出 `icon.png`，同一批多边形写成 `icon.svg`（两者逐像素
只差 ~1/255），外加 `icons/` 里 16…512 与 `icon.ico`、`icon-sheet.png` 供人看尺寸。
改配色改顶层 `VARIANTS`（`icon` 是墨底，`icon-light` 是纸底），然后

```sh
python3 resources/icon.py
```

## 模块

- `internal/book`：唯一的入口层。EPUB 全交给 `layx/epub`（container → OPF → spine、
  nav/NCX 目录、XHTML 标签子集、图片映射），`Book`/`Chapter` 是该模块的类型别名；
  这里只剩 TXT（编码探测、章节标题、段落切分）——纯文本也转成同样的 `Chapter`，
  所以排版、目录、搜索都不知道书不是 EPUB。
- `internal/reader`：字体（内置 XCharter 正文衬线 + Inter 窗口文字，CJK 回落系统字体，
  第一次打开书时才读）、`render.Engine.Layout`、以及 `paint.Painter` 的实现。
  字形按 (面, gid, 字号, DPR, 亚像素相位) **只栅格化一次**进图集
  （`glyph_atlas.go`），之后每帧只是查表 + 贴图。同层还有纸与墨（`Palette`）、
  阅读设置（`Options`）、选区（`Selection`）、离屏出图（`PageRenderer`）。
- `internal/library`：书架。只读 OPF metadata，不打开整本书。
- `internal/toc` / `internal/search`：目录行来自书的 nav/NCX（层级、章内 `#fragment`
  都是真的），页码只来自 `layout.PageRange`；搜索索引走 `render.NodeText`，命中映射回
  `render.Location`。
- `internal/store`：`os.UserConfigDir()/quire/state.json`，原子写 + 2s 合并，
  存书单、每本书的阅读位置与字号/面板偏好。书架就是这份书单。
- `internal/appstate`：把上面几个接起来的一层（面板切换、异步刷新、索引与文档同步）。
- `internal/icons`、`internal/widgets`：工具栏与三个面板共用的图标和小件。
- `main.go` / `windows.go` / `view.go` / `sheet.go` / `scroll.go` / `theme.go` /
  `settings.go` / `stats.go` / `perf.go`：窗口与多窗口的 desk、阅读页、翻页动画、滚动、
  主题、设置、阅读统计、性能日志。

## 跑

```sh
go run . [-font /path/to/font.ttc] [-serif /path/to/serif.ttc] \
         [-shelf ~/books] [-state /tmp/state.json] [book.epub|book.txt]
```

不带书启动是首页（源列表 + 继续阅读 / 所有图书 + 本周阅读圆环 + 阅读目标卡）。
打开书之后：顶栏是目录/书签/笔记、书名、分享/Aa/搜索、书签；正文默认两页大开本
（窗口窄于约 702 DIP 退回单页）；目录、搜索、书签、笔记和书里的链接、脚注这些跳转过后，
页脚左边才出现返回/前进箭头，平时不占地方；中间是「N of M」，右边是本章剩余页数，
最底下是页码滑块；
三个按钮各开一张悬浮卡片盖在书页上，不占版面、不重排正文。滚动模式（Aa → Customize →
Scrolling）排一份整本连续的流，按一屏高的 tile 懒构建。

多窗口：一扇窗读一本。⌘N（macOS 是「文件 › 新建窗口」）开一个新的书库窗口；已经开着
一本书时再打开一本书，那本书另开一扇窗，于是几本可以并排读。每一扇窗有自己的书、页码、
滚动位置和浮出的面板；书架、每本书的阅读位置、书签笔记、纸张、阅读目标和字体表由所有
窗口共用。菜单栏只有一条，命令作用于当前焦点窗口；纸色一换，所有窗口一起换——排版用的
调色板是进程级的，分窗口换纸会让两本书互相用错颜色。

快捷键：←/→ 翻页（大开本一次两页）、Home/End 首末页、+/- 改字号、⌘N 新建窗口、
⌘B/⌘F/⌘T
书库/搜索/目录、⌘D 书签、⌘[ / ⌘] 返回/前进上次跳转、Esc 依次收起面板/封面/选区/回到书库。纸上拖动选字、⌘C 复制，
右键是页面菜单（复制、加笔记、搜选中的词、选中本页、清选区）。翻页默认横向滑带，
Aa 里可换淡入淡出；滚动模式的每一帧只重绘、不重建视图。

## 环境变量与开关

| 变量 | 作用 |
|---|---|
| `LAYX_FONT` / `-font` | CJK 回落字体。默认依次找 PingFang、Hiragino Sans GB、msyh、wqy-microhei 等 |
| `LAYX_SERIF` / `-serif` | 系统衬线，只作为内置 XCharter 缺字时的后备 |
| `LAYX_NO_ANIM=1` | 关掉翻页与滚动动画（CI 与 `SNAP=1` 快照都这样跑，帧才稳定） |
| `LAYX_GLYPH_ATLAS=0` | 走每帧重建轮廓的旧路径。留作 A/B 与回滚，默认全平台用图集 |
| `LAYX_GLYPH_STATS=1` | 图集计数器：`UIPainter.GlyphStats()` 给命中率、栅格化次数、内存 |
| `LAYX_TEXT_GRID=0` | 关掉「每行基线落到整设备像素」（150% 屏上行距非整数时会漂相位） |
| `LAYX_TEXT_TONE` / `_GROW` / `_STEM=0` | 墨色修正的三个开关，屏幕与 PNG 同时生效 |
| `LAYX_PERF=1` | 实机性能日志（格式与字段见 [`docs/scroll-performance.md`](docs/scroll-performance.md)） |
| `LAYX_PERF_FILE` / `_SLOW` / `_HZ` / `_EVERY` | 日志位置、慢帧阈值（默认 8 ms）、刷新率、每块时长 |
| `LAYX_CPU_PROFILE` | 顺带留一份真机 CPU profile，`go tool pprof` 打开 |
| `MYGO_FRAME_STATS=1` 或 `<ms>` | MyGo 自己的 build / layout / paint / present 分帧 |
| `MYGO_GPU=0` | 不用 GPU，走 MyGo 自带的软件光栅 |

## 测试

```sh
go build ./... && go vet ./...
CGO_ENABLED=0 go test ./...
go tool mygo vet . && go tool mygo i18n check .   # 文案：key、占位符、复数形式
```

`internal/book/testdata/mini.epub` 是最小测试书；`testdata/samples/` 是公有领域的真实
样书（阿Q正傳 EPUB+TXT、浮生六記 TXT），`TestSamplesOpen` 逐个打开，截断的文件不会被
误提交。根包的测试用 `ui.NewTester` 无头构建真窗口：`view_test.go` 把每种屏幕状态
（正文、三个面板、封面、首页在参考/宽/窄三种宽度、Aa 面板、大开本、Quiet）都渲染一遍，
`SNAP=1 go test . -run TestWindowRendersEveryScreen` 写出 `/tmp/snap-*.png` 给人看布局。

一个小工具各有自己的命令：`go run ./cmd/corpus -dir ~/epub-corpus` 把整个语料走一遍
`Open → Build → Layout`（真书回归，见 [`docs/css-census.md`](docs/css-census.md)）；
`go run ./cmd/bookshot -book x.epub -page 2 -out /tmp/page.png` 不出窗口直接出图
（`internal/reader.PageRenderer` 与屏幕共用同一个软件光栅器和同一张墨色表，所以 PNG 与
窗口同重）；`go run ./cmd/glyphprobe` 开真窗口滑一本书，报 cpu/frame 与图集命中率。

## 已知限制（本阶段明确不做）

- 不显示 EPUB 内置字体；图片只支持栅格格式（PNG/JPEG/GIF/WebP/BMP），SVG 只提取其中
  嵌入的栅格图，纯矢量封面留空白占位。
- 没有 CSS 子集：只按标签映射标题、段落、引用、列表、斜体/粗体、换行；不解析
  `<style>`、class 或 `em/%` 单位（该做哪 11 项、为什么不做的更多，见 css-census）。
- 图片没有独立版面策略：比内容区高的插图按栏宽缩放后被页底裁切，中间纯图片页不居中。
- 没有脚注、竖排、ruby、分享；搜索只折叠 ASCII 大小写，不折叠简繁/全半角。
- 书架是「打开过的书」的书单，`-shelf` 与「导入文件夹」只把目录这一层加进书单，
  不递归、不监听；文件被移走时那一行还在、只是打不开。
- 位置记忆依赖 node 号跨 `Build` 稳定；换书源/更新 EPUB 后旧位置会静默偏移。
  分页模式靠 `PageFor` 反向映射（单页文档不记位置），滚动模式走 `store.Locate`，不依赖页。
- 多窗口：书、页码、滚动位置和浮出面板各窗独立；书架、书签笔记、阅读位置、纸色、
  阅读目标与字体表共用。字号/边距/滚动模式仍各窗独立，状态文件存最后一次改动；两扇窗
  读同一本书时位置以最后翻页的那扇为准。macOS 关掉最后一扇窗后重开的是书库窗口，
  原来那本书要再从书架点开（位置还在）。
- 换纸不重排（`Layout.WithColors`，5MB 的书 2µs）；行距只重分页，页边距与对齐要重断行，
  换字号要重建 `Document` 并重排，换字面要重排。开关悬浮面板不影响排版。
- 两页大开本是宿主摆的：排一份半宽 `Layout` 在一张纸上画两页，安静时没有书脊线。
- 翻页是横移不是 Books 的卷页（卷页要整页位图，layx 只给显示列表）。
- 滚动模式没有 Books 的惯性与卷页：滚轮按平台给的位移立刻滚，键盘一屏一屏跳，
  位置按 `Location` 记而不是像素。没有 wasm 版本。
- 字体集只有一个字面，粗体/斜体在该字体没有对应 face 时只表现为同一字形。

## 文档

- [`docs/integration-notes.md`](docs/integration-notes.md)：宿主依赖 layx / MyGo 的哪些
  不变量、还缺哪些 API、宿主为此付出了什么。会被人回来查的那份。
- [`docs/scroll-performance.md`](docs/scroll-performance.md)：滚动为什么卡、现在的绘制
  链路（图集 / 墨色 / 只重绘的帧）、核心库的边界判定、待办，以及性能日志字段。
- [`docs/css-census.md`](docs/css-census.md)：EPUB 语料的 CSS 账本，决定 CSS 子集做哪
  11 项、明确不做哪些。
- [`docs/web-wasm.md`](docs/web-wasm.md)：上 Web 的 wasm 体积实测与三档结合方式。
- [`docs/packaging.md`](docs/packaging.md)：原生窗口外壳、macOS 集成与打包、界面语言。
