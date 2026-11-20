# Quire 的功耗与静息状态：现状、实测与提案

> 这份是草稿，放在 `.TMP/` 里，还没有进 `docs/`。只在 `.TMP/` 里成立的部分是"待裁定"：
> 哪些进 `docs/scroll-performance.md`、哪些进 `docs/integration-notes.md`（上游缺口）、
> 哪些变成 commit，见文末「落地顺序」。
>
> 目标读者是"下一步要动手的人"：每条提案都带**证据（哪段代码 / 哪个数字）**、
> **改法**、**代价**、**验收数字**。历史数字不保留，改了就以新的为准。

## 一句话结论

- **静息态已经是 0 帧、0 次 build**（9 种状态各看 10 秒），比"1Hz 刷新"低三个数量级。
  所以 1Hz 不是目标，是**上限**；而 1Hz 本身应该落在 **surface/backend 层**（e-ink 面板
  刷新），不在 app 层。
- **静息之外真正的浪费只有两处**：指针移动（每次 Move 整窗 build 一次）和
  **嵌入式/软件光栅上"整窗位图每帧全损"没有 1:1 拷贝快路径**。
- 一帧的成本构成按后端分成两类账：**GPU 看 op 数**（740 → 1 值得做），
  **软件光栅看变更像素数**（`~9–10 ns/像素`，所以预光栅反而更贵，除非先补上游快路径）。
- 真正卡嵌入式的是**排版的内存与时间**（7 MB TXT 全量重排 145 ms / 454 MB 分配），
  不是帧率。这一条应该和功耗并列立项，优先级更高。

## 一、静息态：实测

方法：用 `ui.NewTester` 无头构建真窗口，settle 之后把测试时钟推进 10 秒（每步 100 ms），
数"重建了几次视图"以及"结束时是否还要帧"。tester 的引擎与真窗口是同一个
`ui/runtime.go`，`next()` 的判定链路一致。

| 静息状态 | 10 秒内 build | 结束时待帧 |
|---|---:|---|
| 分页正文 | 0 | 否 |
| 封面 | 0 | 否 |
| 目录面板 | 0 | 否 |
| Aa 面板 | 0 | 否 |
| 收起顶栏/页脚的滚动列 | 0 | 否 |
| 页脚带短暂消息 | 0 | 否（**这就是 bug，见 §三.4**） |
| 夜间纸 | 0 | 否 |
| 滚动列 | 0 | 否 |
| 首页（书架 + 圆环 + 目标卡） | 0 | 否 |

这条性质已经钉成回归测试：`rest_test.go`（`TestRestingWindowDrawsNoFrames`）。
以后谁加了一个永不停止的 tick，测试会先红。

## 二、一帧的成本构成（本机实测）

环境：Ryzen 7 7800X3D，Linux/WSL2，`go1.27.2`，headless（MyGo 软件光栅），窗口 1180×800。
**GPU 路径没有测**：`MYGO_FRAME_STATS` / `cmd/glyphprobe` 可以在目标机上补，结论要分开写。

| 测量 | 结果 | 命令 / 出处 |
|---|---:|---|
| 滚动整窗一帧 | 2.50 ms | `BenchmarkScrollFrame` |
| 滚动 glide 的"只重绘"帧 | 2.09 ms（其中 build 10 µs） | `BenchmarkScrollGlideFrame/painting_only` |
| app 自己的显示列表回放（裁掉视口外，739 字形） | 0.10 ms | `BenchmarkScrollViewportAtlas/atlas` |
| 场景构建：740 个 image op / 1 个 op | 67 µs / 3 µs（≈90 ns/op） | 探针 bench（本机临时，见 §七） |
| 页面不动、只改一小块（页脚/悬停） | **81 µs** | 同上；MyGo 的 damage 差生效 |
| 740 个字形全部随页面移动 | 7.9 ms | 同上 |
| 整窗一张位图、每帧全损 | **8.8 ms** | 同上 |
| 离屏渲染一页（`PageRenderer`） | 2.39 ms | `BenchmarkPageRender` |
| 字形冷栅格 | ~3.2 µs/字形 | 图集，文档已有 |
| 指针移动 60 次 | **60 次整窗 build** | 探针 bench（本机临时） |

profile（`-cpuprofile`，滚动只重绘帧）：92% 在 `mygo/internal/raster`
（`sample` 36%、`sampleOp` 53%、`blend` 16%、`(*renderer).image` 88%）。
**app 自己不在瓶颈里**：回放 0.1 ms，build 10–50 µs。

