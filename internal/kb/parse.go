package kb

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
)

// ParsedDocument is the plain-text form of a source file.
type ParsedDocument struct {
	Title string
	Text  string
	Mime  string
	// SkippedReason is set when the file type is recognized but yields no
	// text, so the UI can explain the failure instead of showing an empty doc.
	SkippedReason string
}

// SupportedExtensions lists the file types HitK can ingest.
var SupportedExtensions = []string{
	".md", ".markdown", ".mdx", ".txt", ".text", ".log", ".rst",
	".pdf", ".docx", ".csv", ".tsv", ".json", ".jsonl", ".yaml", ".yml",
	".html", ".htm", ".xml",
	".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".java", ".kt", ".rs", ".rb",
	".php", ".c", ".h", ".cc", ".cpp", ".hpp", ".cs", ".swift", ".m", ".mm",
	".sh", ".bash", ".zsh", ".ps1", ".sql", ".toml", ".ini", ".cfg", ".env",
	".vue", ".svelte", ".lua", ".r", ".scala", ".dart", ".ex", ".exs",
}

// extSet is the lookup table built from SupportedExtensions.
var extSet = func() map[string]bool {
	m := make(map[string]bool, len(SupportedExtensions))
	for _, e := range SupportedExtensions {
		m[e] = true
	}
	return m
}()

// Supported reports whether a path has an ingesable extension. Extensionless
// files are accepted because README and Makefile carry no suffix.
func Supported(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return true
	}
	return extSet[ext]
}

// ParseFile reads a file from disk and extracts its text.
func ParseFile(path string) (*ParsedDocument, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return parsePDF(path)
	case ".docx":
		return parseDocx(path)
	case ".csv", ".tsv":
		return parseDelimited(path, ext)
	case ".html", ".htm":
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return &ParsedDocument{
			Title: stem(path),
			Text:  StripHTML(string(raw)),
			Mime:  "text/html",
		}, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// A file that is overwhelmingly NUL bytes is binary in disguise; ingesting
	// it would poison the index with noise.
	if looksBinary(raw) {
		return &ParsedDocument{
			Title:         stem(path),
			SkippedReason: "二进制文件，已跳过",
			Mime:          "application/octet-stream",
		}, nil
	}
	text := string(raw)
	// Strip a UTF-8 BOM so the first heading still parses as markdown.
	text = strings.TrimPrefix(text, "\ufeff")

	return &ParsedDocument{Title: stem(path), Text: normalize(text), Mime: mimeFor(ext)}, nil
}

// ParseText ingests a raw string (pasted content or fetched web page).
func ParseText(title, text string) *ParsedDocument {
	return &ParsedDocument{Title: title, Text: normalize(text), Mime: "text/plain"}
}

// ParseHTMLPage ingests a fetched web page.
func ParseHTMLPage(url, html string) *ParsedDocument {
	title := url
	if m := titleRe.FindStringSubmatch(html); len(m) > 1 {
		if t := strings.TrimSpace(StripHTML(m[1])); t != "" {
			title = t
		}
	}
	return &ParsedDocument{Title: title, Text: normalize(StripHTML(html)), Mime: "text/html"}
}

// ---------------------------------------------------------------------------
// Format handlers
// ---------------------------------------------------------------------------

// parsePDF extracts text with a per-page panic guard: the PDF reader is
// lenient about malformed files and occasionally panics on damaged xref
// tables, which must not take the whole import down.
func parsePDF(path string) (doc *ParsedDocument, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc = &ParsedDocument{
				Title:         stem(path),
				SkippedReason: fmt.Sprintf("PDF 解析失败: %v", r),
				Mime:          "application/pdf",
			}
			err = nil
		}
	}()

	f, r, err := pdf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()

	var b strings.Builder
	total := r.NumPage()
	for i := 1; i <= total; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			// One unreadable page should not discard the readable ones.
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		fmt.Fprintf(&b, "<!-- page %d -->\n", i)
		b.WriteString(text)
		b.WriteString("\n\n")
	}

	if strings.TrimSpace(b.String()) == "" {
		return &ParsedDocument{
			Title:         stem(path),
			SkippedReason: "PDF 中未提取到文本（可能是扫描件，需 OCR）",
			Mime:          "application/pdf",
		}, nil
	}
	return &ParsedDocument{Title: stem(path), Text: normalize(b.String()), Mime: "application/pdf"}, nil
}

// parseDocx reads word/document.xml out of the OOXML zip and converts the
// paragraph and table markup to markdown-ish text.
func parseDocx(path string) (*ParsedDocument, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open docx: %w", err)
	}
	defer zr.Close()

	var body string
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		body = docxToText(data)
		break
	}
	if strings.TrimSpace(body) == "" {
		return &ParsedDocument{
			Title:         stem(path),
			SkippedReason: "docx 中未找到正文",
			Mime:          "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		}, nil
	}
	return &ParsedDocument{
		Title: stem(path),
		Text:  normalize(body),
		Mime:  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}, nil
}

