# EPUB 语料的 CSS 账本

在 `cmd/corpus` 之前，这个工具只数 XHTML 里的 `style=""` 和 `<style>` 块。EPUB 的样式
绝大部分在外链 `.css` 里，所以"阅读器缺哪些 CSS"一直没有分母。本文就是分母：语料里
每一处 CSS 声明、选择器、at-rule、单位，各出现多少次、被几本书用到。

数字全部来自本分支的运行结果，命令见下。

## 一句话结论

- 这批书的样式几乎只有一件事：**段落间距 + 对齐 + 缩进 + 字号/字重**。
  `margin-*`（1540 条）、`text-align`(101)、`font-size`(90)、`text-indent`(58)、
  `font-weight`(51) 就覆盖了绝大部分版面差异。
- **2391 条声明里有 1175 条（49%）写在 `style=""` 属性里**，其中 99%
  （1162 条）是 `margin-top/left/right` 三种。不按元素支持 `style=""`，CSS 支持就是
  做一半：语料里最密的那几本书（`shijing`、`sanguoyanyi`、`xiyouji`）的版面差异主要在
  属性里。
- 单位只有 `em`(809) 和 `%`(775) 有量；`px` 23；**`rem`/`pt`/`vw`/`vh` 全为 0**。
- 选择器表看着复杂（class 243、后代组合 189、id 199），但 **199 个 `#id` 全部是
  Project Gutenberg 的页眉/页脚样板**（`#pg-header`、`#pg-footer`、
  `#pg-machine-header`…），class 里也有 102 次是 `pg*`/`x-ebookmaker-*`/`tei*`。
  正文排版是标签选择器 + 属性，**第一批不需要选择器引擎**。
- 语料里没有 `float`/`position`/`columns`/`flex`/`grid`，也没有 `@font-face`、
  `@import`、`@supports`，`!important` 一次都没有。

## 语料

`/home/laix/epub-corpus`：13 个 `.epub`，12 个能读。全部是 Project Gutenberg 的
ebookmaker 0.14.6 产物（9 本中文古典/现代作品，3 本英文），每本 2–3 个 `.css`
（`pgepub.css` + `0.css` + 部分有 `1.css`）。

`pride-and-prejudice.epub` 是截断的下载（`fetch.log` 里 curl 只跑到 416 KB），
`zip.OpenReader` 直接失败。账本把它标成 `SKIP` 后继续，**不让一本坏 zip 决定整个语料
的成败**。

| book | 章 | 样式表 | style=""+`<style>` | missing | 声明 | 不同属性 |
|---|--:|--:|--:|--:|--:|--:|
| alice.epub | 16 | 3 | 2 | 0 | 140 | 28 |
| elements-of-style.epub | 4 | 3 | 8 | 0 | 205 | 37 |
| fushengliuji.epub | 4 | 3 | 1 | 0 | 109 | 26 |
| hongloumeng.epub | 31 | 2 | 45 | 0 | 94 | 23 |
| laocanyouji.epub | 5 | 2 | 0 | 0 | 79 | 23 |
| liaozhaizhiyi.epub | 11 | 3 | 12 | 0 | 123 | 26 |
| rulinwaishi.epub | 7 | 3 | 7 | 0 | 119 | 26 |
| sanguoyanyi.epub | 13 | 3 | 223 | 0 | 540 | 26 |
| shijing.epub | 5 | 3 | 310 | 0 | 418 | 26 |
| shuihuzhuan.epub | 24 | 2 | 66 | 0 | 165 | 23 |
| waste-land.epub | 11 | 3 | 0 | 0 | 130 | 28 |
| xiyouji.epub | 34 | 2 | 176 | 0 | 269 | 23 |
| pride-and-prejudice.epub | — | — | — | — | — | SKIP：`zip: not a valid zip file` |

`elements-of-style.epub` 一本就用到了语料 38 个属性里的 37 个（它带 8 个 `<style>` 块，
是唯一带富样式的书）；其余 11 本只有 23–28 个，都是常见的间距/字号/对齐。

## 可复现命令

```sh
# 只扫 markup + CSS：不排版、不需要字体，整个语料约 0.1 秒
go run ./cmd/corpus -dir ~/epub-corpus -css-only

# 上面那张 per-book 表和下面所有 markdown 表格的来源
go run ./cmd/corpus -dir ~/epub-corpus -css-only -css-md

# 默认输出：排版账本 + CSS 摘要（-paint / -v 行为不变）
go run ./cmd/corpus -dir ~/epub-corpus

# 不依赖真实语料的单元测试（自带小型 CSS 与自带微型 EPUB）
go test ./cmd/corpus/
```

`-css=false` 关掉默认的 CSS 摘要；`-css-top 0` 打印全部行；`-v` 额外给出每本书的
linked/read/missing/声明数。