推论（后面所有提案都建立在这两条上）：

1. **软件光栅：成本 ≈ 变更像素数 × 每像素代价**。`internal/raster/raster.go:1433`
   的 `image` 是逐像素 `coverage + bilinear sample + blend`，没有"不透明 + 整数对齐 +
   1:1"的 memcpy 快路径。
2. **GPU：成本 ≈ op 数 + batch 数**。740 个字形 quad 在场景里是 740 个 op、
   每次构建 ~67 µs，外加 batch 切换。

## 三、提案 A：1Hz 的位置与唤醒纪律

### 1. 不要为静息加周期定时器

现状 0Hz。加 1Hz = 每天 86 400 次无谓唤醒，比现状差三个数量级。
**"静息时一帧都不该有"要当成不变量**（`rest_test.go` 就是钉这个）。

### 2. 1Hz 落在 surface/backend 层

e-ink 需要的是"面板按 1Hz 刷新 / 清残影"，那是合成器的事：宿主重呈现最后一帧，或者
直接让面板刷新，**Go 侧不跑代码**。建议做成 MyGo 的 surface 能力：

```go
// ui / platform
type IdleRefresher interface{ IdleRefresh(d time.Duration) } // 0 = 不刷新
```

默认 0（桌面）。桌面端没人需要 1Hz；需要它的只有 e-ink 与"要显示秒级时间的界面"。
如果放在 app 里用 `c.After(1s)` 循环，嵌入式上就是每秒一次 build + paint，只为了画出和
上一帧一样的像素。

### 3. 若真有"静息中也会变"的东西：单一唤醒点

将来可能的来源：阅读时长统计、会话自动保存、"刚读完"提示、e-ink 计时器。
统一成一个 `a.wakeAt`（取所有需求的最早时刻），视图里**只留一个** `c.After(until)`。
三条规矩：

- 能用 `Painter.After`（只重绘、不重建）就不要 `c.After`（重建整棵视图）；
- 窗口被遮挡/最小化时不得反复自我重排（MyGo 的 `holdFrame()` 会吞掉帧，
  但 tick 不能在不可见时不断重新武装）；
- tick 里没有真变化就什么都不画（damage 会跳过像素，但仍应避免 build）。

### 4. 顺手修一个真 bug：页脚消息到点不消失

`a.say()`（`main.go:695`）把消息写进页脚，`transientStatus()`（`main.go:703`）在 2.5 秒后
返回空——但**没有任何人安排"到点"的那一帧**。实测：`say()` 之后推进 5 秒，
消息仍在屏幕上，且 `FramePending()==false`。

- 这是"**缺一次一次性唤醒**"，和 1Hz 正好相反，也正好说明"需要一个最早唤醒时刻"的模型。
- 改法：`say()` 里 `c.After(直到 statusAt+2.5s)`；`chromeShown()` 依赖它也一起更新。
- 代价：每条消息一帧。验收：`say()` 后 5 秒，`HasText(msg)==false`，且中间只有 1 次 build。

## 四、提案 B：按后端分线的帧成本

### 1.（GPU 线）一页/一屏合成成位图，每帧 1–2 个 quad

这就是 `docs/scroll-performance.md` 的待办 #1。今天它的收益**不在 app**（0.1 ms），在：

- 场景构建 740 op → 1 op：省 ~65 µs/帧 + batch 数；
- damage 只提交真正变了的那块（页脚、悬停按钮），GPU 上传/绘制面积塌缩；
- e-ink 侧天然就是"整页位图 → 面板刷新"。

代价：显存/内存（1180×800×4 ≈ 3.8 MB/页，DPR 2 约 15 MB/页）；需要在
页/纸/DPI/字号/选中/高亮变化时作废。缓存"当前跨页 + 相邻跨页"足够。

### 2.（软件/e-ink 线）先补上游 1:1 拷贝快路径，再谈预光栅

**现在直接上预光栅会更慢**：整窗位图每帧全损 8.8 ms > 现在 740 个字形 7.9 ms，
因为 `(*renderer).image` 对每个像素都做 `coverage + bilinear sample + blend`。
一张不透明、整数对齐、1:1 的整窗位图应该是一次行拷贝（3.8 MB ≈ 0.3 ms 量级），
不是 8.8 ms。

