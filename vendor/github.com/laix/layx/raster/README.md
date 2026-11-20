# layx/raster

[`layx`](../README.md) 的纯 Go 软件光栅器，独立 module（`github.com/laix/layx/raster`，
tags `raster/v0.x.y`），与 [`epub`](../epub) 同级。输入是 layx 已经排版好的显示列表
（`render.Page` / `render.Tile`），输出是 `*image.RGBA`。

layx 核心刻意**不内置光栅器**：它只产出 `paint.Painter` 调用序列，字形 ID、位置、颜色都由
shaping 和布局决定。本仓库补上缺的那一半——覆盖率计算和像素混合——所以离线截图、快照、
e-ink 驱动、没有 GPU 的宿主都能直接跑，而不必再写一个 Canvas/OPENGL 后端。

```text
layx                          layx/raster
Document → 布局 → DisplayList → paint.Painter
                                        ↓
                              覆盖率扫描线 / 裁剪掩码 / 混合
                                        ↓
                                  *image.RGBA → PNG
```

## 用法

```go
import (
    "github.com/laix/layx/raster"
    "github.com/laix/layx/render/paint"
)

page := layout.Page(0)
r := raster.New(int(page.Size().Width), int(page.Size().Height))
r.Clear(paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) // 纸
page.Draw(r)                                              // 墨
png.Encode(w, r.Image())
```

滚动的 tile 一样：`tile.Draw(r)`。位图通过 `r.SetImage(id, img)` 注册，
`DrawImage` 会按当前变换做双线性采样。

命令行示例（排版一段文本并输出第 N 页 PNG）：

```sh
cd raster
go run ./cmd/layx-shot -font book.ttf -text chapter.txt -page 0 -out page.png
```

`cmd/layx-shot` 只做三件事：把纯文本切成段落建 `render.Document`、用 `render.Engine`
排版、把一页画进 PNG。EPUB/XHTML/CSS 仍然由 `github.com/laix/layx/epub` 负责。

## 已实现

| `paint.Painter` | 说明 |
| --- | --- |
| `Save` / `Restore` | 变换与裁剪的栈；裁剪掩码不可变，回退时不复制 |
| `Transform` | 任意仿射（旋转、镜像、缩放、错切都正确） |
| `ClipRect` | 整数轴对齐矩形走零成本快速路径；其余旋转/小数边缘生成逐像素覆盖率掩码 |
| `FillRect` | 轴对齐时按精确面积覆盖率填充，斜置时走多边形扫描线 |
| `StrokeRect` | 描边画成一个环（外轮廓 + 反向内轮廓），重叠处不会二次混合 |
| `FillRoundRect` | 四角三次贝塞尔圆弧，变换后按设备像素细分 |
| `DrawImage` | 逆变换逐像素映射 + 双线性采样（预乘插值，透明边缘不发黑） |
| `DrawGlyphs` | 直接从字体轮廓（`paint.Font.Data`）栅格化；按面/字形缓存轮廓 |
| `Clipped.Visible` | 把当前裁剪框反变换回用户空间，带 1px 余量，供 layx 剔除不可见 op |

低层接口：

| API | 说明 |
| --- | --- |
| `NewCoverage` / `Reset` | 一块可复用的设备空间覆盖率缓冲，画路径、取 8 位 alpha |
| `Coverage.MoveTo/LineTo/QuadTo/CubeTo/Alpha` | 非零环绕、隐式闭合；`layx-reader` 的字形图集用它，逐字形栅格化一次 |
| `NewTone` / `Tone.Coverage` / `Tone.Apply` | 墨色修正表：系统文字打印机给字形覆盖率加的那层对比度与字干增粗 |
| `Coverage.SetTone` | 让 `Alpha` 在写出的同时应用修正；图集与整页出图用同一张表 |
| `Rasterizer.ToneFor` | 宿主按 run（墨色、字号、是否合成粗体）提供修正，`DrawGlyphs` 逐 run 应用 |

`Coverage` 是 `Rasterizer` 内部填充字形与矢量形状的同一套积分器（精确面积、非零
环绕），只是少了整页状态与混合。需要自己的图集/字形缓存、或只想要一张 alpha mask
的宿主直接用它，不必实现 `paint.Painter`。

其他行为：

- **抗锯齿**：逐像素**精确面积积分**，不做超采样。每条边按它穿过的每个像素行
  落一块带符号面积，行内从左到右累加出环绕数，取绝对值并夹到 1 就是覆盖率：
  水平方向的覆盖率是解析的，竖直方向由行重叠自然得到。
- **工作量与路径成正比**：只有边真正穿过的行才会被写；像素循环每行一次。
- **矩形不逐像素算覆盖率**：一行是「一段整列 + 最多两个小数边缘像素」，整列的
  覆盖率整行相同；不透明时直接按 64 字节的色块 memmove 写出去（一个 840×1200 的
  纸面 0.19 ms，逐像素混合要 5 ms）。混合里的除以 255 全部换成移位加法。
- **字形图集**：`Coverage` 逐字形输出覆盖率，`layx-reader` 的图集把结果存进纹理；
  那是屏幕和离屏（PNG）共用的同一个光栅器，所以屏幕上的一页和导出的 PNG 是同一批
  像素。冷栅格（每页第一次见到字形）约 3.2 µs/字形（17 DIP、DPR 2、CJK），
  暖帧只查表，不受影响。
