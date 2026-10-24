# 滚动模式为什么卡：实测、结论与分工

本文记录 144 Hz 下滑动阅读"帧率低"的定位过程、结论和后续方案。数字都在本分支上
复现得了（命令见文末），不引用外部记忆。结论只保留当前仍然成立的判断。

> 2026-10-24 核对：核心库回放裁剪、`Layout.TileHeight()`、上游措辞已完成；
> `layx/epub` 已建立；`layx-raster` / `scrollhost`、reader 的 `paint.Clipped`
> 接线（因此核心裁剪在 app 里还未生效）和 tile 位图缓存仍未做。下文相关条目
> 已内联标注状态。

## 一句话结论

- 瓶颈不在 layx 的布局/分页（建一个 tile 只要 0.19–0.26 ms，命中缓存 6 ns），
  而在**宿主画家**：文字被当成矢量轮廓每帧重画。
- 更难察觉的一半在**宿主自己**：多画了约 3 个视口的字，且触控板的每个滚动事件
  都重启一次 140 ms 缓动。
- 已修其一（越界 run 裁剪），帧成本降到原来的 43%，画面逐像素不变。
- 核心库的回放裁剪也已实现（`paint.Clipped`），但宿主 `UIPainter` 还没实现该接口，
  所以 app 里尚未生效；这是下一步成本最低的接线。
- 剩下的 2 ms 只能靠"位图缓存"或"更便宜的逐字形绘制"解决；按核心库的边界，
  这两件事属于宿主，不该进 `render`。
- 浏览器里的分发形态（wasm + Canvas）另见 [`web-wasm.md`](web-wasm.md)。

## 测量环境

`ui.Tester`（headless，软件光栅，scale 1/2），Ryzen 7 7800X3D，样本书
`internal/book/testdata/samples/aq-zhuan.epub`。基准文件：

- `internal/reader/scrollpaint_bench_test.go`：视口绘制（620×700）、裁剪开关、逐像素比对
- `scroll_perf_test.go`：整窗一帧（1180×800，含视图重建）

这些是**同机相对比较**：真机（Mac + GPU）的绝对值不同，但"每帧要做多少次
outline→path→flatten→hash"是平台无关的。

## 数据

| 测量 | 改前 | 改后 |
| --- | ---: | ---: |
| 一帧滚动（整窗重建，1180×800） | 7.31 ms | **3.15 ms** |
| 视口绘制：3 个 tile（620×700, scale 1） | 5.61 ms | **2.06 ms** |
| 同上，scale 2（Retina） | 6.16 ms | **2.27 ms** |
| 同一帧、去掉文字 | 0.0027 ms | 0.0027 ms |
| 建一个 tile（冷） | 0.19–0.26 ms | 同 |
| `Tile(i)` 命中缓存 | 6 ns | 同 |

CPU profile（改前，一帧）：`UIPainter.DrawGlyphs` 累计 **85%**，其中
`ui.Path.flatten` 37%、`drawPathPaint`（对全部点做 maphash 算 mask key）27%、
`Path.LineTo/QuadTo/curveSteps` 20%。**光栅化几乎不占时间**——mygo 的 path mask
图集已经缓存了像素，但几何工作（建 path、flatten、哈希）每帧重做。

144 Hz 的预算是 **6.94 ms/帧**：改前 7.31 ms 直接超支，任何机器都会掉帧；改后
3.15 ms 只是"够用"，一次 GC 或建 tile 的尖峰就会掉一帧。

## 三个根因

1. **多画 3 倍的字。** tile 高 = 一个视口高，宿主的 `drawScroll` 画
   `first-1 … last+2`；而 `paint.Painter.DrawGlyphs` / mygo 的 `pathMask` 都**不按
   裁剪区剔除**，视口外的 run 照样走完整几何流程。实测 620×700 视口一帧被要求画
   2448 个字形，可见的只有约 1/3。
