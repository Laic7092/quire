package legado

// Definition is one Legado 3.0 book source definition.
type Definition struct {
	BookSourceName    string       `json:"bookSourceName"`
	BookSourceURL     string       `json:"bookSourceUrl"`
	BookSourceGroup   string       `json:"bookSourceGroup"`
	BookSourceCharset string       `json:"bookSourceCharset"`
	Enabled           *bool        `json:"enabled"`
	SearchURL         string       `json:"searchUrl"`
	RuleSearch        RuleSearch   `json:"ruleSearch"`
	RuleBookInfo      RuleBookInfo `json:"ruleBookInfo"`
	RuleToc           RuleToc      `json:"ruleToc"`
	RuleContent       RuleContent  `json:"ruleContent"`
}

// RuleSearch maps result fields to selectors.
type RuleSearch struct {
	BookList    string `json:"bookList"`
	Name        string `json:"name"`
	Author      string `json:"author"`
	Kind        string `json:"kind"`
	WordCount   string `json:"wordCount"`
	LastChapter string `json:"lastChapter"`
	Intro       string `json:"intro"`
	CoverURL    string `json:"coverUrl"`
	BookURL     string `json:"bookUrl"`
}

// RuleBookInfo maps book-detail fields to selectors.
type RuleBookInfo struct {
	Name     string `json:"name"`
	Author   string `json:"author"`
	Intro    string `json:"intro"`
	CoverURL string `json:"coverUrl"`
	TocURL   string `json:"tocUrl"`
}

// RuleToc maps chapter-list fields to selectors.
type RuleToc struct {
	ChapterList string `json:"chapterList"`
	ChapterName string `json:"chapterName"`
	ChapterURL  string `json:"chapterUrl"`
}

// RuleContent maps chapter-content fields to selectors.
type RuleContent struct {
	Content string `json:"content"`
}

// requestOpts is the trailing JSON object Legado allows on some rules.
type requestOpts struct {
	Method  string            `json:"method"`
	Body    string            `json:"body"`
	Charset string            `json:"charset"`
	Headers map[string]string `json:"headers"`
}
