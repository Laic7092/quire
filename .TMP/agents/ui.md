# 子代理任务：书源搜索/下载 UI 与配置（主包 + sourceconf）

你是 Quire 的一个子代理，负责把书源接到现有 MyGo 界面上。工作目录：
`/home/laix/quire-wt/ui`。这里是一个 git worktree，分支 `source/ui`。

**你和其他子代理并行工作**：zlib / legado / wenku8 三个书源包和 epubwrite/acquire 正在另外的
worktree 里实现。你只依赖它们的**契约**，不要实现或修改它们。接口与桩已在主仓：
`internal/source/`、`internal/epubwrite/`、`internal/acquire/`。

## 先读

1. `/home/laix/quire-wt/ui/docs/book-sources.md`
2. `/home/laix/quire-wt/ui/internal/source/source.go`
3. `/home/laix/quire-wt/ui/internal/source/registry.go`
4. `/home/laix/quire-wt/ui/internal/acquire/acquire.go`（契约/桩）
5. `/home/laix/quire-wt/ui/internal/epubwrite/epubwrite.go`（契约/桩）
6. 现有 UI：`view.go`（`library`、`sourceList`、`sourceRow`、`sourceHeading`、`home`、
   `removeDialog`）、`main.go`（`app` 字段、`chooseFile/chooseDir`、`main`）、
   `windows.go`（`desk`、`newApp`）、`settings.go`（`ui.Modal`/`PopoverBase` 用法）、
   `internal/appstate/appstate.go`（`ImportDir`、`ReloadShelf`、`Open`）。
7. `ui` 包 API：`ui.Modal`、`ui.DialogBase`、`ui.List`、`ui.ListState`、`ui.SearchField`、
   `ui.TextField`、`ui.Button`、`ui.Progress`、`ui.Spinner`、`ui.Segmented`、`ui.Text`、
   `ui.Image`。用 `ui.NewTester` 写测试。

## 所有权

**只允许新增/修改：**

- `sourcesheet.go`（新文件，主包；大部分 UI 放这里）
- `internal/sourceconf/**`（新包）
- `view.go`（只在 `sourceList` 加书源行、在 `view()` 末尾调用书源对话框）
- `main.go`（只在 `app` 加字段、在 `main()` 初始化书源；不要动阅读逻辑）
- `windows.go`（只在 `desk` 加共享字段、在 `newApp` 传递；不要动窗口逻辑）
- `locales/en.json`、`locales/zh-Hans.json`、`locales/catalog_gen.go`（生成文件）
- `internal/icons/icons.go`（只在需要时加 `Globe`/`Download` 之类图标）
- `internal/appstate/appstate.go`（只在必要时加一个 `ImportFile(path)`；优先不加）

不要改 `go.mod`、`go.sum`、`internal/source/*.go`、`internal/epubwrite/**`、
`internal/acquire/**`、三个书源包、`internal/store/**`、`internal/library/**`。
不要引入新依赖。

## 目标

### 1. `internal/sourceconf`：书源配置持久化

- 文件：`os.UserConfigDir()/quire/sources.json`（可用 `XDG_CONFIG_HOME`），独立于 `state.json`。
- 结构：`map[string]map[string]string`，键是 `source.ID`。
- API：`Open(path string) (*Store, error)`、`Get(id string) map[string]string`、
  `Set(id string, values map[string]string) error`、`Delete(id string) error`、`Save() error`、
  `Path() string`。
- 原子写（临时文件 + rename），目录 0700、文件 0600；损坏文件备份 `.bak` 并从空开始，
  与 `internal/store` 的容错风格一致。并发安全。
- 测试：读写、部分更新、损坏文件、权限（至少断言文件 mode 0600）。

### 2. 主包：书源生命周期

在 `desk` 上持有**全 app 共享**的：

```go
srcConf *sourceconf.Store
sources *source.Registry
acq     *acquire.Manager
```

- 在 `main()` 里、窗口创建前初始化：
  - 配置路径 `filepath.Join(filepath.Dir(statePath), "sources.json")`（statePath 为空时用
    `os.UserConfigDir()/quire/sources.json`）；
  - 下载目录 `filepath.Join(filepath.Dir(srcConf.Path()), "books")`；
  - 创建 `zlib.New(...)`、`legado.New(...)`、`wenku8.New(...)` 并注册。**注意：这些包由其他
    子代理并行实现，当前主仓里还没有。** 你的分支上先不要 import 它们；用构建期可替换的
    `buildSources` 函数（返回 `[]source.Source`），本分支先返回空列表 + 注释，联调时由编排者
    补上三行注册。这样你的分支能独立编译测试。
  - 把 `sourceconf` 中保存的值回填：对每个 `source.Configurable` 调 `Configure`；对
    `source.Importer` 若配置里有 `definitions`，调 `Import`。失败写日志/notice，不阻塞启动。
