# 滚动模式为什么卡，以及现在的绘制链路

144 Hz 下滑动阅读掉帧的定位过程。**只保留当前仍然成立的判断和可复现的命令**；
每一轮的测量数字、改前改后、推翻过的结论都随 commit 走，不再留在这里。

## 一句话结论

- 瓶颈从来不在 layx 的排版（建一个 tile 0.19–0.26 ms，命中缓存 6 ns），而在**宿主画家**
  和**宿主自己多干/重复干的活**：文字被当成矢量轮廓每帧重画、视口外约 2/3 的字照样
  走完整几何流程、触控板每个滚动事件都重启一次缓动、每帧重建整棵视图。
- 这些都已修：字形**逐字形只栅格化一次**进图集，之后每帧每字形只是一次查表 + 一次贴图
  （draw 回调内 4.4 ms → 0.21 ms，帧内命中率 98.9%）；回放按可见区裁 op；手势期间
  只重绘不重建视图；滚动输入 1:1。
- 画质没有让步：图集与轮廓两条路逐像素等价（总墨量差 0.002%，差值全落在边缘像素的
  覆盖率分配上），而且屏幕与离屏 PNG 现在是同一套光栅器 + 同一张墨色表（PNG/窗口
  墨量 1.01×）。
- 再往下要降只能把 tile 预光栅成位图（见「待办」），那是策略层的活，不是排版层的。

## 三个根因（为什么是图集）

1. **多画 3 倍的字。** tile 高 = 一个视口高，`drawScroll` 画 `first-1 … last+2`，而
   `paint.Painter.DrawGlyphs` 与 mygo 的 `pathMask` 都不按裁剪区剔除，视口外的 run
   照样走完整几何流程：620×700 的视口一帧被要求画 2448 个字形，可见的只有约 1/3。
2. **文字走矢量轮廓。** 每个字形「go-text 轮廓 → `ui.Path` → flatten → 哈希 mask key」
   每帧重来。profile 里 `DrawGlyphs` 占 85%（`ui.Path.flatten` 37%、
   `drawPathPaint` 27%、`LineTo/QuadTo/curveSteps` 20%），**光栅化几乎不占时间**——
   mygo 的 mask 图集已经缓存了像素，浪费的是几何工作。mygo 自己的文字每帧只发 quad。
3. **手感。** `InputScroll` 一到就 `scrollBy()` 并恢复 140 ms easeOut，触控板以事件率
   发像素级增量，于是文字永远比手势慢 140 ms 并一顿一顿——帧率再高也是「卡」。
   次要项是每帧 `Context.AnimationFrame()`（整棵树重建，约 1 ms）而不是
   `Painter.AnimationFrame()`（只重绘）。

## 现在的链路

### 字形图集（`internal/reader/glyph_atlas.go`）

- key = (面, gid, 字号量化到 0.5 px, DPR, 亚像素相位 x/y 各量化到 1/4 px)。DPR 必须进
  key（设备像素 = size × DPR）；相位进 key 是因为同一字形在不同相位下覆盖率落在不同
  像素上——这正是旧路径每帧重做 mask 的原因。
- 值 = `layx-raster` 的 `Coverage` 栅格化出的 8 位覆盖率，存成**白色 + alpha** 的
  `ui.Bitmap`：颜色跟 run 的 `Tint` 走，所以换纸、改 fade 都不重建图集。
- 存进**层**：每层 1024×512，层内 shelf packing，每个字形裁到墨迹包围盒（拉丁字省
  约 45% 面积，CJK 约 20%）。上限 12 MB，按层 LRU；换 DPR/主题都不重建，只是旧层被换掉。
- 每层每帧最多 `Update` 一次，由宿主画完页面后调用 `UIPainter.FlushGlyphs()`：引擎按
  `scene.Image` 的 version 上传纹理，晚一帧写就晚一帧显示，`drawSheet` 用 `defer` 保证。
- 默认全平台都开。曾经按 `Painter.Accelerated()` 开关（软件光栅上贴图比 mask 混合贵），
  那条测量在「槽位裁到包围盒 + quad 落在整设备像素」之后失效了；
  `LAYX_GLYPH_ATLAS=0` 现在只是 A/B 与回滚。

最终数字（Ryzen 7 7800X3D，样本书；headless = `ui.Tester` 软件光栅）：

| 测量 | 逐字形轮廓 | 图集 |
|---|---:|---:|
| headless 一帧 620×700 的 draw 回调（scale 1） | 2.75 ms | **0.105 ms** |
| 同上 scale 2 | 3.00 ms | **0.117 ms** |
| 整窗一帧 1180×800（`BenchmarkScrollFrame`） | 4.72 ms | **2.58 ms** |
| 真窗口滑动，draw 回调内 | 4.39 ms | **0.21 ms** |
| 真窗口滑动，整帧 wall | 90.8 ms | **29.0 ms** |
| 每帧 flatten | 95 | **0** |
| 图集命中率（滑动 6.3 px/帧） | — | **98.9%** |

