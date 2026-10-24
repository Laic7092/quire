# layx 上 Web：wasm 体积实测与三档结合方式

本文讨论 **分发形态**（要不要、怎么在浏览器里跑 layx），不讨论帧率——帧率与画质见
[`scroll-performance.md`](scroll-performance.md)。所有体积数字都在本机实测，命令附在
文末，可复现。

## 一句话结论

- **能结合，而且仓库里已经有原型**：`examples/wasm` 就是 wasm + Canvas 2D + Path2D，
  `bench.go` 里还有一个 Web Worker 池。
- **体积达不到 1 MB，更不可能到 KB 级**：实测内核 **1.84 MB gzip**，其中 Go 运行时
  自身就占 **576 KB gzip**。真正的体积大头是 go-text（shaping + Unicode 表），不是 layx。
- **比 wasm 更该担心的是字体**：一个中文字体 5–19 MB，必须子集化 + WOFF2。
- 最深的那种结合（wasm 出像素、浏览器只做 GPU 合成）在 Web 上比在 native 更容易，
  因为 **Worker + OffscreenCanvas** 天生就提供了"后台光栅"。

## 实测体积

环境：`go1.27.2 linux/amd64`，`GOOS=js GOARCH=wasm`，`-trimpath -ldflags="-s -w"`，
`gzip -9`。brotli 本机未安装，未测（wasm 一般再小 15–20%，即 ≈1.5 MB）。

| 构建 | 原始 | gzip -9 | 说明 |
| --- | ---: | ---: | --- |
| Go 空壳（只 `syscall/js`） | 1.97 MB | **576 KB** | 运行时地板，任何 Go wasm 都不可能低于它 |
| + layx 的 IR / Document / 类型 | 3.04 MB | 863 KB | 没调用 `Layout`，shaping 被死代码消除，**不可用作产品体积** |
| + 真实 `Layout`（shaping/断行/字体解析全保留） | 6.98 MB | **1.84 MB** | 这才是"内核"的真实数字 |
| `examples/wasm` 完整 demo | 7.30 MB | **1.92 MB** | 含 bench、canvas 画家、手写 JSON |

增量构成（gzip）：

| 组成 | 体积 |
| --- | ---: |
| Go 运行时 + `syscall/js` | 576 KB |
| layx 本体（IR、块布局、分页、滚动切片、命中/选区） | ≈ 290 KB |
| go-text（shaping、UAX #14 断行表、字体解析） | ≈ 980 KB |
| demo 自身（bench、画家、JSON） | ≈ 90 KB |

结论：**压体积要动的是 go-text 与字体，不是 layx 的逻辑**。

- `examples/wasm` 的 demo 与"内核 + 真实 Layout"只差 90 KB，说明 demo 代码不是负担，
  生产构建剔除 bench 也就省几十 KB。
- 想再小只有三条路，都要单独验证：
  1. **TinyGo**：本机未安装，未实测，不给数字。它的运行时比 Go 小一个量级，但
     `reflect`/goroutine 支持有限，go-text 是否全兼容要实测；乐观区间 0.4–0.9 MB。
  2. **拆 shaping**：把断行/分页留在 wasm，把 shaping 交给浏览器（见下方 T1 的
     `fillText` 变体）——但那会放弃我们的 justification 与标点挤压控制。
  3. **拆章节**：只把"当前章 + 相邻章"打进一个更小的实例（多 wasm 实例或按需加载），
     对首屏体积有边际帮助，对总量没有。
- 传输侧：`WebAssembly.compileStreaming` + brotli + 强缓存；`wasm_exec.js` 本身 17 KB。

## 字体才是真正的大头

| 字体 | 体积 |
| --- | ---: |
| WQY MicroHei（.ttc） | 5.2 MB |
| Noto Sans CJK（.ttc） | ≈ 19 MB |
| 子集（常用 3000–5000 字，TTF） | ≈ 1–2 MB |
| 同上，WOFF2 | ≈ 0.6–1.2 MB |

所以 Web 端必须做：**按书内容子集化**（服务端或构建期）+ **WOFF2** + **按章懒加载**。
注意 wasm 侧读的是**字体字节**（`paint.Font.Data`），浏览器读的是 `@font-face`；
两条链路可以用同一份子集文件，但要分别送达。

## 三档结合方式

| 档 | 谁出像素 | 优点 | 缺点 |
| --- | --- | --- | --- |
| **T1 排版预言机** | Canvas 2D：`Path2D`（现有 demo）或 `fillText` | 集成最轻；几何完全由我们控制（Path2D 时浏览器不重排） | 每帧重填路径 → native 的同样问题 + wasm 慢 4.6×（见 README 的 A/B 表：269.6 vs 59.1 ms） |
| **T2 wasm 出像素 + GPU 合成** | wasm 里软件光栅（`layx-raster` 的纯 Go 后端）→ `ImageData` → 纹理 → 每帧 translate | 唯一能稳 144 Hz 的形态；**Worker + OffscreenCanvas 提供天生异步光栅** | 包体 + 显存（瓦片 RGBA：384²×4 ≈ 590 KB/张）；画质要自己负责 |
| **T3 整个 app 搬进浏览器** | mygo 已有 web 后端（`internal/web/surface.go`，`requestAnimationFrame`） | 一套代码多端 | 最重；wasm 单线程布局是硬上限，要 Worker 池，跨源隔离（COOP/COEP）才谈得上 SharedArrayBuffer |

