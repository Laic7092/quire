package book

import (
	"bytes"
	"encoding/xml"
	"strings"

	"golang.org/x/net/html"
)

// parseNavTOC reads an EPUB 3 navigation document: the first <nav> whose type
// says "toc". It maps each linked archive path to the link's text.
func parseNavTOC(data []byte, navPath string) map[string]string {
	titles := map[string]string{}
	z := html.NewTokenizer(bytes.NewReader(data))
	navDepth, aDepth := 0, 0
	inTOC := false
	current, buf := "", ""

	finishLink := func() {
		if current != "" {
			if title := collapseText(buf); title != "" {
				if _, ok := titles[current]; !ok {
					titles[current] = title
				}
			}
		}
		current, buf = "", ""
	}

	for {
		switch tt := z.Next(); tt {
		case html.ErrorToken:
			return titles
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			attrType, href := "", ""
			for {
				key, val, more := z.TagAttr()
				switch strings.ToLower(string(key)) {
				case "type", "epub:type":
					attrType = strings.ToLower(string(val))
				case "href":
					href = string(val)
				}
				if !more {
					break
				}
			}
			switch tag {
			case "nav":
				navDepth++
				if !inTOC && strings.Contains(attrType, "toc") {
					inTOC = true
				}
			case "a":
				if inTOC {
					if aDepth == 0 {
						finishLink()
						current = resolve(navPath, href)
					}
					aDepth++
				}
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			switch tag {
			case "a":
				if inTOC && aDepth > 0 {
					aDepth--
					if aDepth == 0 {
						finishLink()
					}
				}
			case "nav":
				if navDepth > 0 {
					navDepth--
				}
				if navDepth == 0 {
					inTOC = false
				}
			}
		case html.TextToken:
			if inTOC && aDepth > 0 {
				buf += string(z.Text())
			}
		}
	}
}

func parseNCX(data []byte, ncxPath string) map[string]string {
	titles := map[string]string{}
	var ncx struct {
		NavMap struct {
			Points []ncxPoint `xml:"navPoint"`
		} `xml:"navMap"`
	}
	if err := xml.Unmarshal(data, &ncx); err != nil {
		return titles
	}
	var walk func([]ncxPoint)
	walk = func(points []ncxPoint) {
		for _, p := range points {
			if p.Content.Src != "" {
				title := collapseText(p.Label.Text)
				if title != "" {
					key := resolve(ncxPath, p.Content.Src)
					if _, ok := titles[key]; !ok {
						titles[key] = title
					}
				}
			}
			walk(p.Points)
		}
	}
	walk(ncx.NavMap.Points)
	return titles
}

type ncxPoint struct {
	Label struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Points []ncxPoint `xml:"navPoint"`
}