冷栅格约 3.2–3.3 µs/字形（精确面积积分，0 分配；`x/image/vector` 是 1.6 µs 但每字形
31 次分配），所以新页/新 tile 的第一帧冷栅格约 3.2 ms——这笔落在翻页第一帧，不是每帧。

### 墨色修正（`raster.Tone`）

平台文字引擎在小字号上做对比度/gamma 增强与字干生长，走图片路径就拿不到了，所以本读者
自己给：曲线在 `layx-raster` 的 `raster.Tone` 里，**屏幕图集与 `PageRenderer` 共用同一张
表**，逐 run（墨色、字号带、是否合成粗体）缓存。合成粗体只取对比度、不取生长——它已经把
轮廓撑粗一圈，再叠生长会把字怀堵死。当前 PNG 墨量 / 窗口墨量 = 1.01×，开修正 / 关修正
= 1.14×。曲线本身钉在 `layx-raster/tone_test.go`。

### 只重绘的帧、整像素与纸面

- 列位置与翻页进度都是**时钟的函数**，所以手势中的帧不重建视图：draw 回调自己
  `stepColumn()` 然后 `Painter.AnimationFrame()` 要下一帧。落地那一下（和每 120 ms 的
  页脚更新）才 `Services.Invalidate()` 换一次真 build，把位置写进 store。
  `perf.go` 的 `build` 因此从每帧一次变成每格一次。回归：
  `TestGlidePaintsWithoutBuilding` / `TestTurnPaintsWithoutBuilding` /
  `TestRepaintPaintsTheSameColumn`。
- 滚动列与翻页滑带都吸附**整设备像素**（`snapDevices`）。不吸附时每帧跨页的每个字形
  都会换一张相近的位图：一次翻页在 DPR 2 下栅格化 3797 → **545** 个字形，图集项
  2948 → 1639（`TestTurnKeepsOneGlyphPhase`）。
- 整页纸面是每页最大的单笔成本：一行拆成「整列段 + 两个小数边缘像素」，不透明时按
  64 字节 `copy` 写。一页纸面 5 ms → **0.19 ms**，同一页整页 8.93 ms → **1.9–2.0 ms**，
  输出逐字节不变。

### 光栅器只在 `layx-raster`

图集只负责「一次」，不负责「怎么画一个字形」，那一步只有 `layx-raster` 的 `Coverage`
一套实现（`x/image/vector` 已删）。同一页、同一批字形与 FreeType 的对照（它不参与构建，
只当参照）：我们 3.4 µs/字形、整页 1.9–2.0 ms、与 16×16 点采样真值的误差 0.085/255；
FreeType 5.0 µs、3.3–3.6 ms、0.122/255。差别来自精确面积积分比子采样更接近真值，
不是「调得更像 FreeType」。

## 什么能进核心库

`paint.Painter` 就是那条边界：**几何我给你，像素你来。** 光栅化留给下游，是因为它的
收益不归排版库——后端各异（GPU/软件/e-ink/wasm/PDF）、参数是设备与产品决定（DPR、
色彩空间、gamma、亚像素 AA、hinting）、资源生命周期归窗口所有者（图集纹理、显存 LRU、
设备丢失）、变化最快、而且输出几何才能逐行对拍。

据此的判定：

| 提案 | 判定 |
|---|---|
| 核心内置光栅器 / `Layout` 直接出位图 | **不做**，违反边界与依赖闸门（`render/deps_test.go` 禁 `image/*`） |
| `Layout.PaintViewport(painter, y, h)` 按带现排现画 | 不建议：绕过 DisplayList 缓存，与「布局预算、绘制惰性」冲突 |
| 回放时按画家可见区跳过 op（`paint.Clipped`） | **可以**：不 shape、不光栅、不加依赖、不改缓存语义 |

顺带一条结论：layx 是**排版内核**（输入级联好的 `Document`，输出分页结果、显示列表与
命中映射），不含光栅与合成。所以「用了渲染引擎为什么还要自己写画家」是个措辞误会；
上游 README 与 `docs/design.md` 已按这个说法改过。

## 下游 module 的分工

`layx-raster` 分两层，否则它自己就变成「内置一种光栅器」的新偏见：

| 层 | 内容 | 依赖 |
|---|---|---|
| policy | tile 光栅策略、可见集、预取优先级、LRU、整像素吸附、从下往上 blit、异步队列 | 只依赖 `render`/`paint` |
| backend | `Raster(tile, scale) (Bitmap, error)` 的可替换实现：纯 Go（现在这版）/ `ui.Render` 包装 / 未来 CoreText、DirectWrite、Skia | 各自独立 |