2. **文字走矢量轮廓。** `internal/reader/painter.go` 把每个字形
   `go-text` 轮廓 → `ui.Path` → `FillPath`，每帧重来。mygo 自己的文字（元素文本 /
   `ui.Shape` + `Painter.Glyphs`）走 `sys.Glyph` 的 mask 图集，每字形只是一次查表。
3. **手感：每个滚动事件重启缓动。** `handlePageInput` 收到 `InputScroll` 就
   `scrollBy(ev.DY)`，`scrollTo` 恢复 `140 ms` easeOut。触控板（`ev.Precise`）在
   macOS 上以事件率发像素级增量，于是文字始终比手势慢约 140 ms 并一顿一顿——帧率
   再高也是"卡"的感觉。次要项：`Context.AnimationFrame()`（整棵树重建）而不是
   `Painter.AnimationFrame()`（只重绘），约 1 ms/帧；以及 glide 期间每帧
   `RecordAt` → `SetPosition` + `Timer.Reset`。

## 已做

`internal/reader/painter.go`：`DrawGlyphs` 先用 `u.P.Visible()` 丢掉基线落在裁剪区
外的 run，再开始建 path。

- 效果：整窗一帧 7.31 → 3.15 ms；视口绘制 5.61 → 2.06 ms（约 2.7×）。
- 正确性：`TestDrawGlyphsCullChangesNoPixel` 用开关 `noCull` 渲染两次，断言**逐像素
  相同**；`CGO_ENABLED=0 go test ./...` 125 项全过。
- 这是临时措施：`noCull` 只为测试存在；正确做法是上游回放时自己裁（见下）。

### 核心库回放裁剪（layx/render，已完成）

`render` 侧已经按「边界判定」那节落地（commit `9bb6365`）：

- `paint.Clipped` 可选接口；`Page.Draw` / `Tile.Draw` 用 `replayOp` 跳过可见区外的 op；
- 字形 op 带一个比行盒更宽的行盒矩形（`glyphOpRect`），容纳斜体、悬挂标点等溢出；
- `Layout.TileHeight()` / `TileCount()` / `Tiles()` 也已提供，适配层不用再自己记 tile 高。

**但在 app 里还没生效**：`UIPainter` 没有实现 `paint.Clipped`（`Visible() paint.Rect`），
所以 `render` 的回放裁剪目前是关闭的；现在真正省掉字形几何的仍然是上一节的
`DrawGlyphs` 预裁剪。给 `UIPainter` 补一个 `Visible()` 是下一步成本最低的接线，
之后 `noCull` 那套可以只留给逐像素测试。

## 光栅化是什么，为什么核心库不做它

排版给出的只是**几何**：`"字形 #3412、#2088，基线 (48, 913.5)，字号 17，颜色 #3b3b3b"`、
`"背景矩形 (40, 880, 480, 22)"`。**光栅化**是把这些几何变成设备像素：对每个像素算
覆盖率（抗锯齿）、乘颜色与透明度、混合到画面上。

文字的像素来自两段完全不同的工作：

| 步骤 | 频率 | 成本 |
| --- | --- | --- |
| 轮廓（贝塞尔）→ 覆盖率 mask（17×20 的灰度小图） | 每个 (字体, 字形, 字号, 亚像素位置) **一次** | 高，但可摊到无数帧 |
| mask → 屏幕（一个带纹理的 quad） | 每帧每字形 | 极低（GPU 的本职） |

所以渲染器都把 mask 存进**字形图集**（glyph atlas，几十个小 mask 拼一张大贴图）。
我们链路里的分工：

| 环节 | 谁做 |
| --- | --- |
| 轮廓 → mask，并存进图集 | mygo（`rasterizePath` / `pathMask`） |
| mask → 屏幕像素 | mygo 的后端（GPU 或软件） |
| **每帧把轮廓重建为 `ui.Path`、flatten、哈希** | **`UIPainter`（问题所在）** |

那 2.0 ms 花在第三行：几何工作，不是光栅化。mygo 自己的文字每帧只发 quad。

