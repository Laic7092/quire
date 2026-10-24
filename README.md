# layx-reader

用 MyGo 原生 UI（`package ui`，没有任何 webview / TypeScript 前端）实现的 EPUB / TXT
阅读器，分页与滚动两种模式。断行、分页、行位置和字形位置全部由本地 `github.com/laix/layx`
的 `render` 引擎产出；本仓库做四件事：

1. `internal/book`：只做入口和 TXT。EPUB（container.xml → OPF → spine、nav/NCX
   目录、XHTML → `render.Document` 的标签子集、图片映射成 `render.NodeImage`）
   全部来自上层模块 `github.com/laix/layx/epub`，本仓库不再保存副本；
   `Book` / `Chapter` 就是该模块的类型别名。这里剩下的是 TXT 路径（编码探测、
   章节标题、段落切分），它把纯文本转成同样的 `Chapter`，所以后面的排版、
   目录、搜索都不知道书不是 EPUB。
2. `internal/reader`：加载字体（内置 XCharter 做正文衬线、Inter 做窗口文字，CJK
   回落到 `LAYX_FONT` / `-font` / 系统字体），调用 `render.Engine.Layout`（分页用
   `ModePaged`，滚动用 `ModeScroll` + tile），并实现 `paint.Painter`，把
   layx 的 `DisplayList` 通过 `ui.Painter` / `ui.Path` / `FillPath` 画出来（字形来自
   go-text 轮廓，按 `paint.GlyphRun.Font` 分面缓存，所以一页里衬线正文和黑体标题能同时
   出现；位图来自 `ui.DecodeBitmap` + `ui.ImageRegion`）。同一层还管纸与墨
   （`Palette`，几套纸都在 `internal/reader/theme.go`）、阅读设置（`Options`：字号、
   行距、字面、对齐、页边距、双页、滚动）和选区：`Selection` 把拖出来的两个
   `render.Location` 变成矩形（`Layout.Selection`；分页模式带页号，滚动模式带内容
   坐标）和选中的文字（走 `render.Chapter.Nodes`，因为引擎不导出文本区间）。
3. `main.go` + `view.go` + `sheet.go` + `scroll.go` + `theme.go` + `settings.go` +
   `stats.go`：原生
   窗口，按 macOS Books 26 的阅读页截图（787×590 DIP、Quiet 纸、双页、Aa 展开）逐项
   摆的。阅读页：顶栏左侧一个胶囊里是目录/书签/笔记三个 16pt 图标，中间是书名（12pt、
   semibold、居中），右侧一个胶囊里是分享、**Aa** 与搜索，再右边是独立的圆形书签钮；正文
   直接印在纸上（**没有白纸、没有阴影、没有边框**），**默认就是大开本两页**：列宽 318、
   外边距 49、中缝 54、行距约 20.1 DIP，版心 21 + 27 DIP；页脚是一行 10pt 小字——左边
   章节徽章与章号，中间「161 of 418」，右边「2 pages left in chapter」，最底下是窗口宽度
   上的页码滑块，位置按 `page/total` 算，不是画死的。书页在页脚与顶栏之间没有书脊线，
   两页是同一张纸。不带书时是首页，按 Apple Books 的 Home 摆：左边是 macOS 源列表
   （搜索 / 首页 / 导入文件夹 / 打开…；书库下的全部、在读、已读完；我的收藏；底部是
   账户），右边是白底内容——衬线大标题、分区标题加箭头与说明、
   可横向滑动的封面行（继续阅读、所有图书，读到一半的封面底边有一条细进度），右上角是
   本周阅读圆环，最下面是阅读目标卡：年度进度环、阅读天数 / 连续阅读 / 已读完，以及
   目标设置和复制记录的按钮。所有尺寸都从窗口宽度按比例推、再夹上下限（侧栏 160–224、
   封面 76–140、页边距 20–40 DIP），所以 818 宽的参考窗口和更宽的窗口都对，Retina 与
   1× 显示也都由 DIP 处理。
   书库固定用系统外观（白 / 石墨 + 系统蓝，跟着深色/浅色走），不跟着阅读主题变；一打开
   书，窗口就变成那张纸。
   ←/→ 翻页（大开本一次翻两页），Home/End 首末页，+/- 改字号，⌘B/⌘F/⌘T
   书库侧栏/搜索/目录；鼠标点书页左半回翻、右半前进（按下后拖动仍是选字）。Esc 依次收起
   面板/封面/选区/回到书库。
   **滚动模式**（Aa → Customize → Scrolling，或「显示 → 滚动阅读」）用
   `render.ModeScroll` 排一份整本连续的流，按一屏高的 tile 懒构建并绘制：滚轮/触控板
   滚动、↑↓ 滚三行、←/→、PageUp/PageDown、空格滚一屏（留 10% 重叠）、Home/End 到首尾；
   点击不翻页也不滚动，只清选区。页脚中间换成百分比，右边是本章还剩几屏；读到的
   位置按 `render.Location` 记，和分页模式共用一套存储与恢复。
   翻页是一条横向滑带：旧页整幅
   移出、新页跟着进来，边界上有一道落在纸上的影子。在纸上按住拖动可以选字，⌘C 复制
   选中的文字；右键（macOS 是 Ctrl+点击，或菜单键/Shift+F10）是页面的菜单：复制、
   添加笔记、搜索选中的词、选中本页、清除选区。搜索命中在页面上是琥珀色，面板指着的那个更深一档。
   **Aa 面板**照 Books 的样子：219 宽、18 圆角，标题「Themes & Settings」下发丝分隔线，
   一行 38 高的分段（小 A / 大 A 一组，中间一条浅分隔线；右边是独立的 63 宽外观钮），
   3×2 的 62×55 主题卡片（Original / Quiet / Paper / Bold / Calm / Focus，Aa 与名字都印在
   卡里，选中的那张圈一道墨线），底下是 36 高的「⚙ Customize」胶囊。点开 Customize 才是
   字号、行距、页边距三个滑块，Serif/Sans、Justified/Left 两组分段，Two Pages 与
   Scrolling 两个开关和 Reset to Defaults。面板与 Aa 之间有一个指向按钮的小三角。每一项都写进状态文件。