性能收益来自「一次光栅 + 每帧 blit」，与选哪个后端无关。适配层的价值是**正确性只写
一次**（画序、跨 tile 的单行、滚动坐标 ↔ 指针换算、`Precise` 1:1 的状态机），不是帧率。
所以别指望把 170 行搬进库里会快一毫秒。

## 待办

1. tile 预光栅成位图（`ui.Render` + `ui.NewBitmap`，整像素吸附）：每帧 3 个 quad 而不是
   900 个。代价是显存（620×700×4B ≈ 1.7 MB/tile，2× 约 7 MB）、主题/字号/缩放变化时
   作废，以及失去逐帧亚像素定位。
2. 未定：`TileHeight` 取视口高还是更小（更小 + 裁剪 = 更贴合，但 tile 数与显示列表重建
   次数上升）；瓦片尺寸是否与核心的 `TileHeight` 解耦（浏览器是 256–512 固定、与视口无关）。
3. 未定：纯 Go 后端在 macOS 上与 CoreText 的画质差能否接受；不能就补平台后端，不必改 mygo。
4. 适配层（policy 的 A/D/E 三块，我们 app 里已验证过的逻辑）搬进 `layx-raster`，仍未开始。

## 复现

基准与逐像素比对：

```sh
go test -run XXX -bench 'BenchmarkScrollFrame|BenchmarkScrollGlideFrame' -benchtime 3s .
go test -run XXX -bench 'BenchmarkScrollViewportPaint|BenchmarkScrollViewportAtlas' -benchtime 3s ./internal/reader/
go test -run XXX -bench 'BenchmarkGlyphRasterizer|BenchmarkPageRender' -benchtime 3s ./internal/reader/
go test -run 'TestGlyphAtlas|TestDrawGlyphsCullChangesNoPixel' -v ./internal/reader/
go test -run 'TestGlidePaintsWithoutBuilding|TestTurnPaintsWithoutBuilding|TestRepaintPaintsTheSameColumn' .

# A/B：强制走旧路径（=1 反过来）
LAYX_GLYPH_ATLAS=0 go test -run XXX -bench 'BenchmarkScrollFrame' -benchtime 3s .
```

真窗口（`cmd/glyphprobe` 滑这本书，报 cpu/frame、命中率与内存，跑完自己退出）。
显示器刷新率会封顶 wall，所以同机比 **cpu/frame**：

```sh
go run ./cmd/glyphprobe                    # 图集
LAYX_GLYPH_ATLAS=0 go run ./cmd/glyphprobe # 每帧重建轮廓
MYGO_GPU=0 go run ./cmd/glyphprobe         # mygo 自带软件光栅
MYGO_FRAME_STATS=1 go run ./cmd/glyphprobe # mygo 自己的 build/paint/present 分帧
```

实机日志（`perf.go`，给人读的；想知道一台真机器上慢在哪就跑这个）：

```sh
LAYX_PERF=1 go run . book.epub            # 默认写到与 state.json 同目录的 perf-*.log
LAYX_CPU_PROFILE=/tmp/layx.cpu LAYX_PERF=1 go run . book.epub
```

```
[17:42:05] 118 frames in 2.0 s (59 fps, scrolling)
    cost    build    1.0/   2.1/   4.1 ms   paint    0.2/   0.6/   1.2 ms   (p50/p95/max)
    budget  16.7 ms a frame (measured): 11 frames (9%) took longer
    gaps    16.7/16.7/33.3 ms   (p50/p95/max; a gap of two budgets is a lost frame)
    glyphs  1.0 uploads a frame, 8 rasterized, 98.7% came from the atlas
    tiles   3.2 a frame
    memory  1.1 MB allocated a frame, 0 GC cycles, 0.0 ms of GC assist
    input   34 scroll events (612 px, 30 of them smooth), 6 glide restarts
```

- `cost` 是 app 自己的活（`build` 每帧重建视图、`paint` 是它的 draw 回调）；MyGo 的
  layout/commit/present 不在里面，要那几段就同时开 `MYGO_FRAME_STATS=<ms>`，它的行会进
  同一个文件、同一条时间轴。
- `gap` 被刷新率封顶，2 倍预算就是丢了一帧；`budget` 数的是自己花超的帧，那是 app 的问题。
- 超过 `LAYX_PERF_SLOW`（默认 8 ms）的帧单独一行，带每个 tile 的耗时。
- 开书、排版、改字号、换纸、翻页各有一行事件，慢的那一帧能对上是哪个动作。