核心库不做光栅化，是因为这个决策的收益不归排版库：

1. **后端各异**：GPU 要三角形+纹理，软件要扫描线，e-ink 要 1-bit+抖动，wasm 要
   canvas/WebGL，打印/PDF 根本不该光栅化（要矢量）。
2. **参数是设备/产品决定**：1x/2x/3x、sRGB/P3、gamma、亚像素 AA、hinting、多显示器
   不同倍率、旋转。
3. **资源生命周期归窗口所有者**：图集纹理、显存 LRU、设备丢失、内存压力、后台切换。
4. **变化最快**：光栅化是渲染器竞争最激烈的部分，绑进核心等于跟着渲染器版本走——
   和"不跟 CSS 版本走"是同一条理由。
5. **可测试性**：核心靠逐行与 Chromium 比对（`linecmp.sh`）；输出是几何才能这样比。

`paint.Painter` 就是这条边界：**"几何我给你，像素你来。"**

## 画质差异从哪来

"轮廓→像素"不是纯数学，还叠了一整套为小字号可读性做的工程妥协。差的正是这些：

| 手段 | 平台引擎（CoreText / DirectWrite / FreeType+autohint） | 朴素纯 Go 光栅 |
| --- | --- | --- |
| **hinting / 网格拟合** | 按字体指令把横竖笔画对齐像素网格，1px 竖线就是一整列 | 不对齐：笔画落在 0.7px / 1.3px 上，粗细不均、边缘发灰 |
| **stem darkening** | 小字号刻意加粗一档，抵消 AA 变淡 | 没有 → 整页偏细偏淡 |
| **gamma 混合** | 在感知空间混合（macOS 的 font smoothing 也在这层） | 直接 sRGB 相乘 → 浅字压深底尤其"细" |
| **亚像素 RGB AA** | 按红绿蓝三通道算覆盖率 | 只有灰度 |
| **亚像素定位** | 1/4 或 1/8 像素桶 | 已在做（mygo 的 mask key 按 1/4 px） |

要点：

- **Retina（2x）上 hinting 的影响大幅下降**：笔画本身 2–3 个物理像素宽，对不齐不显眼；
  仍能看出来的是 **gamma 与 stem darkening**（整体"偏细偏淡"）。
- 这两项不是玄学，是固定公式 + 经验曲线表（Skia/FreeType 都有对应实现），一次调好
  全站一致。所以是的，**第一版大概率偏淡，但可收敛**——不是运气问题。
- 验证方法：同一页分别用两个后端出图，做**逐像素 diff**（`ui.Render` 已有现成的
  headless 通道），再人眼过一遍正文/标题/小字号页脚三处。2x 下通常只差"淡一点"。
- 若不可接受：保留 backend 接口，先挂系统引擎后端（画质最好，代价是同步 + 主线程）。

## 定位措辞（已落地）

使用者最容易产生的误解是："用了渲染引擎，为什么还要自己写画家？"根因是"渲染内核"
这几个字在两套语境里意思不同：

| 语境 | "渲染引擎"指 | layx |
| --- | --- | --- |
| 浏览器分层 | Blink：样式 → 布局 → 绘制指令 | ✅ 名副其实（样式由宿主提供） |
| 中文口语 | 画出像素 | ❌ 只到绘制指令为止 |

按浏览器分层看，layx 覆盖 shaping / 断行 / 块布局 / 分页 / 滚动切片 / 绘制指令 /
命中选区锚点，**不含 Skia（光栅）与 cc（合成、上屏）**。所以更准确的说法是：

> 排版内核：输入已级联的 `Document`，输出分页结果、显示列表与命中映射。
> 包名 `render` 指"生成绘制指令"，不是"画到屏幕"。