4. `docs/integration-notes.md`：作为宿主的联调记录，以及对 layx 形态的意见。

常用功能各有一个包，都能独立测试：

- `internal/library`：书架。只读 `container.xml` → OPF 的 metadata 拿 title/creator/封面，
  不打开整本书；书架上的条目由 `store` 记着的书单驱动，它负责把每本书描述成一行。
- `internal/toc`：目录。页码只来自 `layout.PageRange`（滚动模式没有页，行只带章节），
  层级按标题里的「第N卷/部/篇/册」猜。
- `internal/search`：全文搜索。索引走 `render.NodeText`，命中映射回 `render.Location`；
  面板按 `layout.PageOf` 显示页码，滚动模式没有页，就只按位置跳转。
- `internal/toc`：目录。行来自书的 nav/NCX（`epub.Book.Toc`）：嵌套层级、章内
  `#fragment` 目标都是真的，点一行由 `Layout.Locate` 落在页或滚动位置；目录标签
  太离谱（整段正文）或只有一个条目的桩目录才退回按章节建行，TXT 一直走这条。
- `internal/store`：持久化。`os.UserConfigDir()/layx-reader/state.json`（Linux 是
  `~/.config`，macOS 是 `~/Library/Application Support`），原子写 + 2s 合并写入，
  存书架、每本书的阅读位置和字号/面板偏好；`PageFor` 按 node → anchor → progress →
  章首降级恢复位置。书架就是这份书单：打开过的书自动记进去，重开应用不用再导入。
- `internal/appstate`：把上面四个接起来的一层（面板切换、异步刷新、索引与文档同步）。
- `internal/icons`、`internal/widgets`：窗口和面板共用的 SVG 图标与几件小件（图标按钮、
  空状态、行底色），保证工具栏和三个面板是同一套观感。

`docs/integration-notes.md` 记的是当前仍然成立的结论：宿主依赖 layx / MyGo 的哪些
不变量、还缺哪些 API、宿主为此写了什么。历史轮次不再保留。

两个 layx module 都是本地 `replace`（`github.com/laix/layx` 负责排版，
`github.com/laix/layx/epub` 负责 EPUB → `render.Document`），所以改上层库不用发版。

## 运行

