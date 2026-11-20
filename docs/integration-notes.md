# 联调笔记：宿主对 layx / MyGo 的依赖与缺口

quire 是 layx 的宿主探针：它把每一条"宿主不得不自己写"的代码都记在这里，作为
上游 API 的输入。本文只保留**当前仍然成立**的结论；逐轮的历史不再保留，要查某个决定
的来龙去脉看 git 记录。

## layx 必须保持的不变量

1. **`Page` 自足、可重放。** 一页的显示列表不依赖之前画过谁，`Draw` 可以反复调用。
   翻页滑带、双页大开本、快照测试都建立在这条上；`Draw` 里不能排版（曾因取字形卡帧）。
2. **`GlyphRun.Font` 带字节 + 面号。** 宿主按面缓存轮廓，所以一页里可以同时出现衬线
   正文和黑体标题。若 `paint.Font` 去掉 `Data`/`Index`，这条路就断了。
3. **`Style` 的零值表示继承 `Typography`。** 字号/行距/字面/对齐才能由 `Layout` 统一
   控制；在文档样式里写死一项，对应的开关就会失效。
4. **`Location` 是 `(chapter, node 内偏移)`，`Anchor` 跨 `Build` 稳定。** 宿主的位置
   恢复顺序是 node → anchor → progress → 章首，靠的就是这两层。
5. **颜色不进位置计算。** 纸色/墨色写进文档样式，所以换纸、换夜间只能整本重排；这是
   已知代价，宿主接受，但不要在引擎里把它变成"每次绘制都重排"。
6. **`Document`/`Layout` 一版一用。** node id 只属于一份文档，位置矩形只属于一份布局；
   宿主在重排/换书时必须清掉选区、命中和搜索索引。

## 仍然缺的 API（按宿主价值排序）

1. **源派生的、跨 `Build` 稳定的锚点（最高）。** 现在 anchor 由宿主提供，换字号没问题，
   换书源/更新 EPUB 后旧位置会静默偏移。目录高亮、搜索跳转、位置恢复压的是同一个缺口：
   由引擎从源结构派生稳定标识。
2. **`Layout` 的页 → 位置。** `LocationOfPage(n)` 已经解决了"页 → 位置"，`PageOf` 是它的
   反向；滚动模式没有页，`PageOf` 按设计返回 false，宿主改用 `OffsetOf`。还差的是
   `TileHeight()`：宿主得自己记住传给 `TileHeight` 的值，才能算第一个可见 tile。
3. **`Document.TextRange(from, to)`。** 复制选中的文字目前沿 `Chapter.Nodes` 走 DFS
   拼串（与搜索索引的遍历几乎重复）。图片节点、空 block、反向拖选都要宿主自己兜。
4. **增量/流式排版与文档版本。** 改页边距、换纸会重排整本书；`Layout` 又不暴露它的
   `*Document`，宿主无法判断索引是否过期。**"只排当前几章"已解决**：`LayoutOptions.Range`
   加宿主的 `reader.RelayoutRange`，5 章一次约 1 ms，拖窗口的预览就压在这上面
   （见下文"resize 分两步"）。还差的是一个 document 版本号，以及"当前章先排、其余
   向后补进同一份 `Layout`"的流式入口——现在预览章和整本书是两份布局。
5. **位置保持的 `Relayout`。** 现有 `Relayout` 要宿主先算好位置再回贴；建议
   `RelayoutKeeping(ctx, opts)`，宿主不再写"取位置—重排—找页"的三段。
6. **tile 的跨界行给个明确的画法。** 现在"从下往上画 + 多画一块 + 不裁 tile"是宿主
   自己推出来的；建议 `Tile` 暴露它越界的行范围（或直接给按行而不是按固定高度切片的
   输出），宿主就不必靠画序补边界。图片在相邻 tile 里也会重复，透明度高的图会被叠两次。
   另一半是**回放时按画家可见区跳过 op**：现在 `DrawGlyphs`/`FillRect` 一律交给画家，
   视口外的字也走完整几何流程，每个宿主都得多画约 3 个视口再自己裁一次。建议
   `paint` 加可选接口 `Clipped interface{ Visible() Rect }`（不破坏现有实现），构建
   显示列表时给字形 op 补行盒矩形，`Page.Draw`/`Tile.Draw` 求交后跳过。
   定位与实测见 [`scroll-performance.md`](scroll-performance.md)。
7. **书架用的元数据。** 已解决：`epub.ReadMetadata` 只读 container.xml、OPF 和封面，
   `quire/internal/library` 不再自己解包文档。

8. **图片链路给一份参考实现。** 位图按 `ImageID` 由宿主缓存、跨文档清空、按栏宽缩放，
   这套语义现在只在宿主里；引擎侧最好有"图片在页面里的矩形"的明确 API。
