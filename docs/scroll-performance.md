# 滚动模式为什么卡：实测、结论与分工

本文记录 144 Hz 下滑动阅读"帧率低"的定位过程、结论和后续方案。数字都在本分支上
复现得了（命令见文末），不引用外部记忆。结论只保留当前仍然成立的判断。

> 2026-10-24 核对：核心库回放裁剪、`Layout.TileHeight()`、上游措辞已完成；
> `layx/epub` 已建立；`layx-raster` / `scrollhost`、reader 的 `paint.Clipped`
> 接线（因此核心裁剪在 app 里还未生效）和 tile 位图缓存仍未做。下文相关条目
> 已内联标注状态。
>
> 2026-10-25：**宿主的字形图集已落地**（`internal/reader/glyph_atlas.go`），
> 逐字形的 outline→path→flatten→hash 每帧工作消失，见「已做：字形图集」一节。
> 下面「数据」表里 3.15 / 2.06 ms 是**上一步（裁剪）之后、图集之前**的数，保留作历史。
>
> 2026-10-26：两件未完的事做完了，并且**推翻了一条旧结论**：
> 1. `UIPainter` 实现了 `paint.Clipped`（`Visible()`），核心库的回放裁剪在 app 里生效；
> 2. 图集**不再按渲染器开关**：裁到包围盒 + 整像素贴图的槽位把「软件光栅上图集更贵」
>    这条旧测量作废了。现在软件光栅上，一个视口的 draw 回调 2.75 ms → **0.105 ms**，
>    1180×800 整帧 5.2 → **2.2 ms**（见「图集的默认开关」）；
> 3. 滚动与翻页的帧**只重绘、不重建视图**（`Painter.AnimationFrame`），视图每个手势
>    只建一次（见「已做：只重绘的帧」）。

## 一句话结论

- 瓶颈不在 layx 的布局/分页（建一个 tile 只要 0.19–0.26 ms，命中缓存 6 ns），
  而在**宿主画家**：文字被当成矢量轮廓每帧重画。
- 更难察觉的一半在**宿主自己**：多画了约 3 个视口的字，且触控板的每个滚动事件
  都重启一次 140 ms 缓动。
- 已修其一（越界 run 裁剪），帧成本降到原来的 43%，画面逐像素不变。
- 已修其二（**字形图集**）：轮廓只栅格化一次，之后每帧每字形只是一次查表 +
  一次贴图。draw 回调内的成本 **4.4 ms → 0.21 ms**（真窗口）/ **2.2 ms → 0.09 ms**
  （headless），滑动时命中率 98.9%，画质逐像素等价（总墨量差 0.002%）。
- 核心库的回放裁剪也已实现（`paint.Clipped`），但宿主 `UIPainter` 还没实现该接口，
  所以 app 里尚未生效；这条现在只是"再省一点 op 派发"，不再是主要矛盾。
- 剩下的成本落到真正的光栅与合成上（GPU 上是填充率，软件光栅上是它的采样循环），
  以及整棵视图重建的 ~1 ms；要再降就是 tile 位图缓存那条路。
- 浏览器里的分发形态（wasm + Canvas）另见 [`web-wasm.md`](web-wasm.md)。

## 测量环境

`ui.Tester`（headless，软件光栅，scale 1/2），Ryzen 7 7800X3D，样本书
`internal/book/testdata/samples/aq-zhuan.epub`。基准文件：

- `internal/reader/scrollpaint_bench_test.go`：视口绘制（620×700）、裁剪开关、逐像素比对
- `internal/reader/glyph_atlas_test.go`：图集的逐像素比对、稳定性、计数器、draw 回调内成本
- `scroll_perf_test.go`：整窗一帧（1180×800，含视图重建）
- `cmd/glyphprobe`：**真窗口**滑这本书，报 wall/cpu 每帧、命中率与内存

这些是**同机相对比较**：真机（Mac + GPU）的绝对值不同，但"每帧要做多少次
outline→path→flatten→hash"、"每帧要有多少个 quad"是平台无关的。

两个坑，量之前先看：

