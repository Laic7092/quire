# 滚动模式的绘制链路

滚动阅读现在怎么画、为什么这么画、下一步做什么。

## 一句话

- 帧里只剩 **重放显示列表**（1 ms）或 **贴一张已经画好的位图**；整屏文字的烘焙在
  窗口自己的 goroutine 上（`tileworker.go`）。
- 手机上实测：帧内 `build` 0.1 + `paint` 0.2 + `layout` 0 ms，剩下的 `present` ~8.9 ms
  是在**等屏幕刷新**，不是我们的活。
- 排版不是瓶颈（建一个 tile 0.19–0.26 ms，命中缓存 6 ns），字形只栅格化一次进图集。

## 一帧的路径

```
帧开始
  key := tileKeyNow()       布局/比例/列宽/tile 高/fade/纸色 变了就丢全部瓦片
  syncTiles(key); pumpTiles()   收 worker 交回来的位图
  对每张可见瓦片：
      有 → drawTileBitmap()          贴图，1 个 image 图元
      无 → a.paint.DrawTile()        重放显示列表，~1 ms
           askTile()                 排给 worker，帧不等
帧结束
```

worker 在另一个 goroutine 上：`tileWorker.draw()` 用**一个**复用的 `ui.Tester` 离屏窗口，
把 tile 的显示列表按屏幕比例画进去，`copyOf()` 拷出像素后交给帧。
`jobs` 容量 1（新的替换旧的：滚过去的瓦片不值得画），`done` 容量 4。

## 字形图集（`internal/reader/glyph_atlas.go`）

- key = (面, gid, 字号量化 0.5 px, DPR, 亚像素相位 x/y 各 1/4 px)。DPR 进 key（设备
  像素 = size × DPR）；相位进 key 是因为同一字形在不同相位下覆盖率落在不同像素上。
- 值 = `layx-raster` 的 `Coverage` 栅格化出的 8 位覆盖率，存成**白 + alpha** 的
  `ui.Bitmap`：颜色跟 run 的 `Tint` 走，换纸、改 fade 都不重建图集。
- 层：每层 1024×512，层内 shelf packing，字形裁到墨迹包围盒（拉丁约省 45%，CJK 约 20%）。
  上限 12 MB，按层 LRU。
- 每层每帧最多 `Update` 一次，画完页面后由 `UIPainter.FlushGlyphs()` 提交。
- 冷栅格约 3.2 µs/字形（精确面积积分，0 分配），所以一屏新字的第一帧约 3 ms。
- 回滚/AB：`LAYX_GLYPH_ATLAS=0`。

## 瓦片（`tiles.go` / `tileworker.go`）

- 高 `reader.ScrollTileHeight` = 512 DIP，与视口解耦；宽是整列。3× 下一张 1170×1536
  约 **8.5 MB**，手机上烘焙 **20–34 ms** —— 比一个刷新周期长，所以只能在帧外画。
- 上下各留一条（一行 + 1em）且清成**透明**：行属于"顶边所在的 tile"，但墨迹会越界，
  不透明的纸条会盖住相邻 tile 的首/末行。
- 位图按整设备像素落位；视口外按 24 MB 预算 LRU 丢弃。
- 每帧在滚动方向前方预取一个 tile。
- 回滚/AB：`QUIRE_TILE_BITMAPS=0` 走纯显示列表。

## 墨色修正（`raster.Tone`）

平台文字引擎在小字号上做对比度/gamma 增强与字干生长，走图片路径就拿不到了，所以本
读者自己给：曲线在 `layx-raster` 的 `raster.Tone` 里，**屏幕图集与 `PageRenderer` 共用
同一张表**，逐 run（墨色、字号带、是否合成粗体）缓存。合成粗体只取对比度、不取生长。
曲线钉在 `layx-raster/tone_test.go`。

## 只重绘的帧、整像素与纸面