9. **XHTML/CSS 的边界写清楚。** 哪些标签/属性会被映射、哪些被有意丢弃，应由 `render`
   或 `epub` 给出一张清单；现在宿主在自己的 README 里维护这份清单。
10. **目录层级。** 已解决，两边都改了：`layx/epub` 的 `Book.Toc` 保留 nav/NCX 的
   顺序、嵌套、`#fragment` 和每个目标的 `render.AnchorID`；`quire/internal/toc`
   接上它之后一个章节可以有多行（章内小节），层级、跳转位置和“正在读”都来自真实
   数据。宿主只剩两条保险：标签太长的行（有些书的 NCX 塞的是整段正文）和只写了一个
   条目的桩目录，遇到就退回按章节建行，`levelOf` 只在这时和 TXT 上兜底。
11. **字体注册给 UI 文本。** 书用 `render.NewFontSet`，MyGo 的文字用 `text.Shared()`，
   宿主得手动 `ui.RegisterFont` 才能让界面和正文用同一份字体。
12. **MyGo 的几处控件契约。** `ui.Split` 没有最小宽度（页面被挤到小于三个页边距时
    `Layout` 直接失败）；`ui.Segmented` 的外观无法接管；`ui.Switch` 的绑定值点击后不
    变化；菜单快捷键先于视图 `Shortcut`（所以 `⌘C` 不能进菜单）；`Tester.Click(文字)`
    点的是文字节点，整行可点必须把 `OnClick` 挂在带 `Label` 的祖先上；**手指在惯性
    滑行中落下时，整个手势被吞掉**（见下条）。
13. **触摸：惯性滑行中的第一下手指被吞，滚动要摸两次。** `ui/touch.go` 的
    `platform.PointerDown` 里 `if rt.flinging.on { rt.flinging.on = false; t.swallow = true;
    return true }`——这一下只用来刹住惯性，既不算点、也不开始拖动。而惯性按
    `flingDecay = 0.998`/ms 衰减、到 `flingStop = 20` DIP/s 才停，一次 2000 DIP/s 的
    快甩要**约 2.3 s**，这段时间里怎么摸都「滚不了第二次」。目录/搜索/笔记的 `ui.List`、
    书库的 `ui.Scroll` 与书源结果都在这条上。
    v0.10.0 与 main 都是这样，升级依赖无解。建议上游：按下停掉 fling 后照常进入
    press/pan（拖动从惯性停下的位置接着走），只有「没移动就抬手」才不算点击（否则会
    误触列表行）。阅读页已在宿主侧绕过（`mobile.go` 的 `takesFinger`：调一次
    `PointerPosition()` 让书页成为 `flagTrackPointer`，引擎就不 pan 它，`PointerMove`
    直接进 `handlePageInput`，于是既没有引擎 fling，也能按速度自己做惯性）。

## 宿主侧已经踩过的坑

- **进程级的东西不能按窗口存。** `reader` 的调色板（`SetPalette`/`Current`）和
  MyGo 的 `Theme.SetSource` 都是进程级的，多窗口下纸色必须所有窗口一致，窗口按钮的
  外观由焦点窗口决定；字体表（`render.FontSet`）是只读的，全 app 只解一次；菜单栏
  只有一条，只由焦点窗口更新。窗口自己的页码、面板、滚动位置、选区与布局才属于窗口。
- **窗口分两种角色。** 书库窗（唯一，纸色跟随系统，`StateKey: main`）与阅读窗
  （一本书一扇，纸色是书页，`book-N`）：阅读窗在书到达前也按书页画，不能闪一下书库；
  Esc / 返回是关掉阅读窗并亮出书库，不是把阅读窗改成书库。关窗和开窗都要排到下一轮
  主循环，不能在正在构建的那一帧里拆窗或建窗。
- **重排必须清选区/命中**：node id 只属于一份布局，忘了就会画到旧位置。
- **`Draw` 回调只重放，不排版**。
- **`⌘C` 不能有菜单项**：macOS 的 key equivalent 先于视图，页面选区和搜索框会一起坏。
- **隐藏标题栏要自己让位**：`c.TitleBar()` 的 `Height/Left/Right` 零值不是唯一情况，
  全屏时归零，布局不能因此跳动。
- **模式切换别让排版和视口打架**：设置先变、布局后到，`scrolling()` 跟着已发布的
  布局走，排版宽度按目标模式的页宽重算（`sheetSize`），否则从双页切到滚动会拿半宽
  排一列、或者拿整窗宽排一页。