- **显示器刷新率会封顶 wall 时间。** 开发机是 WSL + 远程桌面（`rdp-0`，
  3840×2160 @ 59.98 Hz），所以那里**量不出 144 Hz**，匀速帧一律 ~16.6 ms 或它的
  整数倍。这种环境下要比的是 **cpu/frame**（`cmd/glyphprobe` 的 `cpu/frame`，
  或 `MYGO_FRAME_STATS=1` 的 `build+paint+present`），不是 fps。
- **`MYGO_GPU=0` 与 headless 都是 mygo 自带的软件光栅**，它把画图片当双线性采样，
  比 mask 混合贵 4–5 倍。所以在它们上面，图集反而更贵；图集的默认开关就按这一点
  跟随 `Painter.Accelerated()`（见「已做：字形图集」）。

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
所以 `render` 的回放裁剪目前是关闭的；省掉字形几何的是下面的字形图集，以及
`DrawGlyphs` 里的预裁剪。给 `UIPainter` 补一个 `Visible()` 仍然值得做（省 op 派发、
让 `noCull` 那套只留给逐像素测试），但它已经不是主要矛盾。

### 字形图集（已做，宿主侧）

`internal/reader/glyph_atlas.go`：把「轮廓 → 覆盖率位图」从"每帧每字形一次"改成
"每个 (面, gid, 字号, DPR, 亚像素相位) 一次"，之后每帧每字形只剩一次查表和一次
`p.ImageRegion`。