```sh
# [-shelf 目录] 把目录里的 EPUB/TXT 导入书架；-state 换状态文件。
go run . [-font /path/to/font.ttc] [-serif /path/to/serif.ttc] [-shelf ~/books] [-state /tmp/state.json] [book.epub|book.txt]
# 或
LAYX_FONT=/usr/share/fonts/truetype/wqy/wqy-microhei.ttc go run . book.epub
```

不带书启动是首页：源列表里是**打开…**、**选择目录**、全部/在读/已读完，中间是继续
阅读和所有图书两行封面（横向滚动），下面是阅读目标。打开书之后：顶栏左侧是目录/书签/笔记，
中间是书名，右侧是分享、Aa 与搜索，最右是书签；正文默认两页大开本（窗口窄于约 702
DIP 时退回单页），点书页左/右半翻页（拖动选字）；底栏是章节、「N of M」页码与本章剩余页数，最底下
是页码滑块。三个按钮各开一张**悬浮卡片**（左 12、顶 8、宽 300，圆角 + 阴影）盖在书页上，
不占版面、不重排正文；再点同一按钮或 Esc 收起。书页左边缘悬停时浮出「‹」，点击回到书库。

圆形书签钮和 ⌘D 给当前页加/去书签，已标记时按钮填成强调色；书签面板按阅读顺序列出，
点击跳过去，行内垃圾桶删除。选中正文后右键「添加笔记」（macOS 菜单「编辑 → 添加笔记」
也可）把这段存成笔记：段落以琥珀色留在页面上，笔记面板列出原文、章节与页数、批注；
选中一条可在面板底部写/改批注、保存或删除。两者都按 `Book.Key` 存进状态文件。

`LAYX_NO_ANIM=1` 关掉翻页动画（CI 和 `SNAP=1` 的快照都这样跑，帧才稳定）。

首页的图书行按「上次读的时间」排序（显示菜单里可以切成按书名），位置取 `store.Position`；
封面走 `internal/library` 只读 OPF 元数据，不打开整本书。阅读目标的账本在
`stats.go`：打开过的日子与记录过的页合并成阅读天数，今年读完的书才进年度目标，
目标值存在 `store.Prefs`。

## 原生窗口外壳与 macOS 集成

窗口外观在所有平台是同一套 Books 式外壳：`TitleBarStyle: TitleBarHidden`，没有系统
标题栏，视图一直画到窗口顶边，自定义顶栏就是标题栏。阅读页的顶栏整条可拖动，书库
把窗口按钮的位置让给源列表的头部和右上角的工具栏；全屏时 `c.TitleBar()` 归零，
布局自动收回。按钮在哪一侧由平台决定，视图按 `c.TitleBar()` 的 `Left`/`Right`
让位：macOS 的交通灯在左，Windows 的标题按钮在右，Linux 跟随桌面的按钮布局。

macOS 上额外做了这些：

- **菜单栏**：文件（打开… `⌘O`、打开最近、关闭窗口）、编辑、显示（书库侧栏 `⌘B`、
  目录 `⌘T`、搜索 `⌘F`、按书名排序、显示设置 `⌘,`、全屏幕）、窗口、帮助。
  `⌘C` 故意没有菜单项：macOS 的菜单快捷键会先于视图吃掉按键，而页面选区和搜索框
  都要自己处理它；页面上的选中文字走「编辑 › 复制选中的文字」，该行只在有选区时可用。
- **打开文件**：Finder 双击 EPUB/TXT、拖到 Dock 图标、系统/应用内的「打开最近」，
  以及第二次启动，都由 `App.OnOpenFile` 交给已经运行的窗口；读过的书写进
  `App.AddRecentDocument`，Dock 菜单和「最近使用」跟着更新。
- **关窗不退出**：关掉窗口后应用留在 Dock；点图标、Dock 菜单或菜单栏命令会重建窗口，
  打开的书、阅读位置和浮出的面板都还在。
- **字体与状态**：字体查找已经包含 `PingFang.ttc` / `Songti.ttc`；状态文件走
  `os.UserConfigDir()`，在 macOS 是
  `~/Library/Application Support/layx-reader/state.json`。