- **滚动位置在重排后是行首，不是像素**：存 `Location`/`Anchor`，不要存 `scrollY`。
- **resize 分两步，别一个尺寸一个全量任务。** 页宽进了 `BreakKey`（`render/keys.go`），
  改宽度=整本重新断行+shaping：一本 1000 章 / 7 MB 的 TXT，一次约 145 ms、454 MB 分配，
  拖窗口 30 个尺寸事件会排 30 个全量任务，最后一个（唯一想要的）要 3.5 s，总分配 12 GB、
  堆 2.9 GB——GC 和 CPU 一起压到绘制线程上，表现就是"等全量跑完才响应"。
  现在的做法（`preview.go`）：尺寸一变就只排当前章 ±2（`RelayoutRange`，链在上一份
  预览上，约 1 ms），屏幕立刻重排；`resizeDelay`（160 ms，和设置滑杆同一个静默期）后
  才排整本书；起新任务前 `cancelLayout()` 取消旧任务（引擎认 ctx，实测 cancel 后
  24 ms 内退出）。预览只被"画和点"读（`sheetPlan`/`columnPlan`），页码、面板、
  阅读位置仍属于已发布的那份布局；整本落地时用 `Location` 接回原位，页码不动。
- **主题缓存的 key 要含"书库还是阅读"**：书库用系统外观、阅读用纸，纸没变时若不重建，
  关书回书库仍是米色。
- **截图像素不是布局常量。** 参考图只在它自己的窗口宽度下成立；首页的侧栏、边距、封面
  都由窗口宽度按比例推再夹上下限（`homeMetricsFor`），否则换窗口/换 DPI 就散架。
- **统计不要新造状态**：阅读天数、连续天数、年度读完都从 `store` 已有的
  `Books.LastOpened` 与 `Positions.Updated` 现算；只有目标值进 `Prefs`。

## 滚动模式（`ModeScroll`）用到的语义

- `ModeScroll` 只换"取窗口"的方式：断行、块布局与分页模式共享，所以同一份
  `Typography` 在两种模式下排出的行完全一样。
- 内容坐标（`y=0` 是正文顶端、章与章连续堆叠）是滚动模式唯一的坐标系：
  `LocationAt` / `OffsetOf` / `HitTest` / `Progress` / `SelectionQuad.Content` 都用它，
  和 `Tile.Top()` 一起就能把位置映射到像素。
- 分页模式的 `PageRange` / `ChapterPages` / `PageOf` 在滚动模式下按设计失效
  （`ok=false` / `nil` / false）；`ChapterContentTop` 两种模式都成立，是目录与
  "跳到某章"的中立入口。
- `SelectionQuad.Page` 在滚动模式是 -1，宿主不能再用页号挑矩形，要用 `Content`
  减去滚动偏移。
- tile 是显示列表不是位图：宿主自己决定缓存策略。宿主在滚动前后调 `Tiles(lo, hi)`
  预取，`Draw` 回调里只重放 `Tile(i)`。缓存尺寸要跟视口配：`TileCache` 默认 8 块，
  一块一屏时 ≈ 320 行，已超过 `LineCache` 默认的 256 行，回看会重新 shaping。
  一帧的成本构成、144 Hz 预算与“哪些该由上游做”见
  [`scroll-performance.md`](scroll-performance.md)。
- **一行只属于它顶边所在的 tile，所以它会越过 tile 底边。** tile 的显示列表以整块纸
  打底，宿主必须**从下往上**画（下一块先铺纸，上一块连越界的行一起盖上去），并且
  视口顶端要**多画上面一块** tile，否则边界上的那行会正好被切掉半截、停在屏幕中间。
  `Tile.Draw` 自带的 `ClipRect`（裁到 tile 自身大小）不要照做：照做同样会把越界的
  行切掉。宿主的 `UIPainter.ClipRect` 因此是空实现，只靠视口裁剪。

## 宿主为此付出的适配成本（当前）

| 位置 | 内容 |
| --- | --- |
| `internal/appstate` | 面板切换、异步刷新、索引与文档同步、位置恢复 |
| `internal/reader/selection.go` | 选区端点、分页矩形、DFS 拼文本 |
| `internal/reader/painter.go` | `paint.Painter` 的 UI 后端、按面缓存轮廓、封面摆放 |
| `internal/reader/glyph_atlas.go` | 字形图集：轮廓只栅格化一次，之后每帧查表 + 贴图（见 `scroll-performance.md`） |
| `view.go` | 阅读页、书库源列表、首页（含 `homeMetricsFor`）、目标卡、各面板接线 |
| `theme.go` | 书库系统外观与阅读纸两套 palette、皮肤缓存 |
| `stats.go` | 阅读统计与目标持久化 |
| `sheet.go` | 双页几何、页码对齐、滑页动画、书脊与边缘阴影 |
| `scroll.go` | 滚动视口：可见 tile、内容坐标 ↔ 屏幕坐标、滚动键与滚动位置归档 |
| `internal/store/position.go` | `Locate` / `PositionAt`：位置在两种模式下都成立 |
| `internal/reader/theme.go` | 若干套纸的完整 palette |

这些是宿主自己的账，不再逐轮罗列；新增一个功能时先问"这段能不能由上游给"，
能就记在上面两节，不能就留在宿主并以测试钉住行为。
