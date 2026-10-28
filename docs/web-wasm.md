# layx 上 Web：wasm 体积实测与三档结合方式

只讨论**分发形态**（要不要、怎么在浏览器里跑 layx），不讨论帧率——帧率与画质见
[`scroll-performance.md`](scroll-performance.md)。

## 一句话结论

- **能结合，仓库里已经有原型**：`layx/examples/wasm` 就是 wasm + Canvas 2D + `Path2D`，
  `bench.go` 里还有一个 Worker 池。
- **体积到不了 1 MB，更不可能 KB 级**：内核 **1.84 MB gzip**，其中 Go 运行时自身
  576 KB。真正的体积大头是 go-text（shaping + Unicode 表），不是 layx 的逻辑。
- **比 wasm 更该担心的是字体**：一个中文字体 5–19 MB，必须按书子集化 + WOFF2。
- 最深的结合方式（wasm 出像素、浏览器只做 GPU 合成）在 Web 上比 native 更容易，
  因为 **Worker + OffscreenCanvas** 天生就是「后台光栅」。

## 实测体积

`go1.27.2 linux/amd64`，`GOOS=js GOARCH=wasm`，`-trimpath -ldflags="-s -w"`，`gzip -9`
（brotli 未装未测，wasm 一般再小 15–20%）：

| 构建 | 原始 | gzip -9 |
|---|---:|---:|
| Go 空壳（只 `syscall/js`）——任何 Go wasm 的地板 | 1.97 MB | **576 KB** |
| + layx 的 IR / Document / 类型（没调 `Layout`，shaping 被死代码消除） | 3.04 MB | 863 KB |
| + 真实 `Layout`（shaping/断行/字体解析全保留）——内核的真实数字 | 6.98 MB | **1.84 MB** |
| `examples/wasm` 完整 demo（含 bench、canvas 画家、手写 JSON） | 7.30 MB | **1.92 MB** |

增量（gzip）：Go 运行时 576 KB、go-text（shaping、UAX #14 断行表、字体解析）≈ 980 KB、
layx 本体（IR、块布局、分页、滚动切片、命中/选区）≈ 290 KB、demo 自身 ≈ 90 KB。
**压体积要动的是 go-text 与字体，不是 layx。** demo 与「内核 + 真实 Layout」只差 90 KB，
说明 demo 不是负担；想再小只有三条路，都要单独验证：TinyGo（运行时小一个量级，但
`reflect`/goroutine 支持有限，go-text 兼容性要实测，乐观区间 0.4–0.9 MB）；把 shaping
交给浏览器（放弃 justification 与标点挤压控制）；按章拆实例（只帮首屏，不帮总量）。
传输侧：`WebAssembly.compileStreaming` + brotli + 强缓存，`wasm_exec.js` 本身 17 KB。

## 字体才是真正的大头

WQY MicroHei 5.2 MB、Noto Sans CJK ≈ 19 MB；子集（常用 3000–5000 字）≈ 1–2 MB，
子集 + WOFF2 ≈ 0.6–1.2 MB。所以 Web 端必须**按书内容子集化 + WOFF2 + 按章懒加载**。
注意 wasm 侧读的是字体字节（`paint.Font.Data`），浏览器读的是 `@font-face`；两条链路
可以用同一份子集文件，但要分别送达。

## 三档结合方式

| 档 | 谁出像素 | 优点 | 缺点 |
|---|---|---|---|
| **T1 排版预言机** | Canvas 2D：`Path2D`（现有 demo）或 `fillText` | 集成最轻；几何完全由我们控制 | 每帧重填路径，native 的同样问题 + wasm 慢 4.6× |
| **T2 wasm 出像素 + GPU 合成** | wasm 里软件光栅（`layx-raster` 纯 Go）→ `ImageData` → 纹理 → 每帧 translate | 唯一能稳 144 Hz 的形态；Worker + OffscreenCanvas 提供天生异步光栅 | 包体 + 显存（384²×4 ≈ 590 KB/瓦片）；画质自己负责 |
| **T3 整个 app 搬进浏览器** | mygo 已有 web 后端（`requestAnimationFrame`） | 一套代码多端 | 最重；wasm 单线程布局是硬上限，要 Worker 池 + 跨源隔离 |

已经存在、别重复写的东西：`examples/wasm/painter.go`（Canvas 2D 画家，字形走 go-text
outline → `Path2D`，注释写明「绝不让浏览器重新 shaping」）、`bench.go`
（`initLayout`/`layoutChapter`/`benchBook`，给 Worker 池用的接口，保证「池里排一章」与
「整本里排那一章」页数一致）、`build.sh`（构建 + 找字体 + 起服务）。

## 建议的最小版本

1. **先 T1（Path2D）**：最快拿到「排版正确」的证据，可以和 `scripts/linecmp.sh` 的逐行
   结果对拍；此时不追求滚动帧率。
2. **再 T2**：`layx-raster` 的策略层 + 纯 Go 后端在 wasm 里跑，主线程只 `drawImage`。
   native 与 web 共享同一份「瓦片策略 + 后端接口」——这是「深度结合」最实在的收益。
3. 布局并行用 Worker 池（`bench.go` 已有形状），跨线程传 `ArrayBuffer` 用 Transferable。
4. 门槛先写死：wasm ≤ 2 MB（brotli ≤ 1.6 MB）、首屏字体 ≤ 1.5 MB、
   20 万字整书布局 ≤ 400 ms（native 实测 269.6 ms）、滚动帧 ≤ 4 ms（T2 形态）。

## 待验证

- TinyGo 能否编译 go-text（决定能否进 1 MB），以及编译后的体积与 shaping 正确性。
- wasm 里 269.6 ms 的 CPU 分布：shaping 与断行各占多少（决定 Worker 池切章策略）。
- `ImageData → 纹理` 的上传成本：是否需要 `texSubImage2D` 增量上传、是否交给
  `OffscreenCanvas` 让浏览器自己管合成。
- 子集化方案：按书、按章、按阅读进度预取，哪一档在移动网络上最稳。

## 复现

体积标本要放在 layx module 内（依赖本地 `replace`），实测时临时创建、用后删除：
一个只有 `syscall/js` 的空壳当地板，一个调一次真实 `Layout` 当内核（字体字节从 JS 传
进去，别把字体打进 wasm）。

```sh
cd ~/layx
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o /tmp/demo.wasm ./examples/wasm
stat -c%s /tmp/demo.wasm; gzip -9 -c /tmp/demo.wasm | wc -c
brotli -q 11 -c /tmp/demo.wasm | wc -c     # 装了 brotli 时
```