- **打包**：

  ```sh
  go tool mygo build -platform darwin/universal
  ```

  产物是 `build/darwin-universal/layx-reader.app`（arm64 + amd64）。MyGo 不需要 cgo，
  所以 Linux/Windows 上也能交叉编译出这个 bundle，但没有签名，也不会生成 `.dmg`。
  要分发，在 Mac 上配置 `macos.signingIdentity`（Developer ID）和 `macos.notarize`，
  或在命令行加 `-sign`；只想出 app 可以加 `-skip-dmg`。`go run .` 也能得到同样的
  隐藏标题栏（macOS 上还有菜单栏），但 Finder 的文件关联只在打包后注册。

纸与墨来自 `reader.Palette`（几套纸见 `internal/reader/theme.go`）。纸、墨和链接色只决定
“画的时候用什么颜色”，所以换纸是 `Layout.WithColors`：同一份断行和分页换个颜色，只重画
屏幕上的那几页，代价和书多大无关（5MB 的书 2µs）。它在应用主题的那一帧里同步做掉，所以
内容和外壳同一帧变色；其余颜色（桌面、顶栏、面板、强调色）由同一份 palette 现算。书库不跟纸走：没有打开书时用系统外观（白 / 石墨 + 系统蓝）。
正文的衬线是随仓库分发的 XCharter（`internal/reader/fonts/`），窗口文字是 Inter；两者都
只覆盖西文，CJK 由系统 CJK 字体在同一 family 里 fallback，所以一半西文衬线、一半中文
黑体也能排出来。

选区和高亮都在宿主侧，不依赖新 API：`Page.HitTest` / `Layout.HitTest` 把纸上的一点变成
`render.Location`，`Layout.Selection(from, to)` 给出矩形（跨页/跨章由引擎排序；滚动模式
给内容坐标），宿主在 `Draw` 回调里
把矩形填成半透明的「罩色」——搜索命中和拖选共用同一条路径。选中的文字由
`Selection.Text` 沿 `render.Chapter.Nodes` 拼接（`search` 的索引走同一套遍历）。
换了 `Layout`（字号、窗口尺寸、换书）就清掉选区：`Location` 里的 node id 只属于一份布局。
拖选目前限制在当前页内（拖出纸边会被夹到最靠近的一行/图）。

翻页动画只是重放已经排好的显示列表：`drawSheet` 把旧页和新页各按一个偏移画一次，
偏移由 `turnProgress` 从时钟算出，滑动期间每帧只要一次 `AnimationFrame`，不排版、
不重新取字形。`a.now` 是这台钟，测试把它钉死就能拿到确定的中间帧。

默认窗口 787×590（最小 640×420）；`-font` 决定 CJK 回落的字体，依次尝试
`/System/Library/Fonts/PingFang.ttc`、`/System/Library/Fonts/Hiragino Sans GB.ttc`、
`C:\Windows\Fonts\msyh.ttc`、`/usr/share/fonts/truetype/wqy/wqy-microhei.ttc`、
`/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf`、
`/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc`。正文的 XCharter 是内置的，
`-serif` / `LAYX_SERIF` 找到的系统衬线只作为它缺字时的后备。

## 界面语言（i18n）

界面文案走 MyGo 的 catalogs：`mygo.json` 里的 `i18n` 指向 `locales/`，源语言是**英文**
（`en.json`，带每条文案的说明与占位符类型），`zh-Hans.json` 是简体中文翻译。
`go tool mygo generate .`（`mygo dev` / `mygo build` 也会）从这两份 JSON 生成
`locales/catalog_gen.go`——每个 key 一个类型化函数，例如：

```go
ui.Text(c, locales.PanelContents())          // "Contents" / "目录"
ui.Text(c, locales.LibraryCount(3))          // "3 books" / "3 本书"
ui.Text(c, locales.ChapterPagesLeft(12))     // "12 pages left in chapter" / "本章还有 12 页"
```

- 语言跟随系统（`mygo.App.Locale()` 在 app 的语言里挑用户读得最顺的一个），没有切换入口。
  没有设置 `App.SetLanguages`：catalogs 的语言就是 app 的语言。
- MyGo 自己的控件文案（搜索框的 Clear、菜单角色等）随同一种语言：生成的文件按 app 的语言
  import 了 `github.com/egoist/mygo/locales/zh-Hans`。
- 语言在运行时改变时（macOS 的系统设置），`OnLocaleChanged` 会把窗口标题和菜单重新做一遍；
  视图每帧重建，正文之外的面板自己就跟着换了。已经打开的 TXT 没有标题的合成章节名
  （「第 N 节」）和书籍内容不进 catalogs，按书的内容处理。