- 上游改法（记进 `docs/integration-notes.md` 的"仍然缺的 API"）：
  `internal/raster/raster.go` 的 `image` 增加快路径——`op.Rect` 在设备像素上整数对齐、
  `Src` 与 `Rect` 同尺寸、`Opacity==1`、无圆角、无染色/灰度、`Image` 不透明
  （预乘 alpha 全 255）→ 逐行 `copy`。
- 代价：上游一处分支 + 一组逐像素对拍测试（项目已有这个习惯：
  `TestDrawGlyphsCullChangesNoPixel`）。
- 验收：整窗位图全损帧 < 0.5 ms（软件光栅）；离屏 `PageRender` 与屏幕像素逐字节不变。

**先在目标机确认**：如果嵌入式目标是 GPU（或浏览器 `drawImage`），这条可以跳过，
直接做 §四.1。

### 3. 帧率与动效当策略，不当常数

- 现在翻页动画 240 ms（`sheet.go:17`），120Hz 上要画 ~29 帧；e-ink 一帧都显示不出来。
- 建议一个档位（挂在 `reader.Options` 或窗口上，或用显示能力推导）：
  - `Paged`（桌面默认）：现状，动画跑显示刷新率，静息 0Hz；
  - `Eink/Embedded`：`a.instant` 翻页（已有）、滚动限 10–30Hz 或只在手势结束时更新、
    可关淡入淡出、页脚 `folioEvery`（120 ms）可放宽。
- 同时把平台 **reduce-motion 接到 `a.instant`**：现在只有 `LAYX_NO_ANIM` 环境变量
  （`windows.go:170`），`mygo` 的 `Preferences.ReduceMotion` 没被读。

### 4. 指针移动：当前最大的无谓损耗

实测 60 次 `Move` = 60 次整窗 build（每次 ~49 µs + 整窗重绘），而页面本身没有 hover 行为。
两条路，建议都记下来：

- **上游**：MyGo 在 pointer move 时用上一帧已提交树的命中结果判断"有没有 hover 目标变化"，
  没变就不要帧。这是 `ui/runtime.go` 的事，属于"可选接口"级改动，记进 integration-notes。
- **下游**：整窗拆成"页面层（稳定，Canvas/位图）+ chrome 层（每帧重画）"，
  指针、页脚、面板的帧就碰不到页面。与 §四.1 同一套机制。

### 5. 帧内分配挪出帧

- headless paint-only 帧：13 KB / 80 allocs。
- 真机滚动（`LAYX_PERF`）：**1.1 MB/帧**，来源是新 tile 的 shaping + 字形冷栅格
  （每个新字形约 3.2 µs，覆盖率缓冲区 ~KB 级）。日志里 `GC assist` 已经单独量了这一项。
- 原则：推测性工作（预取 tile、预热字形）**只在已经醒着的时候做**（翻页动画期间、输入后），
  静息时绝不启动新活；`race to idle`。

### 6. 内存就是功耗

- **字形图集**：预算 12 MB，一层 `1024×512×4 = 2 MB` 且**整张上传**（`glyph_atlas.go:52`）。
  嵌入式上建议更小的层（256×512 = 512 KB）或向上游要局部上传（`scene.Image` 的
  damage rect）；DPR/屏幅小的时候预算同步缩小。
- **整本书的图片一次性解码并永久保留**：`main.go:977` 的 `SetImage` 循环 +
  `painter.go` 的 `bitmaps`。漫画/图册 EPUB 会吃掉几百 MB，且解码发生在 UI 线程。
  改成带字节预算的 LRU + 首次绘制时解码（可异步，注意 `SetImage` 目前要求 UI 线程）。
- **`mygo.Power` 一个都没接**：`OnSuspend / OnResume / OnLockScreen / OnUnlockScreen`
  （`mygo/power.go`）。锁屏/休眠时 `store.Flush()` + 释放图集/tile/图片纹理 +
  `debug.FreeOSMemory()`，唤醒时一次 invalidate 重建。几乎白拿的静息收益。

### 7. 写盘合批

- 阅读位置 `SaveSoon(2 * time.Second)`（`internal/appstate/appstate.go:522`），
  但 `writeAtomic`（`internal/store/store.go:1000`）是**两次 fsync**（文件 + 目录）。
  持续阅读 = 每 2 秒一次双 fsync，嵌入式闪存上是真实的能量与磨损。
- 改法：阅读中 15–30 秒合批；失焦 / 隐藏 / suspend / 退出立刻 `Flush()`。
  其它动作（书签、笔记、偏好）现在用 `SaveSoon(0)`，属用户显式动作，保持即刻写入。

