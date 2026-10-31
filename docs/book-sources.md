# 书源：一个接口，三个各自独立的实现

本文是书源的契约，不是使用说明。改动接口、规则子集或下载格式前先改这里，再改代码。

## 目标

Quire 的阅读、排版、书签、笔记、统计全部只认本地文件。书源只做两件事：

1. `Search`：按关键词找到书；
2. `Download`：把一本书取回来，变成一个 EPUB 或 TXT。

下载完成后走现有的 `library.Scan` / `book.Open`，所以断网也能读，书源失效也不会影响已经
在书架上的书。

## 分层

| 包 | 责任 | 不做什么 |
| --- | --- | --- |
| `internal/source` | `Source` 接口、`Registry`、错误语义、URL/文本小工具 | 不联网、不读配置、不碰 UI |
| `internal/source/zlib` | Z-Library：登录、搜索、下载 | 不 import 其他书源 |
| `internal/source/legado` | 「阅读 3.0」书源 JSON：解析、规则子集、搜索/目录/正文 | 不 import 其他书源 |
| `internal/source/wenku8` | 轻小说文库：搜索、目录、章节、拼 EPUB | 不 import 其他书源 |
| `internal/epubwrite` | 章节 HTML → 合法 EPUB | 不联网 |
| `internal/acquire` | 下载落到书架目录：原子写、文件名、大小上限 | 不认识具体书源 |

三个书源包之间**没有 import 关系**，都只 import `internal/source`（需要出 EPUB 时再
import `internal/epubwrite`）。注册表是唯一把它们放在一起的地方。

## 接口

见 `internal/source/source.go`。要点：

- `ID()` 稳定且唯一；`Name()` 给人看。
- `Caps()` 说明能搜索/能下载/需要登录，UI 据此决定按钮。
- `Search(ctx, Query)` 必须尊重 `ctx`；分页从 1 开始。
- `Download(ctx, Book, progress)` 只在 `Caps().Download` 时调用；返回内存里的 `File`。
- 错误用 `ErrAuthRequired` / `ErrNotFound` / `ErrUnsupported` / `ErrRateLimited` /
  `ErrBadResponse` 包装，UI 不需要 import 具体书源就能分辨。
- `Book.Key` 与 `Book.Extra` 是书源自己的令牌，UI 原样带回，不解释。

配置是数据：实现 `Configurable` 的书源给出 `[]Setting`，宿主负责显示、保存和回填。
导入型书源（Legado）实现 `Importer`，宿主只负责选文件、存 JSON、报告数量。

## 独立性怎么保证

- `Registry.Search` 每个书源一个 goroutine，结果按注册顺序返回；一个书源的 error、
  panic、超时只出现在它自己的 `Outcome` 里。
- 每个书源自带 `http.Client`、超时、User-Agent、限速和重试策略，不共享可变状态。
- 书源的配置按 `ID()` 分开保存；删掉一个书源不会动另一个的配置。
- 测试全部离线：`httptest` + `testdata/` 固定响应；不允许在测试里访问真实站点。

## 三个书源的约定

### Z-Library（`zlib`）

- 设置：站点根 URL、邮箱、密码。站点经常换域名，必须可配置；不把任何域名或凭据写死。
- 登录后保存 cookie；`Search` 懒登录，401/登录页 → `ErrAuthRequired`。
- 搜索走站点自己的搜索入口，解析结果卡片；下载取 EPUB（没有 EPUB 时取 TXT）。
- 限速：请求之间至少留一个可配置的间隔，默认不小于 1 秒；429 → `ErrRateLimited`。

### 阅读 3.0（`legado`）

支持导入标准 Legado 书源 JSON（数组或单个对象）。只实现可离线测试的子集，遇到不支持的
规则必须明确报 `ErrUnsupported`，不能静默返回空结果。

支持：

- 字段：`bookSourceName`、`bookSourceUrl`、`bookSourceGroup`、`enabled`、`searchUrl`、
  `ruleSearch`、`ruleBookInfo`、`ruleToc`、`ruleContent`、`bookSourceCharset`。
- `searchUrl` 的 `{{key}}` / `{{searchKey}}` / `{{page}}` / `{{baseUrl}}` 模板，
  以及 Legado 的 `url,{"method":"POST","body":"..."}` 形式。
- 规则：CSS 选择器（`@css:` 或裸选择器）的 `@text` / `@html` / `@href` / `@src` /
  `@attr(name)`；`@json:` 的简单点/下标路径；`##正则##替换`。
- 相对 URL 按 `bookSourceUrl` 解析。
- 字符集：UTF-8、GBK/GB18030（用 `golang.org/x/text`）。

不支持（报 `ErrUnsupported`）：`@js:`、需要 WebView 的规则、登录/验证码、动态加密。

搜索结果是聚合的：一个 `legado` 源持有多个已启用的书源定义，`Book.Extra["source"]`
记录定义下标，`Key` 记录书 URL。一个定义失败不影响其他定义。

下载：书信息页 → 目录页 → 逐章正文 → `epubwrite.Bytes`。章节抓取要并发有上限、
尊重 `ctx`、报告 `Progress{Stage:"chapters"}`。

### 轻小说文库（`wenku8`）

- 搜索：`search.php?searchkey=...&t=1`，解析结果行；作者、状态、简介尽量取。
- 目录：书的 `article/...` 页或目录入口，按卷/章解析。
- 正文：章节页的正文容器，去掉广告/导航；相对链接补全。
- 下载：正文按章拼成一个 EPUB，书名/作者/简介来自书页。
- 站点字符集可能是 GBK；解码后再解析。
- 只读公开页面，不登录、不碰需要权限的内容。

## 下载落盘

`internal/acquire.Manager`：

1. 调 `Source.Download`，校验 `ValidateFile`；
2. 文件名 = `Prefix + 书名 - 作者 + .扩展名`，去掉路径分隔符与控制字符，限制长度；
3. 同名文件加 ` (2)`；
4. 写 `*.part`，`Sync`，`Rename`；失败/取消删临时文件；
5. 返回 `Saved{Path,...}`，宿主用 `appstate` 把它加进书架并刷新。

## 验收

- `go build ./... && go vet ./...`
- `CGO_ENABLED=0 go test ./...`
- 三个书源各自有 fixture 测试：搜索、解析、下载；错误路径（401、429、坏 HTML、空结果）
  各有断言。
- `go tool mygo vet .` 与 `go tool mygo i18n check .` 仍通过。
