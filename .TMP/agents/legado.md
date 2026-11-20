# 子代理任务：「阅读 3.0」书源（legado）

你是 Quire 的一个子代理，只负责一个完全独立的书源包。工作目录：
`/home/laix/quire-wt/legado`。这里是一个 git worktree，分支 `source/legado`。

这是三个书源里最复杂的一个：你实现的是一个**兼容 Legado 书源 JSON 的规则引擎子集**，
不是某一个具体站点。宁可明确报 `ErrUnsupported`，也不要静默返回空结果。

## 先读

1. `/home/laix/quire-wt/legado/docs/book-sources.md`（书源契约，尤其「阅读 3.0」一节）
2. `/home/laix/quire-wt/legado/internal/source/source.go`
3. `/home/laix/quire-wt/legado/internal/source/registry.go`
4. `/home/laix/quire-wt/legado/internal/epubwrite/epubwrite.go`（当前是契约/桩）
5. 仓库现有代码风格（`gofmt`、英文注释、表驱动测试）。

## 所有权

**只允许新增/修改：**

- `internal/source/legado/**`

其他任何文件都不要动，尤其不要改 `go.mod`、`go.sum`、`internal/source/*.go`、
`internal/epubwrite/**`、`main.go`、`view.go`、`locales/**`、其他书源包。不要引入新依赖；
可用 stdlib、`golang.org/x/net`、`golang.org/x/text`（go.mod 里已有）。

## 目标

实现 `internal/source/legado`，满足 `source.Source` 与 `source.Importer`：

```go
const ID source.ID = "legado"

type Builder func(epubwrite.Book, []epubwrite.Chapter) ([]byte, error)

type Options struct {
    Client         *http.Client
    Timeout        time.Duration // 单请求，默认 30s
    MaxBytes       int64         // 单响应上限，默认 8<<20
    MaxChapters    int           // 默认 5000
    Concurrency    int           // 搜索/章节并发，默认 4
    Build          Builder       // nil 时用 epubwrite.Bytes；测试注入假实现
}

func New(opts Options) *Source
```

- `ID()` 返回 `"legado"`；`Name()` 返回 `"阅读 3.0"`。
- `Caps()`：`Search: true, Download: true, NeedsLogin: false`。
- `Import(data []byte) (added int, err error)`：接受标准 Legado 书源 JSON 的数组或单个对象。
  只保留 `enabled != false` 且 `bookSourceName`、`bookSourceUrl`、`searchUrl` 非空、规则可解析的定义；
  重复 `bookSourceUrl` 去重。Import **替换**当前定义集（一次导入就是一份完整书源列表），
  返回可用定义数。JSON 语法错 → 错误；单条无效 → 跳过并在返回里体现计数，不要整批失败。
- `Export() ([]byte, error)`：导出当前定义（原始结构，格式化 JSON 即可），供备份。
- `Search(ctx, q)`：对每个可用定义并发请求搜索页，按定义顺序聚合结果；一个定义失败不影响其他定义。
  全部失败且没有结果时返回第一个可操作错误（登录/限速/网络）。结果 `Book.Source = ID()`，
  `Key` 用书籍详情 URL，`Extra["def"]` 放定义下标，`Extra["bookUrl"]` 放绝对 URL。
- `Download(ctx, b, progress)`：按 `Extra["def"]` 找定义，依次：
  书信息页（可选 `ruleBookInfo`）→ 目录页（`ruleToc`）→ 逐章正文（`ruleContent`）→
  `Build(epubwrite.Book{...}, []epubwrite.Chapter{...})`；返回 `source.File{Name: 书名.epub, Format:"epub"}`。
  章节并发有上限（Options.Concurrency），尊重 `ctx`，`Progress{Stage:"chapters"}`。
- 所有请求带 UA、超时、响应体上限、`ctx`；相对 URL 用 `source.Combine` 解析。
- 字符集：支持 `bookSourceCharset`，以及从 `Content-Type`/meta 探测；UTF-8、GBK/GB18030。

## JSON 字段

至少解析这些（标准 Legado 3.0 字段名，Go tag 用原名）：

