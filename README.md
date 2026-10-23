# layx-reader

用 MyGo 原生 UI（`package ui`，没有任何 webview / TypeScript 前端）实现的 EPUB / TXT
分页阅读器。断行、分页、行位置和字形位置全部由本地 `github.com/laix/layx`
的 `render` 引擎产出；本仓库做四件事：

1. `internal/book`：`archive/zip` + `encoding/xml` 解析 EPUB（container.xml →
   OPF → spine），用 `golang.org/x/net/html` 的 tokenizer 把 XHTML 映射成
   `render.Document` 的最小标签子集，并把图片映射成 `render.NodeImage`；同时
   解析 TXT（编码探测、章节标题、段落切分）并复用同一条 `Chapter` 管线。
2. `internal/reader`：加载字体（`LAYX_FONT` / `-font` / 系统 CJK 字体），调用
   `render.Engine.Layout`，并实现 `paint.Painter`，把 layx 的 `DisplayList`
   通过 `ui.Painter` / `ui.Path` / `FillPath` 画出来（字形来自 go-text 轮廓，
   位图来自 `ui.DecodeBitmap` + `ui.ImageRegion`）。
3. `main.go`：一个 `ui.View` 窗口，工具栏（打开 / 上一页 / 下一页 / A- / A+ /
   页码 / 章节名），←/→ 翻页，+/- 改字号，`Window.Update` 发布后台排版结果。
4. `docs/integration-notes.md`：作为宿主的联调记录，以及对 layx 形态的意见。

## 运行

```sh
go run . [-font /path/to/font.ttc] [book.epub|book.txt]
# 或
LAYX_FONT=/usr/share/fonts/truetype/wqy/wqy-microhei.ttc go run . book.epub
```

默认窗口 900×700；默认字体依次尝试
`/usr/share/fonts/truetype/wqy/wqy-microhei.ttc`、
`/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf`、
`/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc`。

## 测试

```sh
go build ./...
go vet ./...
CGO_ENABLED=0 go test ./...
go tool mygo vet .
```

`internal/book/testdata/mini.epub` 是最小测试书（两章，nav 目录）。
`internal/book/testdata/samples/` 是公有领域的真实样书（阿Q正傳 EPUB+TXT、
浮生六記 TXT），`TestSamplesOpen` 会逐个打开，截断的文件不会被误提交。
`main_test.go` 用 `ui.NewTester` 无头构建视图并验证一页真的进入了原生
`ui.Painter`。

## 真书回归（corpus）

```sh
go run ./cmd/corpus -dir ~/epub-corpus -v -paint
```

把目录里每本 EPUB/TXT 都走一遍 `book.Open` → `Build` → `Engine.Layout`（默认还有
`-paint`：每页的 DisplayList 都真的画进一个空 painter），逐本打印页数、耗时和
**它用了什么、读者表示了没有**。这是给"覆盖面"做的账本，不是基准测试：

- `lost:` 是真正会让人看出差别的东西——表格结构被摊平、`<pre>` 空白被折叠、
  脚注上标变成普通字符、列表标记没画、链接不可跳转；
- `unstyled:` 文字还在、样式丢了（`<cite>`、`<small>`、`<ins>`）；
- `walked:` 是纯容器（`<nav>`、`<header>`、`<footer>`），丢掉的只是标签；
- 末尾的 corpus-wide 汇总就是"该不该做某个特性"的分母：语料里用时为 0 的构造，
  不能只因为浏览器支持就排进核心。

`-paint` 会走完绘制路径，所以坏矩形、缺失的字形 run 这类问题在这里就暴露，
而不是在用户屏幕上。`internal/reader` 的常量在这里有一份副本：这个工具必须
不依赖 GTK/cgo 就能在无显示环境里跑。

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
- 没有脚注、竖排、ruby、搜索、书签、TOC 侧边栏、阅读进度持久化。TXT 没有目录，
  也不区分诗歌/剧本的特殊排版；Gutenberg 的页眉页脚会成为“第 1 节”。
- 只有分页模式，没有滚动模式；没有 wasm / 网页版本。
- 字体集只有一个字面（face），所以粗体和斜体在当前字体没有对应 face 时
  只表现为同一字形。
