# 子代理任务：Z-Library 书源（zlib）

你是 Quire 的一个子代理，只负责一个完全独立的书源包。工作目录：
`/home/laix/quire-wt/zlib`。这里是一个 git worktree，分支 `source/zlib`。

## 先读

1. `/home/laix/quire-wt/zlib/docs/book-sources.md`（书源契约）
2. `/home/laix/quire-wt/zlib/internal/source/source.go`
3. `/home/laix/quire-wt/zlib/internal/source/registry.go`
4. 仓库现有代码风格（`gofmt`、英文注释、表驱动测试）。

## 所有权

**只允许新增/修改：**

- `internal/source/zlib/**`

其他任何文件都不要动，尤其不要改 `go.mod`、`go.sum`、`internal/source/*.go`、`main.go`、`view.go`、`locales/**`、其他书源包。不要引入新依赖；可用 stdlib、`golang.org/x/net`、`golang.org/x/text`（go.mod 里已有）。

## 目标

实现 `internal/source/zlib`，满足 `source.Source` 与 `source.Configurable`：

```go
const ID source.ID = "zlib"

type Options struct {
    Site        string        // 站点根 URL，例如 https://example.invalid
    Email       string
    Password    string
    Client      *http.Client  // nil 时用带超时的默认 client
    MinInterval time.Duration // 两次请求最小间隔，默认 1s
    MaxBytes    int64         // 响应体上限，默认 64<<20
    Timeout     time.Duration // 单请求超时，默认 30s
}

func New(opts Options) *Source
type Source struct { /* ... */ }
```

- `ID()` 返回 `"zlib"`；`Name()` 返回 `"Z-Library"`。
- `Caps()`：`Search: true, Download: true, NeedsLogin: true`。
- `Settings()` 返回 `site`、`email`、`password` 三项；password `Secret: true`，site/email `Required: true`。`site` 的 `Value` 回填当前值，password 不回填。
- `Configure(values map[string]string) error`：支持部分更新；site 变化时清掉登录态；空 site 返回错误。配置变化后下一次 Search 重新登录。
- `Search(ctx, q)`：懒登录，然后请求站点搜索入口；解析结果卡片；返回 `source.Book`，`Source` 填 `ID()`，`Key` 用书籍详情 URL 或站点书籍 id（能唯一找回这本书），`Extra` 可放详情 URL、下载页 URL 等。
- `Download(ctx, b, progress)`：进入书籍详情页，找到 EPUB 下载入口；没有 EPUB 时接受 TXT；返回 `source.File{Name, Format, Data}`。下载前/后调用 `progress`（`Stage: "login"/"search"/"detail"/"download"`）。
- 登录/会话：保留 cookie（`http.CookieJar`）；401/403 或登录页 → `fmt.Errorf("%w: ...", source.ErrAuthRequired)`；429 → `ErrRateLimited`；404 → `ErrNotFound`；HTML 结构对不上 → `ErrBadResponse`。
- 所有请求带 UA、`ctx`、超时、响应体上限；请求之间按 `MinInterval` 限速（用 mutex 串行化）。
- 站点结构会变：解析要容忍多种卡片（至少支持 `div.book-item` / `z-bookcard` / 结果表格 `table` 三种常见形态）；选择器/字段集中在一处，方便以后改。不要硬编码任何真实域名或凭据。

## 测试（必须离线）

只用 `httptest.NewServer` + 内联 HTML 或 `testdata/` 固定响应，**不要访问真实站点**。至少覆盖：

1. 登录表单 + cookie + 搜索页解析（标题/作者/格式/大小/封面/详情 URL）。
2. 未登录时 401 → `ErrAuthRequired`。
3. 429 → `ErrRateLimited`。
4. 详情页 → EPUB 下载（返回 `PK` 开头），以及无 EPUB 时回退 TXT。
5. 坏 HTML / 空结果 → `ErrBadResponse` 或空结果，不 panic。
6. `Configure` 部分更新、切换 site 后重新登录。
7. 相对/绝对下载链接都能解析。

## 完成标准

- `gofmt -w internal/source/zlib`
- `CGO_ENABLED=0 go test ./internal/source/zlib/...` 通过
- `go vet ./internal/source/zlib/...` 通过
- 在当前分支只提交你的文件：
  `git add internal/source/zlib && git commit -m "source/zlib: Z-Library 搜索与下载"`
- 不要 push，不要合并，不要改主工作区。

最终回复用中文，列出：新增文件、测试命令与结果、对外 API、任何契约偏差/联调注意点。