已在 `layx/README.md` 首段把"排版 / 渲染内核"改为"排版内核（产出显示列表）"，并在
`layx/docs/design.md` 边界一节补上"光栅与合成的画质责任随像素一起交给下游"——因为
一旦核心把光栅化划给下游，**画质责任也归下游**，这一点该写在边界旁边，而不是让
使用者从"没有光栅器"这句里自己推断。

## 边界判定：什么能进核心库

核心库的规则写在 `layx/README.md` 与 `layx/docs/design.md`：

- "光栅化留在宿主：GPU / 软件 / e-ink 的选择是产品决策，不是排版决策"；
  "明确不做：光栅器、字体加载、图片解码"。
- "tile 是 DisplayList，不是位图……是否把 tile 缓存成图片由宿主决定"。
- `render/deps_test.go` 禁止核心直接 import `image/*` 等包。

据此：

| 提案 | 判定 |
| --- | --- |
| 核心内置光栅器 / `Layer` 出位图 | **违反**边界与依赖闸门。不做 |
| `Layout.PaintViewport(painter, y, h)` 按带现排现画 | 不建议：绕过 DisplayList 缓存，与"布局预计算、绘制惰性并缓存"冲突，`Stats.Display` 也记不到 |
| 回放（`Page.Draw`/`Tile.Draw`）按画家可见区跳过 op | **可以**：不 shape、不光栅、不加依赖、不改缓存语义、不破坏现有 `Painter` |

回放裁剪的做法：`paint` 包加可选接口
`type Clipped interface{ Visible() Rect }`；构建显示列表时给字形 op 补上行盒矩形
（内部字段，无 API 变化），`Draw` 里对 `op.rect` 与 `Visible()` 求交后跳过。测试
要"带裁剪与不带裁剪逐像素一致"，并保持"所有 tile 的字形总数 = 所有页的字形总数"
这条不变量（该测试用普通画家跑）。

## 浏览器的滚动模型，值得借鉴什么

浏览器顺滑的核心是 **compositor 三件套**，和排版引擎基本无关（Blink 在滚动期间是闲着的）：

1. **瓦片**：页面切成固定大小的瓦片（Chromium 默认 256×256），每块**光栅化成位图**
   放进 GPU 纹理。
2. **光栅线程 + 优先级**：视口内 `NOW`、滚动方向下一屏 `SOON`、其余 `EVENTUALLY`；
   来不及就先上低分辨率版本，停下再补高清。
3. **滚动 = 变换**：合成器整体平移已有位图，只补新露出的边缘；不重绘内容。

输入侧两点同样重要：**触控板/触摸 1:1 直接操纵**（惯性/动量由系统给，App 只消费
增量）；**fling 是物理衰减曲线**按刷新率积分，不是"每个事件重置一次缓动"。

可借鉴项，按性价比：

| 借鉴 | 收益 | 归属 |
| --- | --- | --- |
| 滚动期间不排版（repaint-only 帧） | −1.0 ms | 宿主 |
| 输入 1:1，只有档位/跳转才动画 | 体感（消除拖沓） | 宿主 |
| 滚动 = 平移位图（瓦片模型） | −1.9 ms，到 ≈0.3 ms | 宿主 / 下游 module |
| 瓦片固定尺寸（256–512），与视口解耦 | 内存可控、粒度细、复用率高 | 下游 module |
| 位图向下多光栅一行高 + 从下往上 blit | 跨边界那一行不出缝 | 下游 module |
| blit 偏移吸附 `1/scale` | Retina 不糊 | 下游 module |
| 多分辨率顶替 + 异步光栅 | 不掉帧尖峰 | 下游 module（第二期） |
| 滚动锚定 | 已用 `Location` 代替像素做到 | 无需改 |

## 变通：下游 module（不是核心）

核心库不越界，但"每个宿主都会重写的脏活"可以收成**下游 module**。核心库自己的文档
给了两条先例：

- `render/hyphen/en`：可选、纯 Go、**核心不 import 它**的姊妹包；
- `docs/design.md` 对解析器写明"应作为**独立 module**"。