## 五、嵌入式真正的门槛：排版，不是帧率

文档里已有的数字：一本 1000 章 / 7 MB 的 TXT，改宽度一次全量重排 145 ms、
**454 MB 分配**（`docs/integration-notes.md`「resize 分两步」）。
这个体量在 256–512 MB 的嵌入式上不是"慢"，是"不可能"。

所以 `integration-notes.md` 第 4 条（增量/流式排版、document 版本号、"当前章先排、
其余向后补进同一份 Layout"）应当和功耗并列立项，且优先级更高。
现在已经有 `LayoutOptions.Range` + `reader.RelayoutRange` 的预览路径（约 1 ms / 5 章），
把它从"拖窗口时的临时预览"升格成"正式排版入口"，是这条工作的落点。

## 六、度量与回归

- **已有**：`LAYX_PERF`（成本/缺口/图集/tile/GC/输入）、`MYGO_FRAME_STATS`（宿主的
  build/layout/paint/present）、`cmd/glyphprobe`。
- **缺**：静息没有任何一条度量。建议 `perf.go` 加一小节：
  - 静息帧数/分钟（期望 0）；
  - 唤醒按来源计数（timer / input / occluded 恢复）；
  - store 写次数与 fsync 次数。
- **回归**：`rest_test.go` 已钉住"静息 0 帧"。其余提案的验收数字见各条。

## 七、复现

```sh
# 静息：9 种状态各 10 秒，必须 0 帧 0 build
go test -count=1 -run TestRestingWindowDrawsNoFrames -v .

# 一帧的成本：整窗 / glide 只重绘
go test -run XXX -bench 'BenchmarkScrollFrame|BenchmarkScrollGlideFrame' -benchtime 2s .

# app 自己的回放与图集
go test -run XXX -bench 'BenchmarkScrollViewport' -benchtime 1s ./internal/reader/
go test -run XXX -bench 'BenchmarkPageRender|BenchmarkGlyphRasterizer' -benchtime 1s ./internal/reader/

# 一帧花在哪：软件光栅 vs app
go test -run XXX -bench 'BenchmarkScrollGlideFrame/painting_only' -benchtime 2s \
        -cpuprofile /tmp/paint.cpu .
go tool pprof -top -nodecount=15 /tmp/paint.cpu

# 真窗口（目标机）：分帧、命中率、帧路径
LAYX_PERF=1 go run . book.epub
MYGO_FRAME_STATS=1 go run ./cmd/glyphprobe
MYGO_GPU=0 go run ./cmd/glyphprobe
```

本文里"探针 bench"的数字（740 op / 整窗位图 / damage / 指针移动）来自临时测试文件，
已删除，没有进仓库；要复现就按 `ui.NewTester` 的写法重建：

- 740 op：`ui.Box(c).Fill().Draw(p → 740 × p.Image(小bitmap, 整像素位置))`；
- 整窗位图：同一位置改成 `p.Image(整窗bitmap, r)`；
- damage：页面 op 不动，只改一块 40×10 的 `p.Fill` 颜色；
- 指针：`tt.Move(x,y)` 60 次，数 build。
  **注意**：位图要写 alpha=255 的不透明像素，半透明像素会被合成到白底上，
  量出来的像素代价会失真。

## 八、落地顺序（每步都能独立回滚）

1. `say()` 的到期唤醒 + 引入单一 `a.wakeAt`。小、立刻见效、为 1Hz 铺路。
2. 接 `mygo.Power`（suspend/lock 释放 + flush）、接 reduce-motion。
3. 图片 LRU + 写盘合批（嵌入式两张门票，改动局部）。
4. 上游补 1:1 blit 快路径 → 再做页面/瓦片合成位图（GPU 线与软件线才都赚）。
5. 指针帧与 e-ink 档位（帧率/动效 profile）。
6. 流式排版（核心工作，不是优化项）。

## 九、待验证

- GPU 路径的一帧 CPU 成本（目标机 `MYGO_FRAME_STATS`）：740 op 换成 1 op 到底省多少，
  值不值得为它付位图内存。
- 嵌入式目标是否有 GPU：决定 §四.1 与 §四.2 谁先做。
- 指针 move 的"无变化不要帧"上游改动，是否会打破 hover/光标/工具提示的语义
  （需要一组 `ui` 侧的测试钉住）。
- 图片 LRU 的命中率：顺序读的一本书里，回看插图的比例决定预算取多少。