- 列位置与翻页进度都是**时钟的函数**，手势中的帧不重建视图：draw 回调自己
  `stepColumn()` 然后 `Painter.AnimationFrame()` 要下一帧。落地那一下才
  `Services.Invalidate()` 换一次真 build。回归：`TestGlidePaintsWithoutBuilding` /
  `TestTurnPaintsWithoutBuilding` / `TestRepaintPaintsTheSameColumn`。
- 滚动列与翻页滑带都吸附**整设备像素**（`snapDevices`）。
- 整页纸面按「整列段 + 两个小数边缘像素」写，一页纸面 0.19 ms。

## 光栅器只在 `layx-raster`

图集只负责「一次」，不负责「怎么画一个字形」，那一步只有 `layx-raster` 的 `Coverage`
一套实现（`x/image/vector` 已删）。3.4 µs/字形、整页 1.9–2.0 ms，与 16×16 点采样真值
的误差 0.085/255。

## 两条路：软件 worker 与 GPU canvas

烘焙只有两种放法：**另一个线程上软件光栅**（现在这条），或者**GPU 的 render-to-texture**
（`ui.Canvas`）。手机上起不了环境变量，所以两条都要在，用一个**设置里的开关**比。

### 开关（已经在）

- 设置 → 显示细节 →「瓦片绘制」：`工作线程` / `GPU`。改了立刻丢全部瓦片，
  所有窗口一起换，并写进 state 的 `prefs.tileDraw`。
- HUD 的 tiles 一行末尾是 `cpu` / `gpu`。
- worker 每画完一张瓦片记一行 `a tile was drawn in Xms off the frame`；GPU 那条的
  成本落在 `present` 里（GPU 光栅）和 `paint` 里（提交，桌面实测 38 µs）。

### 两条路的形状

| | 软件（`tileworker.go`） | GPU（`ui.Canvas`） |
|---|---|---|
| 光栅在哪 | 窗口自己的 goroutine，`ui.Tester` 软件光栅 | GPU，present 前先画 |
| 帧里做什么 | 重放显示列表（瓦片没到时）或贴图 | 提交显示列表到 canvas（几十 µs），或贴图 |
| 交付 | 像素拷回（8.5 MB）→ `ui.Bitmap` → 上传 | 不拷、不上传，纹理就在显存里 |
| 何时可用 | 两三帧之后 | **当帧**（canvas 在本帧 present 前画完） |
| 帧里的成本 | 上传 5–20 ms（每张第一次） | 提交 38 µs + GPU 光栅 |

GPU 那条要用**复用的** `ui.Canvas` 池（4 个，按 tile 序号占位），不要每张新建：
`Canvas.Draw` 会先清空再画，纹理不动；新建等于每次分配一张 8.5 MB 纹理。

### 已量到的（Windows，真 GPU，1170×1536，1040 个字形图元）

| | `cv.Draw` 的 CPU 耗时 |
|---|---|
| 复用 Canvas | **38 µs** |
| 每帧新建 Canvas | **107 µs** |

### 没量到的（关键）

老版本（`95370b1`）用的就是 `ui.Canvas`，**每张瓦片新建一个**，手机上实测 **20–34 ms**。
桌面上新建只比复用贵 70 µs，所以那 20–34 ms **不是我们的代码、也不是图元数量**。
三种可能，一条都没验：

1. 每次新建 8.5 MB 纹理（iOS 分配 + 清空）
2. 每张 canvas 强制一次 GPU 同步/等待（「固定 ≈9 ms + 每 Mpx 8.5 ms」的形状更像同步，
   不像带宽）
3. mygo 在 iOS 上没有走 GPU，光栅其实是 CPU

判据就是那个开关：手机上量每张瓦片的耗时。

- **1–3 ms** → 当时的问题是「每次新建 + 每帧太多张」，复用就能解决
- **还是 20 ms** → 是同步/回读，得看 mygo 的 iOS 渲染路径（上游）
- **40 ms 以上** → 这条路不通，回滚

### 和条带的关系

如果 GPU 这条路成立，条带就该是**一张 canvas**（2–3 屏高），滚动就是
`ImageRegion` 改 `src.Y`：GPU 上一分钱都不用再花，连提交都省了。两条路在那里合流。

## 下一步：条带（strip）而不是瓦片

