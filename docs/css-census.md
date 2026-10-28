# EPUB 语料的 CSS 账本

「阅读器该支持哪些 CSS」需要分母，这就是分母：`cmd/corpus` 把语料里每一处 CSS 声明、
选择器、at-rule、单位数一遍。**表格不抄在这里**——它会随语料腐烂，需要时一条命令重出。

```sh
go run ./cmd/corpus -dir ~/epub-corpus -css-only -css-md   # 全部账本表格
```

## 一句话结论

- 这批书的样式几乎只有一件事：**段落间距 + 对齐 + 缩进 + 字号/字重**。`margin-*`
  （1540 条）、`text-align`(101)、`font-size`(90)、`text-indent`(58)、`font-weight`(51)
  就覆盖了绝大部分版面差异，第三条起就只剩个例。
- **2391 条声明里有 1175 条（49%）写在 `style=""` 属性里**，其中 99% 是
  `margin-top/left/right`。不按元素支持 `style=""`，CSS 支持就是做一半：语料里样式最密的
  几本书（`shijing`、`sanguoyanyi`、`xiyouji`）的版面差异主要在属性里。
- 单位只有 `em`(809) 与 `%`(775) 有量，`px` 23；**`rem`/`pt`/`vw`/`vh` 全为 0**。
- 选择器表看着复杂（class 243、后代组合 189、id 199），但 **199 个 `#id` 全部是
  Project Gutenberg 的页眉页脚样板**（`#pg-header`、`#pg-footer`…），class 里也有
  102 次是 `pg*`/`x-ebookmaker-*`/`tei*`。正文排版是标签选择器 + 属性，
  **第一批不需要选择器引擎**。
- 语料里没有 `float`/`position`/`columns`/`flex`/`grid`，没有 `@font-face`/`@import`/
  `@supports`，`!important` 一次都没有，`@media` 也没有一条媒体特性查询。

## 语料

`~/epub-corpus`：13 个 `.epub`，12 个能读（`pride-and-prejudice.epub` 是截断的下载，
`zip.OpenReader` 直接失败，账本标 `SKIP` 后继续——不让一本坏 zip 决定整个语料的成败）。
全部是 PG 的 ebookmaker 0.14.6 产物：9 本中文古典/现代、3 本英文，每本 2–3 个 `.css`。
38 个属性里有 37 个出现在 `elements-of-style.epub` 一本（唯一带 `<style>` 块的书），
其余 11 本都是常见的间距/字号/对齐。

## 统计口径

账本是「出现次数」，不是「级联后生效的声明」：`p`、`div p`、`style=""` 各写一遍算三次。

- 收集三处：`<link rel=stylesheet>`、`<style>` 块、`style=""`。`@import` 最多跟两层并
  按 `seen` 去重防环。相对路径先按 XHTML 所在目录解析，不行再退回 OPF 目录，都不行计入
  `missing`（不 panic）。**同一个样式表一本书只读一次**（一章一个 `<link>`、
  一本文档上百章，否则 `0.css` 会被数上百遍）。
- 声明按块内 `;` 切分；自定义属性（`--x`）与注释不计，`@keyframes` 内部不计，
  `!important` 只做标记。`@media`/`@supports` 里的声明**照常计**：账本问的是
  「这本书指望这段样式生效吗」。
- 「书数」= 出现该名字的书本数，「声明数」是出现次数。表按书数排序——「有几本书会碰到
  它」比「某本书重复了多少遍」更能决定要不要做。
- 只统计被 `<link>` 引用的 CSS：OPF manifest 里列了但没被文档引用的、`<?xml-stylesheet?>`
  处理指令都不计（这批书里两者都为空，换语料要先看这两条）。

## 结论：CSS 子集第一批做这 11 项

顺序按「书数 × 现有 `render.Style` 字段」排，全部是 `layx/epub/html.go` 里的纯映射，
不需要改布局器：

1. **`margin-top`/`bottom`/`left`/`right`**（1540 条 / 12 本，另有 `margin` 简写 55）
   → `Style.Margin*`，`auto` 先按 0。
2. **`text-align`**（101 / 12）→ `Style.TextAlign`，center 74、justify 13、left 10、right 4。
3. **`text-indent`**（58 / 12）→ `Style.FirstLineIndent`，**必须可负**（19 条是诗词的
   悬挂缩进：`-0.6em`、`-4em`）。
4. **`font-size`**（90 / 12）→ `Style.FontSize`。全是相对值（`%`/`em`）加
   `medium`/`smaller` 关键字，没有一条绝对值。
5. **`font-weight`**（51 / 12）→ `Style.FontWeight`，normal/bold 就够（`700` 也收）。
6. **`line-height`**（22 / 11）→ `Style.LineHeight`。语料写成 `1.4em` 这类长度，而它是
   「字号的倍数」，要 `em / font-size`；无单位的 `1.5` 直接当倍数。