```json
{
  "bookSourceName": "...", "bookSourceUrl": "https://...", "bookSourceGroup": "...",
  "bookSourceCharset": "gbk", "enabled": true,
  "searchUrl": "/search?q={{key}}&page={{page}}",
  "ruleSearch": {
    "bookList": "class.book-item",
    "name": "tag.a@text", "author": "class.author@text",
    "kind": "...", "wordCount": "...", "lastChapter": "...",
    "intro": "class.intro@text", "coverUrl": "tag.img@src", "bookUrl": "tag.a@href"
  },
  "ruleBookInfo": { "name": "...", "author": "...", "intro": "...", "coverUrl": "...", "tocUrl": "..." },
  "ruleToc": { "chapterList": "class.chapter@tag.a", "chapterName": "text", "chapterUrl": "href" },
  "ruleContent": { "content": "id.content@html" }
}
```

`ruleExplore` 可以忽略并在文档里写明；`ruleSearch` 缺少 `bookList` 的定义不可用。

## 规则子集（必须实现并测试）

规则值先按 `,{...}` 尾部拆出请求选项（`{"method":"POST","body":"..."}`）。

1. **CSS 选择器**：
   - 支持 `@css:` 前缀或裸选择器；支持 `tag`、`.class`、`#id`、
     `[attr]`、`[attr=value]`、后代空格、`>`、逗号分组。
   - 支持 Legado 传统 `a@b` 链：`class.book-list@tag.li` 表示先选 `.book-list`，
     再在其后代里选 `li`。按 `@` 拆分，但不要拆 `@css:`、`@json:`、`@attr(...)`。
   - 末端取值：`@text`、`@html`、`@href`、`@src`、`@attr(name)`；没有末端时取 `text`。
   - `@text` 用节点文本并 `source.CleanText`；`@html` 保留内部 HTML。
2. **JSONPath 子集**：`@json:$.data.books[*].title`、`$.data.title`、`[0]`。
   支持 `[*]` 展开；规则末尾没有路径时取整个 JSON 的文本。
3. **正则**：`##pattern##replacement`，也接受 `regex:pattern`（只取匹配）。
   支持 `$1` 替换；pattern 编译失败 → `ErrUnsupported`。
4. **模板**：`{{key}}`/`{{searchKey}}`（URL 编码）、`{{page}}`、`{{baseUrl}}`；
   URL 里未编码的中文也要能工作。
5. **明确不支持**：`@js:`、`<js>`、WebView、登录/验证码、动态解密。
   规则求值时遇到 → 包装 `ErrUnsupported`，错误里带定义名和规则片段。

选择器/节点工具放在你自己的包内（例如 `html.go`、`rule.go`），不要抽到共享包。

## 测试（必须离线）

只用 `httptest.NewServer` + 内联/`testdata/` 固定页面，**不要访问真实站点**。至少覆盖：

1. `Import`：数组、单对象、无效 JSON、重复 URL、缺字段、`enabled:false`。
2. CSS 规则：`class.book-item`、`tag.a@href`、`@css:`、`@attr(...)`、`@html`、逗号分组。
3. JSONPath：数组展开、单值、下标。
4. 正则替换与 `$1`。
5. `searchUrl` 模板与 POST 选项（`url,{"method":"POST","body":"..."}`）。
6. 端到端：Import 一个 fixture 书源 → Search 解析两本书 → Download：书页 → 目录 → 两章正文 →
   假 `Builder` 收到正确章节 → 返回 `PK` 开头的 EPUB。
7. GBK 搜索页正确解码。
8. 一个定义失败、另一个成功：结果仍返回成功的那本。
9. `@js:` 规则 → `ErrUnsupported`。
10. 空结果、404、429、坏 HTML 各有用例。

## 完成标准

- `gofmt -w internal/source/legado`
- `CGO_ENABLED=0 go test ./internal/source/legado/...` 通过
- `go vet ./internal/source/legado/...` 通过
- 在当前分支只提交你的文件：
  `git add internal/source/legado && git commit -m "source/legado: 阅读 3.0 书源 JSON 与规则子集"`
- 不要 push，不要合并，不要改主工作区。

最终回复用中文，列出：新增文件、测试命令与结果、支持的规则子集与明确不支持项、对外 API、
任何契约偏差/联调注意点。