- **墨色修正**：几何光栅器只画轮廓，系统打印机还会加两层——随墨色变深的对比度
  （DirectWrite 的 grayscale）和字干边缘的生长（CoreText 的 font smoothing、
  FreeType 的轻度 stem darkening）。`Tone` 是这条曲线的唯一实现：`DrawGlyphs` 通过
  宿主给的 `ToneFor` 用它，宿主的自建图集通过 `Coverage.SetTone` 用它，所以屏幕上
  一页和导出的 PNG 墨量一致（实测差 1%），而关掉它整页会轻 12–14%。
- **合成粗体**：`GlyphRun.EmulateBold` 时沿文字自身 x 轴再画一遍（不改变布局）。
  合成粗体的 run 应取"只有对比度、没有生长"的那张表（`ToneOptions.SyntheticBold`）。
- **对齐坐标**：side bearing 与基线都来自 `paint.Glyph`，不会重新 shaping 字符串。
- **不拥有字体**：只读取 `paint.Font.Data`，按「地址 + 长度 + 面索引」解析并缓存。
- **确定性**：相同显示列表、相同字体、相同坐标得到逐字节相同的像素。

## 和平台光栅器比

`reference/freetype/` 里有三个 C 文件，用 FreeType 的灰度光栅器把同一份工作做一遍
（同字体、同字号、同位置），只当尺子，不参与构建。2026-10-26，amd64、Go 1.24、
FreeType 2.13.2、wqy-microhei、34 px（17 DIP @ DPR 2）、《阿Q正傳》第 5 页：

| | 每字形冷栅格 | 同一页（474 字形） | 与 16×16 点采样真值的平均误差 | 墨量/真值 |
| --- | ---: | ---: | ---: | ---: |
| layx/raster | **3.4 µs** | **1.9 ms**（含纸面与墨色修正） | **0.085/255** | 1.0022 |
| FreeType | 5.0 µs | 3.3–3.6 ms（只有栅格与混合） | 0.122/255 | 1.0049 |

误差更小不是调出来的：精确面积积分对的是面积，FreeType 的灰度光栅器是 4×4 子采样。
`TestGlyphFidelity` 把这件事钉在 CI 里（DejaVu Serif，34 px，误差 < 0.35/255、
墨量 ±1%），无需 C 工具链。复现步骤见 `reference/freetype/README.md`。

## 边界与取舍

- 只有灰度抗锯齿，没有 RGB subpixel 定位。文字位置本来就带小数，但没有 ClearType
  式的三通道滤波；这是为了在不同 DPR 下保持可预测。
- 没有 gamma 校正，混合在 sRGB 空间做预乘运算；不追求与浏览器逐像素一致。
- 只支持轮廓字形：COLR/CPAL 彩色字体、SVG 字形、位图字形会被跳过。
- 没有字体回退、没有 shaping、没有断行——这些都在 layx 里。
- 一个 `Rasterizer` 不是并发安全的；每个 goroutine 用自己的实例。
- 裁剪掩码按整个表面分配（页面尺寸）。这是离线渲染/快照的取舍；实时大画布应改用
  tile 加局部表面。
- 除了 `Coverage` 的字形级覆盖率，不提供通用路径/画布 API；`paint.Painter` 仍是
  整页绘制的唯一入口，本包不会变成通用 2D 绘图库。

## 包结构

```text
raster/                         嵌套 module `github.com/laix/layx/raster`
  raster.go       Rasterizer、表面、状态栈、矩形/描边/圆角填充
  clip.go         裁剪的快速路径与覆盖率掩码，Visible
  geometry.go     仿射、box→device、扁平化（二次/三次贝塞尔）、shape
  coverage.go     字形级覆盖率 API（Coverage）：路径、8 位 alpha、可复用
  cover.go        精确面积积分（带符号面积）、字节累积、混合
  glyph.go        字体解析缓存、轮廓缓存、字形路径、合成粗体
  image.go        DrawImage 的逆映射与双线性采样
  cmd/layx-shot   文本 → 文档 → 一页 PNG 的最小示例
  reference/      与平台光栅器（FreeType）对照用的 C 工具，不参与构建
  testdata/       DejaVu Serif（测试字体，Bitstream Vera 许可，见 testdata/DejaVu-LICENSE.txt）
```

直接依赖只有 `github.com/laix/layx`（`replace` 到上层 `../`）和
`github.com/go-text/typesetting`（字体轮廓）。`golang.org/x/image` 是 layx 的间接依赖，
本包自己不用它；也没有 cgo。

## 测试

```sh
cd raster
go test ./...
go run ./cmd/layx-shot -font testdata/DejaVuSerif.ttf -out /tmp/page.png

# 覆盖率：与 16x16 点采样的暴力对照，以及字形大小的路径开销
go test -run TestCoverage -v .
go test -run TestGlyphFidelity -v .
go test -run XXX -bench 'BenchmarkCoverageGlyph|BenchmarkDrawGlyphs' -benchtime 2s .

# 与 FreeType 比（可选，需要 C 工具链，见 reference/freetype/README.md）
go test -run XXX -bench BenchmarkGlyphRasterizer -benchtime 2s .
```

测试覆盖：矩形/α 混合/半像素覆盖、覆盖率与点采样的暴力对照（三角形、洞、圆）、
变换组合、Save/Restore、整数与小数裁剪、裁剪掩码在 Restore 后不被污染、圆角、
描边环、图像 1:1 与缩放、字形确定性/粗体/旋转，以及从 `render.Document` 到
`Page.Draw` 的整页端到端（含超出画布平移后的剔除正确性）。