7. **`page-break-before`/`after`**（51 / 12，`always` 占 49）→
   `Style.BreakBefore/BreakAfter`，**字段已存在，只是没人喂它**。
8. **`text-decoration`**（34 / 12）→ `Style.TextDecoration`。`none`(19) 比 `underline`(15)
   还多，因为三条样板规则在去掉链接下划线——`none` 要能压掉继承。
9. **`color`**（28 / 12）→ `Style.Color`。`initial` 8 条应视为继承。
10. **`padding: 0` 当无操作，`padding-top` 并入 `margin-top`**（41 + 12 / 12）：33 条
    `padding` 就是 0，唯一有画面效果的是 `h2 { padding-top: 1em }`；阅读器里没有紧贴内容
    画的边框/背景，padding 与 margin 视觉等价。
11. **`<link rel=stylesheet>` + `@import` 的加载与层叠骨架**（载体，不是属性）：按
    `tag` + `class` + `style=""` 三档优先级即可，不需要 `#id`、不需要后代组合。

配套两条值层面的决定：单位只做 `em`/`%`/`px`（`px` 当 CSS 像素，`rem` 兜底成 `em`）；
`@media` 一律按无条件处理。

## 明确不做（以及为什么）

| 不做 | 语料证据 |
|---|---|
| float / position / 多栏 / flex / grid | 全部 **0 次** |
| `@font-face`、`@import`、`@supports` | 0 / 0 / 0；字体是宿主的 `Typography` 与 `FontSet` 决定 |
| 选择器引擎（`#id`、后代、属性、伪类） | 199 个 `#id` 全是 `#pg-*` 样板，`attr` 只有 `a[href]`/`a[title]` |
| `all: inherit/initial`（45 / 9 本） | 「重置所有属性」，只做一半比忽略更错 |
| `visibility`（9 / 9） | 全是 `visible`，等于无操作 |
| `clear: both`（26 / 9） | 没有 float，无处可 clear |
| `width`/`max-width`/`height`（47 / 9 / 9） | 主体是 `85%`/`100%`/`45%` 的块宽，要有容器宽度与块内分栏；收益只在 `hr`、封面图、题记 |
| `background-color`/`background`（24 / 8） | `white` 13 + `grey` 9，几乎全是 `body` 与封面页；纸色归主题，跟 CSS 走会和主题开关打架 |
| `font-family`（12 / 12） | 全语料只有一个值 `monospace`，都挂在 `.pgmonospaced` 样板类上 |
| `border`（23 / 12） | `0` 12 条、`none` 1 条，真正画线的是 `hr` 与 8 条 `1px solid black`；`Style.Border*` 已有，但纯属「画得对不对」 |
| letter/word-spacing、list-style、vertical-align、font-variant、content | 零散个例（各 1–4 次），等有真实语料再说 |

`display` 里唯一改变现状的是 `none`（1 次）；`table`(9) 是「表格被摊平」的产物——
语料本来就有 55 个 `<table>` 没有结构。

## 已知局限

- **语料是同一条流水线的产物**（ebookmaker 0.14.6 + PG 的 CSS Cookbook），「书数」最高
  就是 12，很多属性并列 12 本，这个维度在这批书上区分度有限。换一批语料
  （中文站点导出的 EPUB、带竖排/`@font-face` 的书）要把所有表重跑一遍。
- 只统计属性名，不统计全部「属性 × 值」组合；值分布只对上面点名的那几个属性做过。
- 语料不在仓库里（`.gitignore` 里的 `corpus/`），换机器要自己准备。

## 复现

```sh
# 只扫 markup + CSS：不排版、不需要字体，整个语料约 0.1 秒
go run ./cmd/corpus -dir ~/epub-corpus -css-only

# 上面所有表格的来源
go run ./cmd/corpus -dir ~/epub-corpus -css-only -css-md

# 默认输出：排版账本 + CSS 摘要（-paint 会真的把每页画进一个空 painter）
go run ./cmd/corpus -dir ~/epub-corpus -v -paint

# 不依赖真实语料的单元测试（自带小型 CSS 与自带微型 EPUB）
go test ./cmd/corpus/
```

排版账本那部分是给「覆盖面」做的账：逐本打印页数、耗时和**它用了什么、读者表示了
没有**——`lost:` 是读者真会看出差别的东西（表格被摊平、`<pre>` 空白被折叠、脚注上标变成
普通字符、列表标记没画），`unstyled:` 是文字还在样式丢了，`walked:` 是纯容器。
末尾的 corpus-wide 汇总就是「该不该做某个特性」的分母：语料里用时为 0 的构造，
不能只因为浏览器支持就排进核心。`-css=false` 关掉 CSS 摘要，`-css-top 0` 打印全部行。