// docxToText walks the OOXML body, emitting one line per paragraph.
func docxToText(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false

	var (
		b        strings.Builder
		inText   bool
		paraSeen bool
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "p":
				paraSeen = false
			case "tab":
				b.WriteString("\t")
			case "br":
				b.WriteString("\n")
			}
		case xml.CharData:
			if inText {
				txt := string(t)
				// Skip the leading whitespace of a paragraph continuation.
				if !paraSeen {
					txt = strings.TrimLeft(txt, " \t")
				}
				b.WriteString(txt)
				if txt != "" {
					paraSeen = true
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				b.WriteString("\n")
				paraSeen = false
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// parseDelimited converts CSV/TSV to a markdown table when the shape allows,
// falling back to pipe-joined rows for ragged or very wide data.
func parseDelimited(path, ext string) (*ParsedDocument, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	if ext == ".tsv" {
		r.Comma = '\t'
	}

	const (
		maxRows = 5000
		maxCols = 20
	)
	var (
		rows [][]string
		wide bool
	)
	for len(rows) < maxRows {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if len(rec) > maxCols {
			wide = true
			break
		}
		rows = append(rows, rec)
	}
	if len(rows) == 0 {
		return &ParsedDocument{Title: stem(path), SkippedReason: "表格为空", Mime: "text/csv"}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", stem(path))
	if wide {
		// Too many columns for a readable table; one record per line keeps the
		// data searchable without pretending to be tabular.
		for _, row := range rows {
			b.WriteString(strings.Join(row, " | "))
			b.WriteString("\n")
		}
		return &ParsedDocument{Title: stem(path), Text: normalize(b.String()), Mime: "text/csv"}, nil
	}

	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	writeRow := func(row []string, header bool) {
		cell := func(i int) string {
			if i < len(row) {
				return strings.ReplaceAll(strings.TrimSpace(row[i]), "|", "\\|")
			}
			return ""
		}
		b.WriteString("|")
		for i := 0; i < width; i++ {
			b.WriteString(" ")
			b.WriteString(cell(i))
			b.WriteString(" |")
		}
		b.WriteString("\n")
		if header {
			b.WriteString("|")
			for i := 0; i < width; i++ {
				b.WriteString(" --- |")
			}
			b.WriteString("\n")
		}
	}
	writeRow(rows[0], true)
	for _, row := range rows[1:] {
		writeRow(row, false)
	}
	return &ParsedDocument{Title: stem(path), Text: normalize(b.String()), Mime: "text/csv"}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var (
	// Go's regexp engine (RE2) has no backreferences, so the "match a tag and
	// its own closing tag" pattern cannot be written as one expression. The
	// four element types that must be dropped wholesale get one pattern each.
	scriptRe = regexp.MustCompile(`(?is)<script[^>]*>.*?</script\s*>`)
	styleRe  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style\s*>`)
	noscript = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript\s*>`)
	svgRe    = regexp.MustCompile(`(?is)<svg[^>]*>.*?</svg\s*>`)
	tagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	titleRe  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	blankRe  = regexp.MustCompile(`\n{3,}`)
	entityRe = regexp.MustCompile(`&[a-zA-Z#0-9]{2,8};`)
)

// StripHTML converts HTML to readable text, preserving block boundaries as
// newlines so chunking still sees paragraph structure.
func StripHTML(html string) string {
	s := scriptRe.ReplaceAllString(html, " ")
	s = styleRe.ReplaceAllString(s, " ")
	s = noscript.ReplaceAllString(s, " ")
	s = svgRe.ReplaceAllString(s, " ")
	for _, pair := range []struct{ open, close string }{
		{"</p>", "\n\n"}, {"</div>", "\n"}, {"</li>", "\n"},
		{"</h1>", "\n\n"}, {"</h2>", "\n\n"}, {"</h3>", "\n\n"},
		{"</h4>", "\n\n"}, {"</h5>", "\n\n"}, {"</h6>", "\n\n"},
		{"<br>", "\n"}, {"<br/>", "\n"}, {"<br />", "\n"},
		{"</tr>", "\n"}, {"</td>", " | "}, {"</table>", "\n"},
	} {
		s = strings.ReplaceAll(s, pair.open, pair.close)
		s = strings.ReplaceAll(strings.ToLower(s), pair.open, pair.close)
	}
	s = tagRe.ReplaceAllString(s, " ")
	s = entityRe.ReplaceAllStringFunc(s, func(e string) string {
		switch strings.ToLower(e) {
		case "&nbsp;":
			return " "
		case "&amp;":
			return "&"
		case "&lt;":
			return "<"
		case "&gt;":
			return ">"
		case "&quot;":
			return `"`
		case "&#39;", "&apos;":
			return "'"
		}
		return e
	})
	s = collapseSpaces(s)
	return strings.TrimSpace(s)
}

// collapseSpaces squeezes runs of spaces and newlines without rebuilding the
// whole string character by character.
func collapseSpaces(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	return strings.TrimSpace(blankRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// normalize applies the whitespace and encoding cleanup every ingest path needs.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "\u3000", " ")
	return strings.TrimSpace(blankRe.ReplaceAllString(s, "\n\n"))
}

// looksBinary reports whether data contains a NUL byte in its first 8 KiB.
func looksBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}

// stem returns a filename without its extension.
func stem(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// mimeFor maps an extension to a rough MIME type for display.
func mimeFor(ext string) string {
	switch ext {
	case ".json", ".jsonl":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".md", ".markdown", ".mdx":
		return "text/markdown"
	case ".xml":
		return "application/xml"
	}
	return "text/plain"
}