所以光栅器不是"违反边界"，而是**放在边界之外的另一层**——但要按第二条走：
**独立 module**（如 `github.com/laix/layx-raster`），独立 go.mod，这样
`go get github.com/laix/layx` 永远不会把 `image/*`、`x/image/vector` 带到只要排版的宿主上，
README 里"直接依赖只有 go-text + x/image/math/fixed"这句对 `render` 包仍然成立。

分层：

```text
layx            核心：布局、显示列表、回放裁剪        （不做光栅化）
layx-epub       解析层（已建：layx/epub；TOC/链接/表格转换已落地，reader 尚未接入）
layx-raster     下游：瓦片策略 + 可替换光栅后端        （可选依赖）
宿主 app        主题、存储、UI，只需 位图→ui.Bitmap
```

`layx-raster` 内部还要再分两层，否则它自己就变成"内置一种光栅器"的新偏见：

| 层 | 内容 | 依赖 |
| --- | --- | --- |
| **policy** | 瓦片栅格（固定尺寸/与视口解耦）、可见集、预取优先级、LRU、代际失效、分辨率阶梯、整像素吸附、从下往上 blit、异步队列 | 只依赖 `render`/`paint`/标准库 |
| **backend** | `Raster(t *render.Tile, scale float32) (Bitmap, error)` 的可替换实现：纯 Go 软件（`x/image/vector` + 自建字形图集）／宿主 `ui.Render` 包装／未来的 CoreText、DirectWrite、Skia 包装 | 各自独立，可只引一个 |

要点：

- 性能收益来自 **"一次光栅 + 每帧 blit"**，与选哪个后端无关；图集是软件后端内部的事，
  不对外暴露。
- policy 层用假后端就能做像素级/顺序级测试（和 `ui.Tester` 一个套路）。
- 画质风险要提前接受：平台文字引擎（CoreText/DirectWrite）的 hinting/亚像素 AA 最好，
  纯 Go 软件后端在 macOS 上会略"软"。真机 A/B 若画质不达标，就在 backend 里补一个
  平台后端（cgo 直接调 CoreText，不需要 mygo 配合）。
- 异步光栅（第二期）要求后端能在主线程之外跑；纯 Go 软件后端天然满足，`ui.Render`
  后端不满足（它建整个 view，且用系统文字系统）。

`scrollhost`（适配层的脏活）与 `layx-raster` 可以是同一个 module 的两个包。

| # | 脏活 | 为什么值得收进库 |
| --- | --- | --- |
| A | 由 `scrollY` + 视口算可见 tile、**从下往上**画、单行跨 tile 的归属、视口顶端多画一块 | 画序是正确性陷阱（见 `integration-notes.md`），注释比代码长 |
| B | 跳过裁剪区外的 op | 各宿主写法不同，早跳早省 |
| C | 预取窗口与缓存尺寸（8 个 tile ≈ 320 行 > 默认 `LineCache` 256 行 → 回看重新 shaping） | 猜错只在长书暴露 |
| D | 滚动坐标 ↔ 指针/选区/高亮的换算（`-Margins.Left`、`+scrollY`） | 差一个 margin 就是高亮偏几像素 |
| E | 滚动状态机：`Precise` 1:1、档位缓动、停止条件 | 每个宿主都会做成"卡"的样子 |
| F | tile 位图缓存（LRU、外观代际、整像素吸附） | 144 Hz 才需要，但策略通用 |

骨架（示意，时间由宿主给，符合"无隐藏 goroutine/定时器"）：

```go
func Paint(p paint.Painter, l *render.Layout, y float32, view paint.Rect) int
func Prefetch(l *render.Layout, y float32, view paint.Rect, dir int) (lo, hi int)
func LocationAt(l *render.Layout, y float32, pt paint.Point) (render.Location, bool)
func QuadRect(q render.SelectionQuad, y float32, view paint.Rect) paint.Rect
type Animator struct{ /* Wheel(dy, precise, now) / Step(now) */ }
type Cache[B any] struct{ /* 位图由宿主 raster 回调产出，库只管策略 */ }
```