## 统计口径

账本是"出现次数"，不是"级联后生效的声明"。具体规则：

- 收集三处 CSS：`<link rel="stylesheet">`（`rel` 按空格分词，`icon` 之类不算）、
  `<style>` 块、`style=""` 属性。`@import` 最多跟两层，按 `seen` 去重防环。
- 相对路径按 `<link>` 所在的 XHTML 路径解析；解析不到再退回 OPF 目录再试一次；
  两次都没有则计入 `missing`（不 panic、不中断）。`%20` 会解码；zip 内大小写不一致
  时按小写再找一次。
- **同一个样式表一本书只读一次**。一章一个 `<link>`、一本文档上百章，否则 `0.css`
  会被同一本书数上百遍。
- 声明按块内 `;` 切分，冒号前为属性（`--custom` 与小写化后的属性除外：自定义属性
  不是阅读器能实现的声明，不计）；注释先整体剥掉，`@keyframes` 内部不计（本语料 0 次），
  `!important` 只做标记。
- `@media`/`@supports` 里的声明**照常计**：账本问的是"这本书指望这段样式生效吗"，
  一本书用 `@media` 包住一条规则，仍然说明它需要这条规则。
- "书数"= 出现该名字的**书本数**；声明数才是出现次数。表按书数排序，因为
  "有几本书会碰到它"比"某本书重复了多少遍"更能决定要不要做。

## 属性表

全部 38 个属性，按书数排序（书数相同按声明数）：

| property | declarations | books |
|---|---:|---:|
| `margin-top` | 655 | 12 |
| `margin-left` | 401 | 12 |
| `margin-right` | 365 | 12 |
| `margin-bottom` | 119 | 12 |
| `text-align` | 101 | 12 |
| `font-size` | 90 | 12 |
| `display` | 72 | 12 |
| `text-indent` | 58 | 12 |
| `margin` | 55 | 12 |
| `font-weight` | 51 | 12 |
| `width` | 47 | 12 |
| `padding` | 41 | 12 |
| `text-decoration` | 34 | 12 |
| `color` | 28 | 12 |
| `page-break-before` | 28 | 12 |
| `background-color` | 24 | 12 |
| `border` | 23 | 12 |
| `page-break-after` | 23 | 12 |
| `font-family` | 12 | 12 |
| `padding-top` | 12 | 12 |
| `line-height` | 22 | 11 |
| `all` | 45 | 9 |
| `clear` | 26 | 9 |
| `height` | 9 | 9 |
| `visibility` | 9 | 9 |
| `background` | 8 | 8 |
| `max-width` | 9 | 4 |
| `letter-spacing` | 4 | 3 |
| `font-style` | 3 | 3 |
| `word-spacing` | 2 | 2 |
| `font-variant` | 3 | 1 |
| `vertical-align` | 3 | 1 |
| `list-style-type` | 2 | 1 |
| `padding-left` | 2 | 1 |
| `padding-right` | 2 | 1 |
| `border-bottom` | 1 | 1 |
| `border-collapse` | 1 | 1 |
| `content` | 1 | 1 |

`all`（45 条 / 9 本）、`clear`（26 / 9）、`height`（9 / 9）排在书数前列，位置来自
`pgepub.css` 里的样板规则，不是正文排版，下面结论里会点名。

## 选择器类型

| kind | occurrences | books |
|---|---:|---:|
| `tag` | 550 | 12 |
| `class` | 243 | 12 |
| `id` | 199 | 12 |
| `attr` | 12 | 11 |
| `descendant` | 189 | 12 |
| `pseudo` | 11 | 3 |
| `universal` | 0 | 0 |

（`descendant` 指"由一个组合符粘起来的复合选择器"，`a > b`、`a b`、`a + b` 都算一次。）

拆开看复杂度来自哪里：

- 199 个 `#id` **全部**是 PG 样板：`#pg-footer`(57)、`#pg-header`(48)、
  `#pg-machine-header`(18)、`#project-gutenberg-license`(9)…
- class 里 102 次是 `pg*`/`x-ebookmaker-*`/`tei*`；剩下的是
  `xhtml_center`(18)、`figcenter`(13)、`caption`(12)。
- `attr` 选择器只出现在两种写法上：`a[href]`、`a[title]`，还是样板。

也就是说：**正文从来没有用复杂选择器定位过**；复杂选择器都在 PG 的页眉页脚盒子上，
而那块内容在阅读器里本来就该被跳过。

## at-rule

| at-rule | occurrences | books |
|---|---:|---:|
| `media` | 11 | 8 |
| `font-face` | 0 | 0 |
| `import` | 0 | 0 |
| `supports` | 0 | 0 |
| `charset` | 12 | 12 |
| `page` | 1 | 1 |

