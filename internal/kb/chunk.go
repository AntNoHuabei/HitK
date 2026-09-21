package kb

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Chunking parameters. The defaults are tuned for mixed Chinese/English prose:
// a Chinese chunk of 900 runes carries roughly the same information as ~600
// English words, which is a comfortable retrieval unit for every model in the
// preset catalog.
const (
	DefaultChunkRunes = 900
	DefaultOverlap    = 120
	// minChunkRunes prevents a trailing fragment from becoming its own chunk;
	// it is merged into the previous one instead.
	minChunkRunes = 200
)

// headingRe matches markdown ATX headings.
var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)

// Chunker splits documents into overlapping retrieval units.
type Chunker struct {
	Size    int
	Overlap int
}

// NewChunker builds a chunker, clamping the overlap below the size so the
// walker can always make forward progress.
func NewChunker(size, overlap int) *Chunker {
	if size <= 0 {
		size = DefaultChunkRunes
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= size/2 {
		overlap = size / 4
	}
	return &Chunker{Size: size, Overlap: overlap}
}

// block is a paragraph-sized unit of source text plus the heading it sits under.
type block struct {
	text    string
	heading string
	// hard marks a block that must not be merged with its neighbour, used for
	// fenced code blocks so a chunk never mixes two languages.
	hard bool
}

// Split documents the chunking policy for one text body.
func (c *Chunker) Split(text string, baseHeading string) []Chunk {
	blocks := c.blocks(text, baseHeading)
	if len(blocks) == 0 {
		return nil
	}

	var (
		out     []Chunk
		cur     strings.Builder
		heading string
		ordinal int
	)

	flush := func() {
		body := strings.TrimSpace(cur.String())
		if body == "" {
			cur.Reset()
			return
		}
		out = append(out, Chunk{
			Ordinal:    ordinal,
			Text:       body,
			Heading:    heading,
			TokenCount: EstimateTokens(body),
		})
		ordinal++
		cur.Reset()
	}

	for _, b := range blocks {
		// A block always knows which heading it sits under; when that differs
		// from the heading of the chunk being built, the chunk has straddled a
		// section boundary.
		h := b.heading
		if h == "" {
			h = heading
		}

		buffered := utf8.RuneCountInString(cur.String())
		needSplit := cur.Len() > 0 &&
			((h != heading && buffered >= minChunkRunes) ||
				cur.Len()+len(b.text) > c.Size ||
				(b.hard && cur.Len() > c.Size/2))

		if needSplit {
			tail := tailRunes(cur.String(), c.Overlap)
			flush()
			if tail != "" {
				cur.WriteString(tail)
				cur.WriteString("\n")
			}
			heading = h
		} else if cur.Len() == 0 || h != heading {
			// Either the buffer is empty, or it is too small to justify its own
			// chunk. In both cases the content being appended is dominated by
			// the new heading, so adopt it.
			heading = h
		}

		cur.WriteString(b.text)
		cur.WriteString("\n")
	}
	flush()

	// A tiny trailing chunk is a retrieval liability: it rarely stands alone
	// and it inflates the index. Fold it into its predecessor — but only when
	// both sit under the same heading, because merging across a section
	// boundary would relabel the content with the wrong section.
	if n := len(out); n >= 2 && utf8.RuneCountInString(out[n-1].Text) < minChunkRunes {
		sameSection := out[n-1].Heading == out[n-2].Heading || out[n-1].Heading == ""
		if sameSection {
			merged := out[n-2].Text + "\n" + out[n-1].Text
			out[n-2].Text = merged
			out[n-2].TokenCount = EstimateTokens(merged)
			out = out[:n-1]
		}
	}
	return out
}

// blocks splits text into paragraphs while tracking markdown headings and
// keeping fenced code blocks intact.
func (c *Chunker) blocks(text string, baseHeading string) []block {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	var (
		out       []block
		buf       []string
		heading   = baseHeading
		inFence   bool
		fenceLang string
		fenceBuf  []string
	)

	flushPara := func() {
		body := strings.TrimSpace(strings.Join(buf, "\n"))
		buf = buf[:0]
		if body == "" {
			return
		}
		out = append(out, block{text: body, heading: heading})
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				flushPara()
				inFence = true
				fenceLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				fenceBuf = []string{line}
				continue
			}
			fenceBuf = append(fenceBuf, line)
			body := strings.Join(fenceBuf, "\n")
			fenceBuf = nil
			inFence = false
			// Code is kept as one hard block: splitting it mid-function would
			// produce chunks the model cannot reason about.
			if utf8.RuneCountInString(body) <= c.Size*2 {
				out = append(out, block{text: body, heading: heading + langSuffix(fenceLang), hard: true})
			} else {
				for _, part := range forceSplit(body, c.Size) {
					out = append(out, block{text: part, heading: heading + langSuffix(fenceLang), hard: true})
				}
			}
			fenceLang = ""
			continue
		}
		if inFence {
			fenceBuf = append(fenceBuf, line)
			continue
		}

		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			flushPara()
			heading = strings.TrimSpace(m[2])
			continue
		}
		if trimmed == "" {
			flushPara()
			continue
		}
		buf = append(buf, line)
	}
	if inFence && len(fenceBuf) > 0 {
		out = append(out, block{text: strings.Join(fenceBuf, "\n"), heading: heading, hard: true})
	}
	flushPara()
	return out
}

func langSuffix(lang string) string {
	if lang == "" {
		return ""
	}
	return " · " + lang
}

// forceSplit hard-splits oversized text on rune boundaries.
func forceSplit(s string, size int) []string {
	runes := []rune(s)
	if len(runes) <= size {
		return []string{s}
	}
	var out []string
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

// tailRunes returns the last n runes of s, snapped forward to the next newline
// so the overlap never begins mid-sentence.
func tailRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return strings.TrimSpace(s)
	}
	tail := string(runes[len(runes)-n:])
	if idx := strings.IndexByte(tail, '\n'); idx >= 0 && idx+1 < len(tail) {
		tail = tail[idx+1:]
	}
	return strings.TrimSpace(tail)
}

// EstimateTokens approximates token count for budgeting. CJK characters are
// roughly one token each and Latin text roughly one token per four characters,
// which is close enough for context-window accounting.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	var cjk, other int
	for _, r := range s {
		if isCJKRune(r) {
			cjk++
		} else {
			other++
		}
	}
	n := cjk + (other+3)/4
	if n == 0 {
		n = 1
	}
	return n
}

func isCJKRune(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF, r >= 0x3400 && r <= 0x4DBF, r >= 0xF900 && r <= 0xFAFF:
		return true
	case r >= 0x3040 && r <= 0x30FF, r >= 0xAC00 && r <= 0xD7AF:
		return true
	}
	return false
}