## 性能归属（重要，别抱错期望）

**把同样 170 行搬进 `scrollhost` 不会快一个毫秒。** 当前 3.15 ms 的构成：

| 部分 | 成本 | 谁能影响 |
| --- | ---: | --- |
| 字形几何（outline→path→flatten→hash） | ≈ 2.0 ms | 宿主：换绘制方式才行 |
| 整棵视图建树 + 布局 + commit | ≈ 1.0 ms | 宿主：改用 repaint 帧即消失 |
| op 派发 + 裁剪跳过（A/B 的地盘） | < 0.05 ms | 适配层可省一半以上 |
| 背景/边框矩形 | 0.003 ms | 谁都能 |

适配层的价值是**正确性只写一次、不变量可测、集成成本降低**，不是帧率。真能再降
成本的只有两件，都落在宿主：

1. glide 帧用 `p.AnimationFrame`（repaint-only）：**−1.0 ms**，代价是滑动过程中
   folio 的百分比不刷新。
2. tile 预光栅成位图（`ui.Render` + `ui.NewBitmap`，整像素吸附）：**−1.9 ms**，
   到 ≈ 0.3 ms/帧，144 Hz 有约 20 倍余量。代价是 Retina 显存
   （620×700×4B ≈ 1.7 MB/tile，2x 约 7 MB）与主题/字号/缩放变化时的作废。

## 待办与决策点

1. ~~核心库：回放裁剪 + 字形 op 矩形~~ 已完成（`render/replay.go`，commit `9bb6365`）；
   剩余：宿主 `UIPainter` 实现 `paint.Clipped`，否则裁剪不会触发。
2. 下游 module `layx-raster`：先做 policy + 纯 Go 软件后端（同步光栅），把
   2.0 ms 的逐字形几何摊成"一帧一次 blit"；异步/多分辨率留第二期。
3. 适配层（可与 2 同一个 module）：A + D + E（这三块是我们 app 里已验证过的逻辑，
   搬过去风险最低）。`Layout.TileHeight()` 已有（`render/scroll.go`），这条前置条件
   已满足；适配层本身仍未开始。
4. 宿主手感三条：`ev.Precise` 直接位移、glide 用 repaint 帧、位置记录去抖
   （现在每秒 144 次 `SetPosition` + `Timer.Reset`）。
5. 未定：`TileHeight` 取视口高还是更小（更小的 tile + 裁剪 = 更贴合的覆盖，但
   tile 数量与显示列表重建次数上升）；以及 `layx-raster` 的瓦片尺寸是否跟核心的
   `TileHeight` 解耦（浏览器的做法是 256–512 固定尺寸、与视口无关）。
6. 未定：纯 Go 软件后端的画质能否接受（macOS 上与 CoreText 对比），不能接受就在
   backend 里补平台后端，不必改 mygo。先补 gamma / stem darkening 再判断。
7. ~~上游建议：README 首段措辞、`docs/design.md` 补画质责任~~ 已完成：
   `layx/README.md` 首段已是"排版内核（产出显示列表）"，`docs/design.md` 边界一节
   已写明画质责任随像素归下游。

## 复现

```sh
cd layx-reader
go test -run XXX -bench 'BenchmarkScrollFrame' -benchtime 3s .
go test -run XXX -bench 'BenchmarkScrollViewportPaint' -benchtime 3s ./internal/reader/
go test -run 'TestDrawGlyphsCullChangesNoPixel' ./internal/reader/

# layx 侧：建 tile 的成本（滚动侧不是瓶颈的证据）
cd ~/layx/render && go test -run XXX -bench 'BenchmarkTileCold640x900|BenchmarkTiles200' -benchtime 2s

# layx 侧：回放裁剪、字形 op 矩形与 TileHeight
cd ~/layx/render && go test -run 'Cull|GlyphOpsHaveFootprints|TileHeight' ./...
```