11 条 `@media` 全部按媒体**类型**分流：`screen` 8、`screen, print` 1、
`print, handheld` 1、`handheld` 1，**没有一条媒体特性查询**（没有 `min-width`）。
内容是 `body { margin-left: 8% }` 这类样板缩进；分页阅读器把它们当无条件规则处理即可。

## 单位

| unit | occurrences | books |
|---|---:|---:|
| `em` | 809 | 12 |
| `%` | 775 | 12 |
| `rem` | 0 | 0 |
| `pt` | 0 | 0 |
| `px` | 23 | 9 |
| `vw` | 0 | 0 |
| `vh` | 0 | 0 |

百分比出现在 `width`（85%/100%/45%…）、`margin-left`（8%/10%）、`font-size`
（110%/120%/90%）和 `text-indent`（0%）。

## 简写与 !important

| shorthand | declarations | books |
|---|---:|---:|
| `margin` | 55 | 12 |
| `padding` | 41 | 12 |
| `border` | 23 | 12 |
| `background` | 8 | 8 |

`!important` 全语料 **0 次**。

## 值分布：决定"能不能只做一半"

| 属性 | 值 |
|---|---|
| `text-align` | center 74、justify 13、left 10、right 4 |
| `font-weight` | normal 32、bold 19 |
| `display` | block 52、inline 10、table 9、none 1 |
| `page-break-before` | always 27、avoid 1 |
| `page-break-after` | always 22、avoid 1 |
| `text-decoration` | none 19、underline 15 |
| `text-indent` | 0 22、1em 10、-0.6em 9、-4em 9、0% 5、1.5em 2 |
| `font-size` | 110% 20、120% 20、0.9em 12、90% 11、medium 10、smaller 8 |
| `line-height` | 1.2em 9、1.4em 9、1.5 2、1.5em 2 |
| `margin-left` | 0 21、auto 18、8% 13、2em 10、4em 9、10% 5 |
| `width` | auto 12、100% 11、85% 8、45% 8、80% 3、90% 1、60% 1、50% 1 |
| `background-color` | white 13、grey 9、inherit 1、#fafafa 1 |
| `color` | black 13、initial 8、blue 4、red 2、#808080 1 |
| `padding` | 0 33、6px 5、`1px 4px 1px 4px` 1、`0.25em 0` 1、`0 0.75em` 1 |
| `border` | 0 12、`1px solid black` 8、`1px solid silver` 1、`1px dashed #808080` 1、none 1 |
| `visibility` | visible 9 |
| `clear` | both 26 |
| `all` | inherit 27、initial 18 |
| `font-family` | monospace 12 |

几条能直接读出结论的：

- `text-decoration: none`(19) 比 `underline`(15) 还多：三条 `a[href]`/`a.pgkilled`
  样板规则在去掉链接下划线。
- `display` 里唯一改变现状的是 `none`（1 次）；block/inline 与阅读器默认一致，
  `table` 则是"把表格摊平"的产物（语料本来就有 55 个 `<table>` 被摊平）。
- `text-indent` 有 19 条负值（`-0.6em`、`-4em`），是诗词的行首悬挂；`FirstLineIndent`
  必须是可负的。
- `margin-left: auto`(18) 是居中块，需要容器宽度，不是"间距"。
- `font-size` 全是相对值（`%`/`em`）加 `medium`/`smaller` 关键字，没有一条绝对值。
- `line-height` 写成 `1.4em` 这种长度，而 `render.Style.LineHeight` 是"字号的倍数"，
  需要 `em / font-size` 换算（`1.5` 无单位则直接当倍数）。

## 结论：CSS 子集第一批做这 11 项

顺序按"书数 × 现有 `render.Style` 字段"排，全部是 `internal/book` 里的纯映射，不需要
改布局器：

1. **`margin-top` / `margin-bottom` / `margin-left` / `margin-right`**（1540 条 / 12 本，
   另有 `margin` 简写 55 条）→ `Style.MarginTop/Bottom/Left/Right`。`auto` 先按 0。
2. **`text-align`**（101 / 12）→ `Style.TextAlign`（center/justify/left/right 全覆盖）。
3. **`text-indent`**（58 / 12）→ `Style.FirstLineIndent`，支持负值。
4. **`font-size`**（90 / 12）→ `Style.FontSize`，`%`/`em` 相对父级，`medium`/`smaller`
   走一张小关键字表。
5. **`font-weight`**（51 / 12）→ `Style.FontWeight`（normal/bold 就够，`700` 也接受）。
6. **`line-height`**（22 / 11）→ `Style.LineHeight`，`em` 换算成倍数。
7. **`page-break-before` / `page-break-after`**（51 / 12，`always` 占 49 条）→
   `Style.BreakBefore/BreakAfter` **字段已经存在**，只是没人喂它。