已经存在的东西（别重复写）：

- `examples/wasm/painter.go`：Canvas 2D 画家，字形走 go-text outline → `Path2D`，
  注释里写明"绝不让浏览器重新 shaping"。
- `examples/wasm/bench.go`：`initLayout`/`layoutChapter`/`benchBook`，是给 Worker 池
  准备的接口，同一份样式与选项，保证"池里排一章"与"整本里排那一章"页数一致。
- `examples/wasm/build.sh`：构建 + 找字体 + 起本地服务的完整流程。

## 建议的最小版本

1. **先 T1（Path2D）**：最快拿到"排版正确"的证据，可以和 `scripts/linecmp.sh` 的
   逐行结果对拍；此时不追求滚动帧率。
2. **再 T2**：`layx-raster` 的策略层 + 纯 Go 后端在 wasm 里跑，主线程只 `drawImage`。
   native 与 web 共享同一份"瓦片策略 + 后端接口"代码——这是"深度结合"最实在的收益：
   一次实现，两端复用。
3. 布局并行用 Worker 池（`bench.go` 已有形状）；跨线程传 `ArrayBuffer` 用 Transferable，
   避免拷贝。
4. 体积/性能门槛先写死（建议值）：
   - wasm ≤ 2 MB（brotli ≤ 1.6 MB）；
   - 首屏字体 ≤ 1.5 MB（子集 + WOFF2）；
   - 20 万字整书布局 ≤ 400 ms（单线程；README 实测 200k → 269.6 ms）；
   - 滚动帧 ≤ 4 ms（wasm 单线程，T2 形态）。

## 待验证

- TinyGo 能否编译 go-text（决定体积能否进 1 MB 以内），以及编译后的实测体积与
  shaping 正确性（跑 `linecmp.sh` 对拍）。
- wasm 里 `layout` 的 CPU 分布：单线程 269.6 ms 里 shaping 与断行的占比（决定 Worker
  池切章策略）。
- `ImageData → 纹理` 的上传成本：是否需要 `texSubImage2D` 增量上传、是否要
  `OffscreenCanvas` 让浏览器自己管合成。
- 子集化方案：按书、按章、按阅读进度预取，哪一档在移动网络上最稳。

## 复现

小体积标本要放在 layx module 内（依赖本地 `replace`），实测时临时创建、用后删除。

```sh
cd ~/layx
go version   # 本文用 go1.27.2 linux/amd64

# 1) demo：完整体积（含 bench）
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o /tmp/layx_demo.wasm ./examples/wasm
stat -c%s /tmp/layx_demo.wasm; gzip -9 -c /tmp/layx_demo.wasm | wc -c

# 2) 地板：只有运行时
mkdir -p tools/zzsize/min && cat > tools/zzsize/min/main.go <<'EOF'
//go:build js && wasm

package main

import "syscall/js"

func main() { js.Global().Call("alert", "hi") }
EOF

# 3) 内核：真实调用 Layout；字体字节从 JS 传入，避免把字体打进 wasm
mkdir -p tools/zzsize/kernel && cat > tools/zzsize/kernel/main.go <<'EOF'
//go:build js && wasm

package main

import (
	"context"
	"syscall/js"

	"github.com/laix/layx/render"
)

var data []byte

func fontData() []byte {
	if data == nil {
		buf := js.Global().Get("Uint8Array").New(0)
		dst := make([]byte, buf.Length())
		js.CopyBytesToGo(dst, buf)
		data = dst
	}
	return data
}

func main() {
	js.Global().Set("layout", js.FuncOf(func(js.Value, []js.Value) any {
		b := render.NewDocumentBuilder().
			SetStyles([]render.Style{{}, {FontFamily: "serif", FontSize: 18}}).
			SetMetadata(render.Metadata{Language: "zh-CN", Reflowable: true})
		_ = b.AddChapter(render.Chapter{ID: "ch1", Root: 0, Nodes: []render.Node{
			{Kind: render.NodeBlock, Children: []render.NodeID{1}},
			{Kind: render.NodeText, Style: 1, Text: "正文……"},
		}})
		doc, err := b.Build()
		if err != nil {
			return err.Error()
		}
		fonts, err := render.NewFontSet(render.FontFace{Family: "serif", Weight: 400, Data: fontData()})
		if err != nil {
			return err.Error()
		}
		eng, err := render.New(render.Options{})
		if err != nil {
			return err.Error()
		}
		l, err := eng.Layout(context.Background(), doc, render.LayoutOptions{
			Viewport:   render.Viewport{Width: 600, Height: 900},
			Typography: render.Typography{FontFamily: "serif", FontSize: 18, LineHeight: 1.6},
			Fonts:      fonts,
			Mode:       render.ModeScroll,
			TileHeight: 900,
		})
		if err != nil {
			return err.Error()
		}
		_ = l.Tile(0)
		return l.TileCount()
	}))
	select {}
}
EOF

for t in min kernel; do
  GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o /tmp/$t.wasm ./tools/zzsize/$t
  printf "%s raw=%s gzip=%s\n" "$t" "$(stat -c%s /tmp/$t.wasm)" "$(gzip -9 -c /tmp/$t.wasm | wc -c)"
done

rm -rf tools/zzsize
```

用 `gzip -9 -c` 之外的压缩器时（wasm 一般比 gzip 再小 15–20%）：
`brotli -q 11 -c /tmp/kernel.wasm | wc -c`。