- 轮廓仍来自 `glyphFace.outlines`（go-text `GlyphDataOutline` 的缓存，没动）；
- 用 [`layx-raster`](https://github.com/laix/layx-raster) 的 `Coverage` 把轮廓栅格化
  成 8 位覆盖率（和离屏出图同一个光栅器），存成**白色 + alpha 的 `ui.Bitmap`**
  （premultiplied white）：颜色跟着 run 的 `Tint` 走，所以换主题、改
  fade 都不重建图集；
- key = `(面, gid, 字号量化到 0.5 px, DPR, 亚像素相位 x/y 各量化到 1/4 px)`。DPR 必须
  进 key（设备像素 = size × DPR）；相位进 key 是因为同一字形在不同相位下覆盖率落在
  不同像素上，这也正是旧路径每帧重做 mask 的原因（滑动时相位一直在变）；
- 存进**层**：每层一张 1024×512 的 `ui.Bitmap`（2 MB RGBA），层内 shelf packing；每个
  字形按实际覆盖率**裁到包围盒**（拉丁字省约 45% 面积，CJK 约 20%），
  所以相邻字形之间没有空白像素要采样；
- 上限 12 MB，按**层**做 LRU 淘汰；换 DPR、换主题都不重建，只是旧层慢慢被换掉；
- 每层每帧最多 `Update` 一次，由宿主在画完页面后调用一次
  `UIPainter.FlushGlyphs()`：引擎按 `scene.Image` 的 version 上传纹理，写在那一帧之后
  就会晚一帧才显示——`sheet.go` 的 `drawSheet` 用 `defer` 保证这一点。

数字（同一台机，Ryzen 7 7800X3D；headless = `ui.Tester` 的软件光栅，真窗口 = 787×700，
`cmd/glyphprobe` 跑 6.3 px/帧 的滑动）：

| 测量 | 逐字形轮廓 | 图集 |
| --- | ---: | ---: |
| headless 一帧 620×700，draw 回调内（scale 1） | 2.20 ms | **0.091 ms**（24×） |
| 同上 scale 2 | 2.53 ms | **0.093 ms**（27×） |
| `BenchmarkScrollViewportPaint`（裁后，scale 1） | 1.81 ms | **0.103 ms**（18×） |
| 真窗口滑动，draw 回调内 | 4.39 ms | **0.21 ms**（21×） |
| 真窗口滑动，整帧 wall | 90.8 ms | **29.0 ms**（3.1×） |
| `BenchmarkScrollFrame`（整窗重建，1180×800） | 2.82 ms | **2.40 ms** |
| 每帧 run / 字形（裁后） | 95 / 876 | 同 |
| 每帧 flatten | 95 | **0** |
| 图集命中率（滑动，6.3 px/帧） | — | **98.9%** |
| 图集项 / 内存（DPR 1 静止 / 滑动） | — | 580 / 2 MB；1947 / 2 MB |
| 图集项 / 内存（DPR 2 滑动） | — | 3819 / 10.5 MB（预算内，0 次淘汰） |

画质（`TestGlyphAtlasMatchesOutlines`：同一页两条路各渲一次，逐像素比）：

| scale | fade | 最大差(/255) | 平均差 | 差 >32 的像素 | 总墨量比 |
| --- | --- | ---: | ---: | ---: | ---: |
| 1 | 1 | 30 | 1.10 | 0.000% | 0.99998 |
| 1 | 0.5 | 33 | 1.20 | 0.000% | — |
| 2 | 1 | 36 | 0.61 | 0.000% | 1.00002 |
| 2 | 0.5 | 39 | 0.72 | 0.000% | — |

差值全部落在边缘像素的**覆盖率分配**上（图集的光栅器与 mygo 的 mask 光栅器是两套
算法），总墨量差 0.002%：没有变细、变粗或位移。顺带一提，旧路径把**每个轮廓点**吸附到
1/4 px 网格（形状被微扰），图集只吸附整个字形的相位，形状本身是精确的——所以这里是
"等价而非变差"，不需要换 `srwiley/rasterx`。

### 图集的默认开关（2026-10-26：改成全平台默认）

`(*UIPainter).atlasWanted` 曾经跟着 `Painter.Accelerated()` 走：GPU 上用图集，
mygo 回退到**自带软件光栅**时用轮廓。理由当时是实测过的——那个渲染器把图片当双线性
采样（每像素 4 次取 texel + 12 次插值），比 mask 的一字节混合贵，同一窗口 CPU/帧
7.8 ms 涨到 13.2 ms。

那条测量现在**不成立**，变的是图集这边的两个实现细节，不是渲染器：

1. 槽位**裁到墨迹的包围盒**（拉丁字省约 45% 面积，CJK 约 20%），所以贴图覆盖的像素
   少了一大截；
2. 槽位的 quad 落在**整设备像素**上（`dst` 的宽高就是位图宽高除以 DPR），双线性
   采样在 texel 中心上就是取那一个 texel，不再有半像素的插值。

重测（Ryzen 7 7800X3D，`ui.Tester`，`benchScrollColumn` 的样本书）：

| 测量 | 轮廓 | 图集 |
| --- | ---: | ---: |
| 视口 draw 回调 620×700，scale 1 | 2.75 ms | **0.105 ms**（26×） |
| 同上 scale 2 | 3.00 ms | **0.117 ms**（26×） |
| 整窗一帧 1180×800（`BenchmarkScrollFrame`） | 4.72 ms | **2.58 ms** |
| 滑动中的一帧（`BenchmarkScrollGlideFrame`） | 5.2 ms | **2.05 ms** |

所以默认改成**所有渲染路径都用图集**，`LAYX_GLYPH_ATLAS=0` 变成纯粹的 A/B 与回滚
（它同时是 `TestGlyphAtlasMatchesOutlines` 里的对照）。`LAYX_GLYPH_STATS=1` 打开计数器
（`UIPainter.GlyphStats()`）。

已知差异：Windows 的 DirectWrite 后端会给 mask 覆盖率做对比度/gamma 增强
（`scene.TextCoverage`，由 `Scene.Text` 参数驱动），走到图片路径就没有这层增强。
所以本读者自己给：曲线在光栅器里（`raster.Tone`：DirectWrite 的对比度曲线 +
CoreText 式的边缘生长），图集和离屏出图都用它，`texttone.go` 只留策略（哪些墨色、
哪个字号带宽、是否合成粗体）与缓存。合成粗体（`EmulateBold`）**只取对比度、不取
生长**——它已经把轮廓撑粗了一圈，再叠生长会把粗体正文的字怀堵死。
本机（软件光栅、这本正文全是 `<h5>` 的样书）实测：整页墨量 轮廓 : 图集 = 1 : 1.04。

### 图集内部的光栅器：只留 layx-raster 的 Coverage（2026-10-26）

图集只负责"一次"，不负责"怎么画一个字形"。那一步现在只有一套实现：
[`github.com/laix/layx-raster`](https://github.com/laix/layx-raster) 的 `Coverage`。
`golang.org/x/image/vector` 已经删掉——它当初是图集的起点，留着两条路就要维护两份
A/B、两个环境变量，而画质和速度都不再需要它。

第一版 `Coverage` 是 8 条竖向子扫描线 + 水平解析面积，冷栅格要 **18 µs/字形**
（vector 约 1.6 µs），那是自找的：超采样把一个像素写了 8 次，还要排 8 次序。改成
**精确面积积分**后是 **3.2–3.3 µs/字形**：每条边按它穿过的每个像素行落一块带符号面积，
行内从左到右累加出环绕数，取绝对值夹到 1 就是覆盖率；水平方向解析、竖直方向由行
重叠自然得到，一个像素只写一次。`BenchmarkCoverageGlyph`（`layx-raster`）盯住这条
路径的开销，`BenchmarkGlyphRasterizer`（这里）盯住真实字形。

冷栅格（17 DIP、DPR 2，一批 CJK/拉丁字形，每字形一次，不含图集查找）：

| 光栅器 | 每字形 | 分配 |
| --- | ---: | ---: |
| `x/image/vector`（已删除） | 1.6 µs | 31 次/字形，2.0 KB |
| `Coverage`，8 条子扫描线（第一版） | 18.0 µs | 0 次 |
| `Coverage`，精确面积（现在） | **3.2–3.3 µs** | 0 次 |

一个 620×700 的视口在 DPR 2 下约有 1000 个图集槽位，所以新页/新 tile 的第一帧冷
栅格约 **3.2 ms**（第一版是 19 ms，vector 是 1.6 ms）。图集命中后的帧完全不变
（`TestGlyphAtlasScrolling` 里滑动中的 `rasters≈4/frame`）：这是一笔落在**翻页/新
tile 第一帧**上的成本，不是每帧成本。

画质：两条路都还在的时候逐像素比过（`benchScrollColumn` 的样本书、同一条 outline、
同一条 tone 处理），差全在边缘像素的覆盖率分配上，没有变细、变粗或位移：

| 版本 | scale | 最大差(/255) | 平均差 | 差 >32 的像素 |
| --- | --- | ---: | ---: | ---: |
| 8 条子扫描线 | 1 | 18 | 0.280 | 0.000% |
| 8 条子扫描线 | 2 | 12 | 0.148 | 0.000% |
| 精确面积 | 1 | 12 | **0.108** | 0.000% |
| 精确面积 | 2 | 12 | **0.069** | 0.000% |

同一页用两种积分器出图（`bookshot`，阿Q正傳第 2 页、DPR 2、整页）平均差 0.11/255、
差 >32 的像素 0.08%——精确面积是更接近真值的那一边。离屏出图也因此变快：
`BenchmarkPageRender` 同一页从 11.4–12.2 ms 降到 **8.4 ms**（8.2–8.8 之间），分配 282 KB/2230 次 →
40 B/4 次。

复现：

```sh
go test -run XXX -bench 'BenchmarkGlyphRasterizer|BenchmarkPageRender' -benchtime 3s ./internal/reader/
cd ../layx-raster && go test -run XXX -bench 'BenchmarkCoverageGlyph|BenchmarkDrawGlyphs' -benchtime 3s .
go test -run TestCoverageMatchesSupersampling -v .
```

### 墨色修正进了光栅器：屏幕与 PNG 同重（2026-10-26）

上面那层修正原先只烘在**屏幕的图集**里，`PageRenderer` 出的 PNG 走的是
`DrawGlyphs`，所以同一页在窗口里和在 PNG 里不是一个重量：实测 PNG 比屏幕**轻 12%**，
导出件看起来比读的时候细一号。

现在曲线只写一份，在 `layx-raster` 的 `raster.Tone`：

- `Rasterizer.ToneFor` 让宿主按 run（墨色、字号、是否合成粗体）给表，`DrawGlyphs`
  逐 run 应用；`layx-reader` 的 `PageRenderer` 装上它，就得到了窗口里的字重；
- `Coverage.SetTone` 让宿主自己的图集在同一像素循环里应用同一张表，省掉一遍后处理；
- 合成粗体（图集是九个偏移求并集）要等并集完成再套表，所以那条路保留
  `Tone.Apply`；
- 表按 (墨色, 字号带, 是否合成粗体) 缓存，一个文档只有几种墨，逐字形不重复算。

测量（`TestPageRendererPrintsTheSameWeightAsTheScreen`，阿Q正傳第 5 页、17 DIP）：

| 测量 | 值 |
| --- | ---: |
| 离屏 PNG 墨量 / 窗口墨量 | **1.01×**（之前 0.88×） |
| 开修正 / 关修正 的墨量 | **1.14×** |
| 同一页 PNG 的深色像素（r<128） | +20%（3.80 万 → 4.56 万） |

曲线本身在 `layx-raster/tone_test.go` 里钉着：纸与墨不动（0→0、255→255）、单调不反转、
半覆盖处对比度把 128 抬到 150 以上、生长再往上抬、淡墨（夜读主题）只取生长不取对比度、
合成粗体等于"只取对比度"，以及**图集覆盖率与 `DrawGlyphs` 逐像素相同**（≤2/255）。

`LAYX_TEXT_TONE=0` / `LAYX_TEXT_GROW` / `LAYX_TEXT_STEM=0` 三个开关现在同时作用于
屏幕和 PNG——A/B 一次就能看到修正值多少。

## 已做：只重绘的帧（2026-10-26）

滚动的列和翻页的进度都是**时钟的函数**，所以手势中的帧不需要重建视图：draw 回调自己
`stepColumn()`（移动列、备好 tile），然后 `Painter.AnimationFrame()` 要下一帧——
mygo 的 `repaintFrame` 只重画上一帧的元素。落地那一下（以及每 `folioEvery` = 120 ms
的页脚更新）用 `Services.Invalidate()` 换一次真正的 build，把阅读位置写进 store、
把百分比/章节/书签滑块画到新位置。

- 视图每个手势只建一次，`perf.go` 的 `build` 从每帧一次变成每格一次；
- 日志里那些只画不建的帧也照常计数（`drawSheet` 自己 `perf.frameBegin`），
  否则一次手势会在日志里只剩两帧（`TestPerfLogCountsTheFramesThatOnlyPaint`）；
- 回归：`TestGlidePaintsWithoutBuilding`（滑动期间 build ≤ 1/4 帧且落地后页脚正确）、
  `TestTurnPaintsWithoutBuilding`（翻页同一套）、`TestRepaintPaintsTheSameColumn`
  （只重绘画出与 build 完全相同的画面）。

翻页的滑带顺带改成**整设备像素**（跟滚动列同一个 `snapDevices`）：滑带每帧落在
亚像素上时，两张跨页的每个字形都会换一张「相近的位图」——画面在抖，图集在一次翻页
里被塞进几千项。实测同一本书、缩放 2：一次翻页栅格化 **3797 → 545** 个字形，
图集项 2948 → 1639，层数 6 → 3（`TestTurnKeepsOneGlyphPhase` 用 `LAYX_NO_SNAP`
自己对照）。

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
| **backend** | `Raster(t *render.Tile, scale float32) (Bitmap, error)` 的可替换实现：纯 Go 软件（现在就是 [`layx-raster`](https://github.com/laix/layx-raster)：精确面积积分 + 自建字形图集）／宿主 `ui.Render` 包装／未来的 CoreText、DirectWrite、Skia 包装 | 各自独立，可只引一个 |

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

**把同样 170 行搬进 `scrollhost` 不会快一个毫秒。** 图集之前，3.15 ms 的构成是：

| 部分 | 图集之前 | 现在 | 谁能影响 |
| --- | ---: | ---: | --- |
| 字形几何（outline→path→flatten→hash） | ≈ 2.0 ms | **≈ 0.1 ms**（查表 + 贴图） | 宿主：已做（字形图集） |
| 整棵视图建树 + 布局 + commit | ≈ 1.0 ms | **每手势一次**（≈ 0.06 ms/帧，均摊） | 宿主：已做（只重绘的帧） |
| op 派发 + 裁剪跳过（A/B 的地盘） | < 0.05 ms | < 0.05 ms | 适配层可省一半以上 |
| 背景/边框矩形 | 0.003 ms | 0.003 ms | 谁都能 |

适配层的价值是**正确性只写一次、不变量可测、集成成本降低**，不是帧率。现在真能再降
成本的只剩一件，落在宿主：

1. ~~glide 帧用 `p.AnimationFrame`（repaint-only）~~ 已做：视图每个手势只建一次；
   页脚靠每 120 ms 一次的 build 跟上，不再是"滑动中百分比不刷新"的代价。
2. tile 预光栅成位图（`ui.Render` + `ui.NewBitmap`，整像素吸附）：把"每帧 ~900 个
   quad"换成"每帧 3 个 quad"，144 Hz 的余量再放大一档。代价是 Retina 显存
   （620×700×4B ≈ 1.7 MB/tile，2x 约 7 MB）、主题/字号/缩放变化时的作废，以及
   失去了"文字每一帧都精确落在亚像素网格上"（tile 必须整像素吸附）。

## 待办与决策点

1. ~~核心库：回放裁剪 + 字形 op 矩形~~ 已完成（`render/replay.go`，commit `9bb6365`），
   宿主侧 `UIPainter.Visible()` 也已接上：裁剪现在真的在 app 里触发，
   `TestDrawGlyphsCullChangesNoPixel` 同时覆盖画家自己的行裁剪和引擎的 op 裁剪。
2. ~~把 2.0 ms 的逐字形几何摊成"一帧一次 blit"~~ 已用**宿主侧字形图集**做到，
   并且它现在是所有渲染路径的默认（见「图集的默认开关」）。剩下 `layx-raster`
   的意义是**tile 位图缓存**（policy 层）与可替换后端（异步/多分辨率留第二期），
   不再是字形几何。
3. 适配层（可与 2 同一个 module）：A + D + E（这三块是我们 app 里已验证过的逻辑，
   搬过去风险最低）。`Layout.TileHeight()` 已有（`render/scroll.go`），这条前置条件
   已满足；适配层本身仍未开始。
4. 宿主手感三条：~~`ev.Precise` 直接位移~~、~~glide 用 repaint 帧~~、~~位置记录去抖~~
   都已完成：连续设备 1:1、滑动与翻页只重绘（见「已做：只重绘的帧」）、位置在落地
   和每 `folioEvery` 记一次而不是每帧。
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
go test -run XXX -bench 'BenchmarkScrollFrame|BenchmarkScrollGlideFrame' -benchtime 3s .
go test -run XXX -bench 'BenchmarkScrollViewportPaint|BenchmarkScrollViewportAtlas' -benchtime 3s ./internal/reader/
go test -run 'TestDrawGlyphsCullChangesNoPixel|TestGlyphAtlasMatchesOutlines' ./internal/reader/
go test -run 'TestGlidePaintsWithoutBuilding|TestTurnPaintsWithoutBuilding|TestRepaintPaintsTheSameColumn' .

# 字形图集：画质逐像素比对、稳定性、计数器、以及 draw 回调内的成本
go test -run 'TestGlyphAtlas' -v ./internal/reader/

# 上面同一批基准与比对，强制走旧路径，做 A/B（也可以 =1 反过来）
LAYX_GLYPH_ATLAS=0 go test -run XXX -bench 'BenchmarkScrollFrame' -benchtime 3s .
LAYX_GLYPH_ATLAS=0 go test -run XXX -bench 'BenchmarkScrollViewportPaint' -benchtime 3s ./internal/reader/

# 真窗口：开一个窗口滑这本书，报 wall/cpu 每帧、图集命中率与内存，跑完自己退出。
# 显示器刷新率会封顶 wall，所以同机比 cpu/frame：
go run ./cmd/glyphprobe                                  # 默认（图集）
LAYX_GLYPH_ATLAS=0 go run ./cmd/glyphprobe               # 旧路径：每帧重建轮廓
MYGO_GPU=0 go run ./cmd/glyphprobe                       # mygo 自带软件光栅（同样走图集）
MYGO_FRAME_STATS=1 go run ./cmd/glyphprobe               # mygo 自己的 build/paint/present 分帧

# layx 侧：建 tile 的成本（滚动侧不是瓶颈的证据）
cd ~/layx/render && go test -run XXX -bench 'BenchmarkTileCold640x900|BenchmarkTiles200' -benchtime 2s

# layx 侧：回放裁剪、字形 op 矩形与 TileHeight
cd ~/layx/render && go test -run 'Cull|GlyphOpsHaveFootprints|TileHeight' ./...

# 实机：app 自己记的日志（人读的，见 README「实机性能日志」）。
# 这份文档里的数字都来自上面几个跑在开发机上的工具；真机的绝对值
# 要看这一条，尤其 cpu/frame 与「budget 有几帧超了」：
LAYX_PERF=1 MYGO_FRAME_STATS=6 go run . book.epub
```