- 新增一门语言：往 `locales/` 放一份 `<tag>.json`（key 与 `en.json` 相同，中文这种只有
  `other` 复数形式的语言直接写普通消息也行），跑 `go tool mygo i18n check .` 再
  `go tool mygo generate .`。缺翻译时回落到源语言。
- 两份 JSON 之外不要往 `locales/` 放别的东西：它同时是一个 Go 包，`*.json` 会被 embed。

## 测试

```sh
go build ./...
go vet ./...
CGO_ENABLED=0 go test ./...
go tool mygo vet .
go tool mygo i18n check .   # 文案：key、占位符、复数形式
```

`internal/book/testdata/mini.epub` 是最小测试书（两章，nav 目录）。
`internal/book/testdata/samples/` 是公有领域的真实样书（阿Q正傳 EPUB+TXT、
浮生六記 TXT），`TestSamplesOpen` 会逐个打开，截断的文件不会被误提交。
`main_test.go` 用 `ui.NewTester` 无头构建视图并验证一页真的进入了原生
`ui.Painter`；`view_test.go` 把整个窗口的每种状态（正文、三个悬浮面板、封面、首页在参考宽度/宽窗/窄窗
三种尺寸、Aa 面板、大开本两页、Quiet）都渲染一遍，`SNAP=1 go test . -run
TestWindowRendersEveryScreen` 会写出 `/tmp/snap-*.png`，人工看布局用。

## 真书回归（corpus）

```sh
go run ./cmd/corpus -dir ~/epub-corpus -v -paint
```

把目录里每本 EPUB/TXT 都走一遍 `book.Open` → `Build` → `Engine.Layout`（默认还有
`-paint`：每页的 DisplayList 都真的画进一个空 painter），逐本打印页数、耗时和
**它用了什么、读者表示了没有**。这是给"覆盖面"做的账本，不是基准测试：

- `lost:` 是真正会让人看出差别的东西——表格结构被摊平、`<pre>` 空白被折叠、
  脚注上标变成普通字符、列表标记没画；
- `unstyled:` 文字还在、样式丢了（`<cite>`、`<small>`、`<ins>`）；
- `walked:` 是纯容器（`<nav>`、`<header>`、`<footer>`），丢掉的只是标签；
- 末尾的 corpus-wide 汇总就是"该不该做某个特性"的分母：语料里用时为 0 的构造，
  不能只因为浏览器支持就排进核心。

`-paint` 会走完绘制路径，所以坏矩形、缺失的字形 run 这类问题在这里就暴露，
而不是在用户屏幕上。`internal/reader` 的常量在这里有一份副本：这个工具必须
不依赖 GTK/cgo 就能在无显示环境里跑。

## 链接

正文里的 `<a href>` 由 `epub` 映射成 `render.Node.Link`，render 给出命中矩形
（`Page.Links()` / `Tile.Links()`，分页和滚动两种模式都有）。`links.go` 把点击
变成动作：书内目标经 `epub.Book.ResolveLink` 变成 chapter + `AnchorID`，再
`Layout.Locate` 落在页或滚动位置；外部 URL 交给 `ui.SafeToOpen` 过滤后用
`Services.OpenURL` 打开，`javascript:` 之类不会执行。点击落在链接上时不再翻页。
链接文字是主题里的蓝色（`reader.Palette.Link`，深色纸上是浅蓝）带下划线。书用
`book.Options{ThemeLinks: true}` 构建，`<a>` 只写下划线、不写颜色，链接色由
`render.Typography.LinkColor` 在画的时候给，所以换主题和墨色一起重绘、不重建文档；作者
CSS 自己写了链接色的仍然以作者为准。指针移到链接上时整张书页的鼠标变成手形，移开变回箭头。

## 封面与图片

- 封面按 OPF 的顺序发现：`properties="cover-image"` → `<meta name="cover">` →
  `guide type="cover"` → 首个纯图片章节。纯图片的封面页不进入阅读流，由窗口
  整页展示；翻页或按 ←/→ 收起封面，阅读位置不变。
- 正文里的 `<img>` 和封面常见的 `<svg><image xlink:href=…>` 映射成
  `render.NodeImage`，由 layx 按栏宽缩放、分页；绘制用
  `ui.DecodeBitmap` + `ui.ImageRegion`，位图按 `ImageID` 缓存到 `UIPainter`。
