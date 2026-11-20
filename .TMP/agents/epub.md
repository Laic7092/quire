# 子代理任务：EPUB 写出与下载落盘（epubwrite + acquire）

你是 Quire 的一个子代理，负责两个纯基础设施包。工作目录：
`/home/laix/quire-wt/epub`。这里是一个 git worktree，分支 `source/epub`。

## 先读

1. `/home/laix/quire-wt/epub/docs/book-sources.md`（书源契约）
2. `/home/laix/quire-wt/epub/internal/source/source.go`
3. `/home/laix/quire-wt/epub/internal/epubwrite/epubwrite.go`（当前是契约/桩，要你实现）
4. `/home/laix/quire-wt/epub/internal/acquire/acquire.go`（当前是契约/桩，要你实现）
5. `/home/laix/layx/epub/` 的解析器（`epub.Open`、`ReadMetadata`），确保写出的 EPUB
   能被 Quire 真正打开。重点看它要求的 container.xml、OPF、spine、nav/NCX、XHTML 子集。
6. 仓库现有代码风格（`gofmt`、英文注释、表驱动测试）。

## 所有权

**只允许新增/修改：**

- `internal/epubwrite/**`
- `internal/acquire/**`

其他任何文件都不要动，尤其不要改 `go.mod`、`go.sum`、`internal/source/*.go`、
`main.go`、`view.go`、`locales/**`、其他书源包。不要引入新依赖；可用 stdlib、
`golang.org/x/net`、`golang.org/x/text`（go.mod 里已有）。

## 1. `internal/epubwrite`

把现有契约实现完整：

- `Write(w io.Writer, b Book, chapters []Chapter) (int64, error)`
- `Bytes(b Book, chapters []Chapter) ([]byte, error)`

要求：

- EPUB 3 布局：第一个 entry 必须是 **stored（不压缩）** 的 `mimetype`，内容
  `application/epub+zip`；接着 `META-INF/container.xml`、`OEBPS/content.opf`、
  `OEBPS/nav.xhtml`，每章 `OEBPS/chapter-N.xhtml`，可选封面。
- 同时写 `OEBPS/toc.ncx`（EPUB 2 兼容），确保 layx/epub 的 NCX 路径也能识别。
- OPF：`dc:title`、`dc:creator`、`dc:language`（空则 `und`）、`dc:identifier`
  （空则生成稳定 UUID/urn）、`dc:description`、`dc:source`、cover meta；spine 按章节顺序。
- nav.xhtml：每章一个链接，标题为空用 `Chapter N`；书名空用 `Untitled`。
- 章节 HTML 规范化：
  - 用 `golang.org/x/net/html` 解析 fragment（不是整文档）；
  - 去掉 `script`、`style`、`iframe`、`object`、`embed`、表单控件；
  - `img` 去掉，保留 `alt` 文本；`a` 保留文本、去掉 `javascript:`；
  - 保留 `p/div/br/h1-h6/blockquote/ul/ol/li/em/strong/i/b/u/s/code/pre/ruby/rt/rp/span`；
  - 输出必须是 well-formed XHTML（用 `encoding/xml` 解码验证）；
  - 文本转义，实体 `&nbsp;` 等归一；
  - 空章节也要有合法 body。
- 输入 `Book.Cover` 是 PNG/JPEG：写进 `OEBPS/cover.(png|jpg)`，OPF 声明
  `properties="cover-image"`，nav 或 guide 指向它；无法识别则忽略而不是失败。
- 章节数 0 时返回明确错误（`ErrUnavailable` 删除；用普通 error）。
- 目录名/文件名固定，不接受外部路径，杜绝 zip slip。
- 所有 zip entry 的修改时间固定（例如 1980-01-01），便于测试稳定；但不要承诺字节级可复现。
- 不需要保留 `ErrUnavailable`；实现后删掉它（如果其他包没引用）。

测试（`internal/epubwrite`）：

1. `Bytes` 后用 `zip.NewReader` 检查：mimetype 第一且 stored、container/OPF/nav/NCX/章节都在。
2. 用 `encoding/xml` 验证每个 XHTML 与 OPF 是 well-formed。
3. 未闭合标签、`&nbsp;`、 `<br>`、脚本、远程图片、空章节、特殊字符 `<>&"'`。
4. 封面 PNG/JPEG 被写入并声明；坏封面被忽略。
5. 书名/作者/语言为空时的回退。
6. **集成测试**：把 `Bytes` 写到临时 `.epub`，用 `github.com/laix/layx/epub.Open`
   打开，断言章节数、标题、正文包含预期文字。这是最重要的验收。

## 2. `internal/acquire`

把现有契约实现完整：

- `New(Options) *Manager`
- `(*Manager).Save(ctx, src source.Source, b source.Book, progress func(source.Progress)) (Saved, error)`

要求：

- `Options.Dir` 为空 → `ErrNotConfigured`。
- 调 `src.Download(ctx, b, progress)`；用 `source.ValidateFile` 校验。
- `Options.MaxBytes > 0` 且 `len(Data) > MaxBytes` → 明确错误（包装 `source.ErrBadResponse`）。
- 文件名：优先 `source.File.Name`，否则 `Book.Title`；去目录、控制字符、`/\:*?"<>|`；
  去首尾空白和点；限制 120 字节左右（按 rune 截断）；扩展名必须与 `Format` 一致
  （没有就补 `.epub`/`.txt`）；空名回退 `book-<timestamp>`。
- `Options.Prefix` 加在文件名前。
- 同名文件存在则加 ` (2)`、` (3)`…；不得覆盖已有书。
- 原子写：在目标目录 `CreateTemp` → 写入 → `Sync` → `Close` → `Rename`；
  任何错误/`ctx` 取消都删除临时文件。目录 `MkdirAll(0755)`，文件 `0644`。
- `Saved.Path` 是绝对路径；`Size` 是实际写入字节数；`Title/Author/Format` 来自输入。
- 并发安全（同名竞争也不能互相覆盖；用 O_EXCL 或 rename 前重查 + 目录级 mutex 均可，
  但要在测试里证明）。

测试（`internal/acquire`）：用假 `source.Source`，覆盖成功、同名、路径穿越文件名、
非法下载、超限、取消、进度回调、目录不存在、Prefix。断言失败后目录里没有 `.part` 残留。

## 完成标准

- `gofmt -w internal/epubwrite internal/acquire`
- `CGO_ENABLED=0 go test ./internal/epubwrite/... ./internal/acquire/...` 通过
- 额外跑一次 `CGO_ENABLED=0 go test ./internal/...`（其他包可能还是桩，但应能编译）
- `go vet ./internal/epubwrite/... ./internal/acquire/...` 通过
- 在当前分支只提交你的文件：
  `git add internal/epubwrite internal/acquire && git commit -m "epubwrite, acquire: 章节转 EPUB 与原子落盘"`
- 不要 push，不要合并，不要改主工作区。

最终回复用中文，列出：新增文件、测试命令与结果、EPUB 结构说明、`acquire` 的文件名/原子写规则、
任何与 layx/epub 的兼容性注意点。