现在的形状是「每 512 DIP 一张瓦片，帧里逐张贴」。帧里的活已经空了，但滚动中每一块
内容都会**换一次画法**：先是重放显示列表，两三帧后被位图替换；瓦片之间还有各自的
snap。丝滑不是帧率，是**滚动中内容一次都不变，只有位移**。

苹果原生就是这么做的（`CALayer.contentsRect`）：一整块已经合成好的层，每帧只改采样
偏移。mygo 有对应的接口：

```go
func (p *Painter) ImageRegion(b *Bitmap, src, dst Rect, o ImageOptions)
// src 是位图里的像素矩形，浮点，可连续移动（文档原话：moving ... glides）
```

所以：**worker 画一条 2–3 屏高的长条带，帧里只画它的一个窗口，src.Y 跟着滚动走。**

| | 现在（瓦片） | 条带 |
|---|---|---|
| 上传 | 每 512 DIP 一次 8.5 MB | 每 2–3 屏一次 |
| 帧里图元 | 4 张瓦片 + HUD | 1 个 ImageRegion |
| 滚动中的内容 | 重放↔位图切换、瓦片边界 snap | 不变，只有 src 位移 |
| 分配/GC | 每张瓦片新建 8.5 MB | 2 块循环复用，滚动中零分配 |

### 量

- 一条 2 屏 ≈ 1700 DIP，3× 下 **1170×5064×4 ≈ 14 MB**；留 2 条 = 28 MB。
- worker 画一条 = 两屏的字 ≈ **50 ms**（现在一张瓦片 20–34 ms）。
- 提前 40% 就向 worker 要下一条。

| 速度 | 一条能撑 | 余量 |
|---|---|---|
| 200 DIP/s（慢读） | 8.5 s | 170× |
| 1000（正常拖） | 1.7 s | 34× |
| 3000（甩） | 570 ms | 11× |
| 10000 | 170 ms | 3.4× |
| 30000（7 屏/秒） | 57 ms | 1.1× ← 只有这里会追不上 |

追不上时退回重放显示列表（1 ms，不卡，但会闪一下字）。

### 要做的事

1. `tiles.go`：`tileCache`（按 512 DIP 索引）换成 `stripCache`（按条带序号索引，记住
   每条带的内容坐标 `top`，2 条循环复用像素）。
2. `tileworker.go`：`draw()` 的尺寸从「一张瓦片」变成「一条带」，像素池化，不再每张
   `image.NewRGBA`。
3. `scroll.go`：合成循环从一个 per-tile 循环变成一个 `ImageRegion`（跨条带时画两个）。
4. 边界：条带边界画在屏幕外再 clip；换主题/调字号/换纸 → 丢全部条带。
5. 验证：`TestScrollFramesCompositeTiles` 改成「一帧只有 1 个 image 图元」；真机上滚
   30 s，看 gap 是否不再出现 16 ms、paint 是否稳定 < 0.5 ms。

## 复现

基准与逐像素比对：

```sh
go test -run XXX -bench 'BenchmarkScrollFrame|BenchmarkScrollGlideFrame' -benchtime 3s .
go test -run XXX -bench 'BenchmarkScrollViewportPaint|BenchmarkScrollViewportAtlas' -benchtime 3s ./internal/reader/
go test -run XXX -bench 'BenchmarkGlyphRasterizer|BenchmarkPageRender' -benchtime 3s ./internal/reader/
go test -run 'TestGlyphAtlas|TestDrawGlyphsCullChangesNoPixel' -v ./internal/reader/
go test -run 'TestGlidePaintsWithoutBuilding|TestTurnPaintsWithoutBuilding|TestRepaintPaintsTheSameColumn' .
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
- `gap` 被刷新率封顶，2 倍预算就是丢了一帧；`budget` 数的是自己花超的帧。
- 超过 `LAYX_PERF_SLOW`（默认 8 ms）的帧单独一行，带每个 tile 的耗时。
- 开书、排版、改字号、换纸、翻页各有一行事件，慢的那一帧能对上是哪个动作。
