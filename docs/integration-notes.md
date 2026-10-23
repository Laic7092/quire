# 联调笔记：一个宿主对 layx 的反馈

> 这是 layx-reader（`github.com/laix/layx-reader`）作为 layx 宿主的观察记录，
> 属于个人意见，不是 layx 的设计结论。放在这里，是因为它只对一个真实应用负责，
> 先不反过来影响 layx 仓库。
>
> 记录时间：图片/封面联调之后。

## 本次联调做了什么

把 EPUB 图片接通了整条链路：

```
manifest 图片 ──► book.Image（ID 稳定）──► render.NodeImage
                                              │
                                     layx 布局/分页占位
                                              │
                                    paint.Painter.DrawImage
                                              │
                        ui.DecodeBitmap + ui.ImageRegion
```

同时：

- **封面发现**按 OPF 的顺序：`properties="cover-image"` → `<meta name="cover">` →
  `guide type="cover"` → 首个纯图片章节。纯图片封面页**从阅读流中移除**，由 UI
  单独整页展示，避免"一张被栏宽裁掉一半的封面"。
- **空章节兜底**从"节点数等于 1"改成"没有任何可见的文本或图片"。原先 `<body><div>`
  包着一个被丢弃的 `<svg>` 会留下三个空 block，于是页面纯白且没有提示；现在会显示
  “（本章没有可显示的文本）”。

结论：**layx 的 `NodeImage` 契约本身够用**——给固有宽高，引擎会按栏宽缩放、按页
分页、把目标矩形交给 painter。缺的部分全在宿主侧：资源生命周期、尺寸探测、位图缓存。

## 观察到的摩擦点

按"如果 layx 补上，宿主能少写多少"排序。

### 1. `paint.Painter` 没有官方后端

现象：实现 `DrawGlyphs` 要自己处理 upem 缩放、Y 轴翻转、轮廓取点、拼 path，约
150 行；图片还要再做一层 `DecodeBitmap` + 缓存。

代价：每个宿主重写一遍。这个仓库的 `internal/reader/painter.go` 就是样板。

建议：官方提供 `layx/mygo` 与 `layx/software` 两个后端；把"字形轮廓 → path"抽成
`paint/outline` 这类辅助，绘制侧只填颜色。

### 2. `Style` 的零值语义无法表达"显式取消继承"

现象：`flow.go` 的 `inheritStyle` 用零值判断"未设置"，所以下列合法 CSS 写不出来：

```css
p { text-indent: 0 }        /* FirstLineIndent:0 被当成未设置，继续继承 */
span { letter-spacing: normal }
* { hyphens: manual }
```

代价：CSS adapter 无法忠实翻译，只能忽略这些声明。

建议：用 `StyleFlags` 位图（如 `SetFirstLineIndent`）区分"未设置"与"显式零"；退一步
至少在文档里写清这一限制。指针字段和三态枚举都更重。

顺带确认一个好消息：字号、颜色、字重、斜体、行高、对齐、装饰这些**已经在引擎里继承**
了。我之前以为 adapter 必须自己展开整条继承链，是错的——这省掉不少宿主代码。

### 3. 没有增量 / 流式布局

现象：改字号或改窗口 = 整本书重新 `Layout`。宿主只能自增 `job` 号丢弃过期结果，
算力已经花掉了。

代价：大书首屏不可控，也没法做"当前章先出、其余后台"的进度。

建议：`LayoutOptions.Priority`、按章布局，或提供"只重分页不重排"的入口（`Repaginate`）。
这可能是核心 API 层面最值得先讨论的一项。

### 4. 没有"保持阅读位置"的辅助

现象：改字号后 `page` 直接 clamp 到新范围，用户会跳到别的段落。

建议：宿主需要的是"当前页首锚点 → 新布局页码"，一个 `Layout.Resolve(Location)`
或 `PageOfAnchor` 包装即可，不必让每个 App 自己拼 `Locate` + `PageOf`。

### 5. 图片链路缺参考实现

`ImageID` 不透明是对的，但生命周期需要写明：**资源表变化（如换书）后，宿主必须清空
自己的位图缓存**，否则旧 ID 会命中新书的图片。这个仓库因此在 `openAsync` 里显式调用
`UIPainter.ClearImages()`。

建议：一句文档 + 一个 20 行的示例，比任何类型设计都有效。

### 6. 字体面（face）

现象：`NewFontSet` 只喂了一个 weight 400 的 face，`<strong>`/`<h1>` 的 700 静默退化。
引擎按 weight/slant 选择 face 是正确的，但宿主不知道去哪里找粗体和斜体。

建议：`layx/systemfont` 负责"同族 bold/italic 配对 + CJK fallback"；文档明确"不合成
伪粗体/伪斜体"，让预期对齐。

### 7. XHTML/CSS 的边界

宿主目前必须自己写：UA 默认表、空白折叠、`em/%` 单位换算、列表符号、图片尺寸探测。

建议：`layx/html` 作为**可选**包（不放进核心），接受
`ResolveImage(href, wHint, hHint)` / `ResolveLink(href)` 回调。这样它不需要认识 zip，
epub 包也不需要认识标签。

一个命名提醒：EPUB 里的"XHTML"在现实中大量不是合法 XML（不闭合标签、裸 `&nbsp;`），
所以必须用 `golang.org/x/net/html` 的容错 tokenizer。包名叫 `xhtml` 会误导后来者去
找 XML parser，叫 `html` 更诚实。

## 对 layx 形态的意见

```
layx/                 核心：IR + 布局 + 分页 + 命中/锚点（只依赖 go-text）
layx/epub             可选：zip + OPF + spine + nav/NCX → Document
layx/html             可选：XHTML → Chapter（UA 表、空白、单位换算）
layx/mygo             可选：MyGo Painter 后端
layx/software         可选：纯 Go 光栅器（wasm / 测试 / e-ink）
examples/reader       参考宿主（本仓库最终形态）
```

几条边界规则：

- 核心不出现 EPUB 概念（`spine` / `linear` / `guide` / `cover` / `NCX`），也不出现
  任何 UI 框架类型。
- 可选包各自独立 module，用 `go.work` 联调；核心的依赖门测试保持不变。
- 稳定部分与演进部分分开：EPUB 容器层小而稳，HTML→IR 会持续长（CSS 子集、空白、
  列表、表格），不要让前者被后者拖着升级。
- 单一判断标准：**宿主写的是业务代码还是适配代码。** 适配代码越长，说明库缺东西。
  本仓库 `internal/`（不含测试）约 1550 行，几乎全是可提炼的适配代码，目标是把它们
  压到接近零。

## 这个仓库还没解决的自留问题

- 中间插图页（非封面、纯图片、比内容区高）会按栏宽缩放后被页底裁切：缺少"图片适配
  页面"的布局选项。
- 纯矢量 SVG 封面只留空白占位（不内置光栅器，合理，但需要文档和降级提示）。
- 章标题回退只认 `h1–h3`；calibre 用 `<div class="shuming">` 排的书会得到"第 N 章"。
  这是"没有 CSS 时如何取标题"的策略问题，适合在 `layx/html` 里统一。
- 每次重排都会重新读盘图片字节，尚未做跨 build 的资源缓存。
