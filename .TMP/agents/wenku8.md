# 子代理任务：轻小说文库书源（wenku8）

你是 Quire 的一个子代理，只负责一个完全独立的书源包。工作目录：
`/home/laix/quire-wt/wenku8`。这里是一个 git worktree，分支 `source/wenku8`。

## 先读

1. `/home/laix/quire-wt/wenku8/docs/book-sources.md`（书源契约）
2. `/home/laix/quire-wt/wenku8/internal/source/source.go`
3. `/home/laix/quire-wt/wenku8/internal/source/registry.go`
4. `/home/laix/quire-wt/wenku8/internal/epubwrite/epubwrite.go`（当前是契约/桩）
5. 仓库现有代码风格（`gofmt`、英文注释、表驱动测试）。

## 所有权

**只允许新增/修改：**

- `internal/source/wenku8/**`

其他任何文件都不要动，尤其不要改 `go.mod`、`go.sum`、`internal/source/*.go`、`internal/epubwrite/**`、`main.go`、`view.go`、`locales/**`、其他书源包。不要引入新依赖；可用 stdlib、`golang.org/x/net`、`golang.org/x/text`（go.mod 里已有）。

## 目标

实现 `internal/source/wenku8`，满足 `source.Source`（可选 `source.Configurable`）：

```go
const ID source.ID = "wenku8"

type Builder func(epubwrite.Book, []epubwrite.Chapter) ([]byte, error)

type Options struct {
    BaseURL     string        // 默认 "https://www.wenku8.net"；测试替换成 httptest URL
    Client      *http.Client
    Timeout     time.Duration
    MaxBytes    int64
    MaxChapters int           // 默认 2000
    Concurrency int           // 章节抓取并发，默认 4，必须 >0 且有上限
    Build       Builder       // nil 时用 epubwrite.Bytes；测试注入假实现
}

func New(opts Options) *Source
```

- `ID()` 返回 `"wenku8"`；`Name()` 返回 `"轻小说文库"`。
- `Caps()`：`Search: true, Download: true, NeedsLogin: false`。
- 实现 `Configurable` 只暴露 `site`（BaseURL），便于域名变化；`Secret: false`。
- `Search(ctx, q)`：请求 `{base}/modules/article/search.php?searchkey=<urlencoded>&searchtype=all`（或站点传统入口），解析结果行：
  - 书名、作者、状态/卷数、最后更新、简介；
  - `Key` 用书籍详情 URL（`/book/<id>.htm` 形式）或绝对 URL；
  - `Extra` 放书 id / 详情 URL。
  - 搜索页可能是 GBK/GB2312：用 `golang.org/x/net/html/charset` 按 `Content-Type` 或 meta 解码，不要假设 UTF-8。
  - 空结果返回空 `Result`；404 → `ErrNotFound`；非 HTML/结构不对 → `ErrBadResponse`。
- `Download(ctx, b, progress)`：
  1. 取书页（`/book/<id>.htm`），解析书名、作者、简介、封面（可选）、目录入口；
  2. 取目录页，按卷/章解析章节标题与链接，补全相对 URL；
  3. 并发抓章节正文（上限 `Concurrency`，尊重 `ctx`，用 `errgroup` 手写即可，不引新依赖），限速温和；
  4. 正文容器通常是 `#content`；去掉 script/style/iframe/广告/导航，保留段落、换行、图片 alt；
  5. 调用 `Build(epubwrite.Book{...}, []epubwrite.Chapter{...})` 得到 EPUB 字节；
  6. 返回 `source.File{Name: 书名.epub, Format:"epub", Data: bytes}`，并报告 `Progress{Stage:"chapters"}`。
  - 章节数超过 `MaxChapters` 或单页超过 `MaxBytes` → `ErrBadResponse`/明确错误；
  - 单章失败：重试一次；仍失败则整体失败并带章节标题，不要静默缺章。
- 不要登录，不要碰需要权限的内容。

## 测试（必须离线）

只用 `httptest.NewServer` + 内联/`testdata/` 固定页面，**不要访问真实站点**。至少覆盖：

1. UTF-8 搜索页解析（书名/作者/详情 URL）。
2. GBK 搜索页正确解码中文。
3. 书页 + 目录页 + 两章正文 → 下载，断言 `Build` 收到的书名/章节标题/正文，并断言返回 EPUB（`PK` 开头）。
4. 相对链接补全。
5. 404 → `ErrNotFound`；坏 HTML → `ErrBadResponse`；空结果不 panic。
6. 章节超限 / 下载超限。
7. 正文去广告：`script`、导航、广告 div 不出现在 HTML 里。

## 完成标准

- `gofmt -w internal/source/wenku8`
- `CGO_ENABLED=0 go test ./internal/source/wenku8/...` 通过
- `go vet ./internal/source/wenku8/...` 通过
- 在当前分支只提交你的文件：
  `git add internal/source/wenku8 && git commit -m "source/wenku8: 轻小说文库搜索与 EPUB 下载"`
- 不要 push，不要合并，不要改主工作区。

最终回复用中文，列出：新增文件、测试命令与结果、对外 API、任何契约偏差/联调注意点（尤其 `epubwrite` 桩在联调时要换成真实现）。