- 图片 ID 在 `book.Open` 时按 manifest 顺序分配，读盘延迟到 `book.Build`；
  字节只对当前文档中真正引用到的图片保留。换书时 UI 侧必须清空位图缓存。

## TXT

- **编码**：BOM（UTF-8 / UTF-16LE / UTF-16BE）优先；合法 UTF-8 直接使用；
  否则同时尝试 GB18030（含 GBK/GB2312）与 Big5，按常用汉字命中率选一个。
- **章节**：识别 `第N章/节/回/卷`、`卷N`、`Chapter N`、以及 `序章 / 楔子 /
  后记 / 番外` 等标题行（行长 ≤ 40 且不含句末标点）。没有标题时按段落切成
  “第 N 节”；单章超过 20000 rune 会继续切成 `（2）` 续章。
- **段落**：优先按空行分块（Gutenberg 与多数导出文本），没有空行时看行首缩进，
  再退化为一行一段；硬换行的行会拼回一段，只在 ASCII 单词之间补空格。
- 标题会作为 `h1` 写进正文，所以章节名在页面上可见；书籍标题取文件名。
- 单文件上限 64 MiB。编码探测是启发式：GBK/Big5 混排或非中文单字节编码可能误判。

## 已知限制（本阶段明确不做）

- 不显示 EPUB 内置字体；图片只支持栅格格式（PNG/JPEG/GIF/WebP/BMP）。
  SVG 只提取其中嵌入的栅格图；纯矢量封面会留空白占位。
- 没有 CSS 子集：只按标签映射标题、段落、引用、列表、斜体/粗体、换行，
  不解析 `<style>`、class 或 `em/%` 单位。
- 图片没有独立的版面策略：比内容区高的插图会按栏宽缩放后被页底裁切；
  中间纯图片页不会自动整页居中。
- 没有脚注、竖排、ruby 和分享；搜索只折叠 ASCII 大小写，不折叠简繁/全半角；
  书架是打开过的书的书单（存在 `state.json` 里），`-shelf` / 「导入文件夹」只是把目录这一层
  加进书单，不递归子目录、不监听目录变化；文件被移走或磁盘没挂上时，那一行还在、只是打不开；
  目录层级靠标题猜，没用 nav 的嵌套。
  TXT 没有目录，也不区分诗歌/剧本的特殊排版；Gutenberg 的页眉页脚会成为“第 1 节”。
- 位置记忆依赖 node 号在本仓库里跨 `Build` 稳定这一事实；换书源/更新 EPUB 后旧位置会
  静默偏移。分页模式依赖 `PageFor` 的反向映射（单页文档拿不到，因此不记位置），滚动模式
  走 `store.Locate` / `store.PositionAt`，不依赖页。
- 换纸不重排：纸、墨和链接色只决定画的时候的颜色，`Layout.WithColors` 复用全部断行和分页
  （5MB 的书 2µs，耗时与书的大小无关），并在同一帧里换掉，所以内容区和外壳一起变色。行距/
  页边距/对齐走 `Relayout`：行距只重分页、复用断行缓存，页边距和对齐要重新断行，但文档和
  已算出的东西能复用就复用。换字号要重建 `Document`（字号是文档构建时的常量）并从零排版；
  换字面不重建文档，但断行 key 变了，照样要重排一次。开关悬浮面板不影响排版。
- 大开本两页是宿主摆的：排一份半宽的 `Layout`，在一张纸上画两页；安静时两页之间没有
  书脊线（Books 26 的纸是一整张），只在翻页时由宿主画边缘影子。窗口窄于约 702 DIP 时
  `双页显示` 会被忽略。
- 翻页是横移，不是 Books 的翻卷（curl）：卷页要做像素级形变，layx 现在只给显示列表，
  宿主没有整页的位图可扭。
- 滚动模式没有 Books 那种惯性滑动与卷页：滚轮按平台给的位移立刻滚动，键盘一屏一屏跳；
  滚动位置按 `Location` 记录，重排后落在同一行的行首，不是同一个像素。没有 wasm / 网页版本。
- 字体集只有一个字面（face），所以粗体和斜体在当前字体没有对应 face 时
  只表现为同一字形。
