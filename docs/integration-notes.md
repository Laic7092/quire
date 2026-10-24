# 联调笔记：宿主对 layx / MyGo 的依赖与缺口

layx-reader 是 layx 的宿主探针：它把每一条"宿主不得不自己写"的代码都记在这里，作为
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
4. **增量/流式排版与文档版本。** 打开面板、改页边距、换纸都会重排整本书；`Layout`
   又不暴露它的 `*Document`，宿主无法判断索引是否过期。期望一个 document 版本号，
   以及"只排当前章、向后补"的入口。
5. **位置保持的 `Relayout`。** 现有 `Relayout` 要宿主先算好位置再回贴；建议
   `RelayoutKeeping(ctx, opts)`，宿主不再写"取位置—重排—找页"的三段。
6. **图片链路给一份参考实现。** 位图按 `ImageID` 由宿主缓存、跨文档清空、按栏宽缩放，
   这套语义现在只在宿主里；引擎侧最好有"图片在页面里的矩形"的明确 API。
7. **XHTML/CSS 的边界写清楚。** 哪些标签/属性会被映射、哪些被有意丢弃，应由 `render`
   或 `epub` 给出一张清单；现在宿主在自己的 README 里维护这份清单。
8. **目录层级。** `book` 层把 nav/NCX 拍平成 `map[path]title`，嵌套、顺序、fragment
   都丢，宿主只能按标题猜层级。这是 `layx/epub` 该给的结构。
9. **字体注册给 UI 文本。** 书用 `render.NewFontSet`，MyGo 的文字用 `text.Shared()`，
   宿主得手动 `ui.RegisterFont` 才能让界面和正文用同一份字体。
10. **MyGo 的几处控件契约。** `ui.Split` 没有最小宽度（页面被挤到小于三个页边距时
    `Layout` 直接失败）；`ui.Segmented` 的外观无法接管；`ui.Switch` 的绑定值点击后不
    变化；菜单快捷键先于视图 `Shortcut`（所以 `⌘C` 不能进菜单）；`Tester.Click(文字)`
    点的是文字节点，整行可点必须把 `OnClick` 挂在带 `Label` 的祖先上。

## 宿主侧已经踩过的坑

- **重排必须清选区/命中**：node id 只属于一份布局，忘了就会画到旧位置。
- **`Draw` 回调只重放，不排版**。
- **`⌘C` 不能有菜单项**：macOS 的 key equivalent 先于视图，页面选区和搜索框会一起坏。
- **隐藏标题栏要自己让位**：`c.TitleBar()` 的 `Height/Left/Right` 零值不是唯一情况，
  全屏时归零，布局不能因此跳动。
- **模式切换别让排版和视口打架**：设置先变、布局后到，`scrolling()` 跟着已发布的
  布局走，排版宽度按目标模式的页宽重算（`sheetSize`），否则从双页切到滚动会拿半宽
  排一列、或者拿整窗宽排一页。
- **滚动位置在重排后是行首，不是像素**：存 `Location`/`Anchor`，不要存 `scrollY`。
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
  预取，`Draw` 回调里只重放 `Tile(i)`。

## 宿主为此付出的适配成本（当前）

| 位置 | 内容 |
| --- | --- |
| `internal/appstate` | 面板切换、异步刷新、索引与文档同步、位置恢复 |
| `internal/reader/selection.go` | 选区端点、分页矩形、DFS 拼文本 |
| `internal/reader/painter.go` | `paint.Painter` 的 UI 后端、按面缓存轮廓、封面摆放 |
| `view.go` | 阅读页、书库源列表、首页（含 `homeMetricsFor`）、目标卡、各面板接线 |
| `theme.go` | 书库系统外观与阅读纸两套 palette、皮肤缓存 |
| `stats.go` | 阅读统计与目标持久化 |
| `sheet.go` | 双页几何、页码对齐、滑页动画、书脊与边缘阴影 |
| `scroll.go` | 滚动视口：可见 tile、内容坐标 ↔ 屏幕坐标、滚动键与滚动位置归档 |
| `internal/store/position.go` | `Locate` / `PositionAt`：位置在两种模式下都成立 |
| `internal/reader/theme.go` | 若干套纸的完整 palette |

这些是宿主自己的账，不再逐轮罗列；新增一个功能时先问"这段能不能由上游给"，
能就记在上面两节，不能就留在宿主并以测试钉住行为。