8. **`text-decoration`**（34 / 12）→ `Style.TextDecoration`（`none` 要能压掉继承的
   下划线，这正好是它的 19 条用法）。
9. **`color`**（28 / 12）→ `Style.Color`（注意 `initial` 8 条应视为继承）。
10. **`padding: 0` 只当无操作，`padding-top` 并入 `margin-top`**（41 + 12 / 12）：
    33 条 `padding` 就是 `0`，唯一有画面上效果的是 `h2 { padding-top: 1em }`。
    在阅读器里 padding 与 margin 视觉等价（没有紧贴内容画的边框/背景），先折算不会错。
11. **`<link rel=stylesheet>` + `@import` 的加载与层叠骨架**：这是载体，不是属性。
    按 `tag` + `class` + `style=""` 三档优先级（不需要 `#id`、不需要后代组合），
    因为 `style=""` 占了 49% 的声明量。

配套两条"值层面"的决定：

- 单位只做 `em` / `%` / `px`（`px` 直接当 CSS 像素），`rem` 当 `em` 兜底，
  `pt`/`vw`/`vh` 语料里 0 次，遇到就当 0 或忽略。
- `@media` 一律按无条件处理：语料里 11 条 `@media` 没有任何随宽度变化的语义。

## 明确不做（以及为什么）

| 不做 | 语料证据 |
|---|---|
| 浮动 / 定位 / 多栏 / flex / grid | `float`、`position`、`columns`、`flex`、`grid` **全部 0 次** |
| `@font-face`、`@import`、`@supports` | 0 / 0 / 0；字体是宿主的 `Typography` 与 `FontSet` 决定 |
| 选择器引擎（`#id`、后代组合、属性选择器、伪类） | 199 个 `#id` 全是 `#pg-*` 样板，`attr` 只有 `a[href]`/`a[title]`，正文没有一处需要 |
| `all: inherit/initial`（45 条 / 9 本） | 语义是"重置所有属性"，只做一半会得到比忽略更错的结果；忽略它至少还是继承 |
| `visibility`（9 / 9） | 9 次全是 `visible`，等于无操作 |
| `clear: both`（26 / 9） | 阅读器没有 float，无处可 clear |
| `width` / `max-width` / `height`（47 / 9 / 9） | 主体是 `85%`、`100%`、`45%` 这类块宽，需要容器宽度与"块内再分栏"的布局，收益只在 `hr`、封面图、题记；`height` 9 条里 8 条是封面页的 `100%` |
| `background-color` / `background`（24 / 8） | `white` 13 + `grey` 9，几乎全是 `body` 与封面页；纸色由主题与 `Typography.Background` 决定，照 CSS 走会和主题开关打架 |
| `font-family`（12 / 12） | 全语料只有一个值 `monospace`，且都挂在 `.pgmonospaced` 样板类上 |
| `letter-spacing` / `word-spacing`（4 / 2） | `Style` 字段虽在，量级不足以进第一批（可以顺手做，但不要作为验收项） |
| `list-style-type`(2)、`vertical-align`(3)、`border-collapse`(1)、`content`(1)、`font-variant`(3)、`font-style`(3) | 零散个例，等有真实语料再说；`li` 本来就没有画 marker（排版账本里 `li=251` 记着） |
| `border`（23 / 12） | `0` 12 条、`none` 1 条，真正画线的是 `hr` 与 8 条 `1px solid black`；`Style.Border*` 已存在，但它纯属"画得对不对"，与排版无关，放在第一批之后 |

## 已知局限

- **语料是同一条流水线的产物**（ebookmaker 0.14.6，PG 的 CSS Cookbook）。"书数"
  最高就是 12，很多属性并列 12 本，这个维度在这批书上区分度有限；扩容语料后
  （中文站点导出的 EPUB、带竖排/`@font-face` 的书）这张表会变。
- 计数是"文本里出现过几次"，不是"级联后哪条生效"。同一属性被 `p`、`div p`、
  `style=""` 各写一遍就算三次。
- 只统计 XHTML 里被 `<link>` 引用的 CSS。OPF manifest 里列了但没有文档引用的样式表
  不计；`<?xml-stylesheet?>` 处理指令也不计。这批 ebookmaker 书里两者都为空，换一批
  语料时要记得先看这一条。
- 只统计属性名，不统计"属性 × 值"的全部组合：值分布只针对上面点名的那几个属性。
- 布局账本（页数、`-paint`、节点数）与本次改动无关，行为未变；唯一的行为变化是
  坏归档从"ERROR + 退出码 1"变成 `SKIP` 并继续，见 `pride-and-prejudice.epub`。
