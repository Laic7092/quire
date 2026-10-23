# layx-reader

用 MyGo 原生 UI（`package ui`，没有任何 webview / TypeScript 前端）实现的 EPUB
分页阅读器。断行、分页、行位置和字形位置全部由本地 `github.com/laix/layx`
的 `render` 引擎产出；本仓库只做三件事：

1. `internal/book`：`archive/zip` + `encoding/xml` 解析 EPUB（container.xml →
   OPF → spine），用 `golang.org/x/net/html` 的 tokenizer 把 XHTML 映射成
   `render.Document` 的最小标签子集。
2. `internal/reader`：加载字体（`LAYX_FONT` / `-font` / 系统 CJK 字体），调用
   `render.Engine.Layout`，并实现 `paint.Painter`，把 layx 的 `DisplayList`
   通过 `ui.Painter` / `ui.Path` / `FillPath` 画出来（字形来自 go-text 轮廓）。
3. `main.go`：一个 `ui.View` 窗口，工具栏（打开 / 上一页 / 下一页 / A- / A+ /
   页码 / 章节名），←/→ 翻页，+/- 改字号，`Window.Update` 发布后台排版结果。

## 运行

```sh
go run . [-font /path/to/font.ttc] [book.epub]
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
`main_test.go` 用 `ui.NewTester` 无头构建视图并验证一页真的进入了原生
`ui.Painter`。

## 已知限制（本阶段明确不做）

- 不显示 EPUB 内的图片、封面、字体。
- 没有 CSS 子集：只按标签映射标题、段落、引用、列表、斜体/粗体、换行。
- 没有脚注、竖排、ruby、搜索、书签、TOC 侧边栏、阅读进度持久化。
- 只有分页模式，没有滚动模式；没有 wasm / 网页版本。
- 字体集只有一个字面（face），所以粗体和斜体在当前字体没有对应 face 时
  只表现为同一字形。