- 在 `app` 上持有指向 desk 的三个指针（`newApp` 里复制），供 `sourcesheet.go` 使用。
- 下载完成后：`a.pan.ImportDir(dir)`（或新增的 `ImportFile`），再
  `a.desk.refreshShelves(a)`；toast 报告成功/失败。

### 3. `sourcesheet.go`：搜索与下载界面

一个自包含的 `sourceSheet` 状态类型，挂在 `app` 上（例如 `a.find *sourceSheet`），
`view()` 末尾调用 `a.sourceDialog(c, t)`。要求：

- **入口**：`sourceList` 增加 `sourceHeading(locales.SidebarSources())`，下面三行：
  Z-Library、阅读 3.0、轻小说文库（用 registry 里的真实 `Name()`；空 registry 时用固定名字）。
  点击打开 sheet 并默认只看该源。图标可用 `icons.Globe`/`icons.Search`（没有 Globe 就加）。
- **sheet**：`ui.Modal` 居中卡片，宽约 720、高约 560，纸色跟随 `a.chrome.Popover`：
  - 顶部：标题、关闭按钮、搜索框（回车触发）、源筛选（`ui.Segmented` 或可点 chips：
    「全部」+ 三个源）；显示每个源的错误/耗时，一个源失败不影响其他源的结果。
  - 结果列表：封面占位（有 `CoverURL` 也不要在 UI 线程下载图片；先画书名首字/图标）、
    书名、作者、来源名、格式、大小、简介一两行。`ui.List` + `Key` 用 `Source+"\x00"+Key`。
  - 选中行右侧「下载」按钮；双击行也下载。下载中显示 `ui.Progress` / `ui.Spinner` 和
    「正在下载…」；同一时间只允许一个下载，可取消（`context.CancelFunc`）。
  - 搜索/下载全部在 goroutine；结果与进度通过 `win.Update` 回 UI 线程；进度回调节流
    （例如 100ms 一次）。关闭 sheet / 再按搜索时取消旧 ctx，旧结果不得覆盖新结果
    （用递增 id，参考 `appstate.search` 的 `searchID`）。
  - 下载成功：toast、刷新书架、可选「打开」按钮（`a.openPath(saved.Path)`）。
  - 空状态、无结果、未配置、需要登录各有文案。
- **设置**：
  - 每个源一个齿轮：`Configurable` → 根据 `Settings()` 生成 `ui.TextField`，Secret 用密码框
    （若 ui 没有密码框，就用普通 TextField 并说明）；保存时 `Configure` + `sourceconf.Set`。
  - `Importer`（阅读 3.0）→ 「导入书源 JSON…」按钮，`mygo.Dialog.Open` 选 `.json`，
    读文件、`Import`、把原文存进 `sourceconf` 的 `definitions`；显示「已导入 N 个书源」。
    另给「导出」按钮（`Export` + `mygo.Dialog.Save`）可选。
  - Z-Library 的 site/email/password 必须可填；保存后下一次搜索生效。
- 所有用户可见文案走 `locales`：在 `en.json` 和 `zh-Hans.json` 加同名 key（en 是源语言），
  然后运行 `go tool mygo generate` 更新 `catalog_gen.go`。不要手写生成文件。
- 失败用 toast/状态行，不要 panic。

### 4. 测试（离线）

- `internal/sourceconf`：见上。
- 主包：`sourcesheet_test.go`
  - 用假 `source.Source`（实现 Search/Download）注册进 `source.Registry`；
  - 纯逻辑测试：结果行标签、源筛选、错误归并、下载中状态、旧搜索不覆盖新搜索；
  - `ui.NewTester` 渲染 sheet：打开、搜索、显示两本书、点下载、显示进度；
  - 不联网、不写真实用户目录（`t.TempDir()`）。
- 如果测试 app 太重，可把 `sourceSheet` 的纯逻辑抽成不依赖 `*app` 的方法，再测。

## 完成标准

- `gofmt -w sourcesheet.go internal/sourceconf`
- `CGO_ENABLED=0 go test ./...` 通过（你的分支上三个书源包还不存在，`buildSources` 返回空，
  所以能编过；不要为了让它编过而 import 不存在的包）
- `go vet ./...` 通过
- `go tool mygo vet .` 与 `go tool mygo i18n check .` 通过
- 在当前分支只提交你的文件：
  `git add sourcesheet.go internal/sourceconf view.go main.go windows.go locales internal/icons && git commit -m "sources: 书源搜索、下载与配置界面"`
- 不要 push，不要合并，不要改主工作区。

最终回复用中文，列出：新增/修改文件、测试命令与结果、UI 交互说明、配置保存位置、
联调时需要在 `buildSources` 里补的三行注册、任何契约偏差。
