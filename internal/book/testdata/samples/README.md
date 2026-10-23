# 样书

联调和测试用的真实书籍。全部是**公有领域**文本，来自 Project Gutenberg，
不包含任何盗版网文。`go test ./internal/book` 会逐个打开它们（见
`TestSamplesOpen`），所以截断的文件不会被误提交。

| 文件 | 来源 | 用来覆盖什么 |
|---|---|---|
| `aq-zhuan.epub` | [PG #25332](https://www.gutenberg.org/ebooks/25332) 鲁迅《阿Q正傳》 | 真实 EPUB2：PNG 封面页（`wrap0000.html` 里只有 `<img>`）、Gutenberg 分块 HTML、NCX 目录 |
| `aq-zhuan.txt` | 同上，Gutenberg 纯文本 | UTF-8 BOM、CRLF、全角章节标题 `第一章　序`、缩进段落的空白行分块 |
| `fusheng-liuji.txt` | [PG #25192](https://www.gutenberg.org/ebooks/25192) 沈復《浮生六記》 | 硬换行的长文本、`第X卷` 标题、单卷超过 20000 rune 时自动切分（`（2）`） |

这些作品在美國属于公有领域。文件保持原样，没有手工修改；测试里的
GB18030 / UTF-16 / 网文结构样本是由测试代码临时生成的，不占仓库空间。

## 关于“像《斗破苍穹》那样的样书”

《斗破苍穹》仍在版权保护期内，不适合下载或提交。它对我们真正重要的特征是
**结构**：几百个 `第N章` 标题、每段一行、动辄几千章。这个结构在
`TestTextWebNovelShape` 里按同样形状生成，足以覆盖章节切分、分页和性能路径。

如果要手工用真实网文联调，自己准备一本合法来源的 `.txt` 即可，解析器不依赖
特定作品。
