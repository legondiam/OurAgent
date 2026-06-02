package document

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type Chunk struct {
	Content     string
	SectionPath string
	TokenCount  int
}

// SplitDocument 按文件类型切分文档
func SplitDocument(filename, text string, chunkSize, overlap int) []Chunk {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	if ext == "md" || ext == "markdown" {
		return splitMarkdown(text, chunkSize, overlap)
	}
	return wrapPlainChunks(SplitText(text, chunkSize, overlap), "")
}

// SplitText 按段落和长度切分文本chunk
func SplitText(text string, chunkSize, overlap int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkSize {
		overlap = chunkSize / 5
	}

	paragraphs := splitParagraphs(text)
	var chunks []string
	var current strings.Builder

	flush := func() {
		content := strings.TrimSpace(current.String())
		if content != "" {
			chunks = append(chunks, content)
		}
		current.Reset()
	}

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if runeLen(para) > chunkSize {
			flush()
			chunks = append(chunks, splitLongText(para, chunkSize, overlap)...)
			continue
		}
		nextLen := runeLen(current.String()) + runeLen(para) + 2
		if current.Len() > 0 && nextLen > chunkSize {
			flush()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
	}
	flush()

	if overlap == 0 || len(chunks) <= 1 {
		return chunks
	}
	return addOverlap(chunks, overlap, chunkSize)
}

// splitMarkdown 按Markdown标题层级切分文本
func splitMarkdown(text string, chunkSize, overlap int) []Chunk {
	lines := strings.Split(text, "\n")
	headings := make([]string, 0, 6)
	var section strings.Builder
	var sectionPath string
	var chunks []Chunk

	flush := func() {
		content := strings.TrimSpace(section.String())
		if content != "" {
			chunks = append(chunks, wrapPlainChunks(SplitText(content, chunkSize, overlap), sectionPath)...)
		}
		section.Reset()
	}

	for _, line := range lines {
		level, title, ok := parseMarkdownHeading(line)
		if ok {
			flush()
			headings = updateHeadingStack(headings, level, title)
			sectionPath = joinSectionPath(headings)
			section.WriteString(line)
			section.WriteString("\n")
			continue
		}
		section.WriteString(line)
		section.WriteString("\n")
	}
	flush()
	return chunks
}

// parseMarkdownHeading 解析Markdown标题行
func parseMarkdownHeading(line string) (int, string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return 0, "", false
	}
	title := strings.TrimSpace(trimmed[level:])
	if title == "" {
		return 0, "", false
	}
	return level, title, true
}

// updateHeadingStack 更新当前标题层级栈
func updateHeadingStack(headings []string, level int, title string) []string {
	if level <= 0 {
		return headings
	}
	if len(headings) >= level {
		headings = headings[:level-1]
	}
	for len(headings) < level-1 {
		headings = append(headings, "")
	}
	return append(headings, title)
}

// joinSectionPath 拼接标题层级路径
func joinSectionPath(headings []string) string {
	parts := make([]string, 0, len(headings))
	for _, heading := range headings {
		heading = strings.TrimSpace(heading)
		if heading != "" {
			parts = append(parts, heading)
		}
	}
	return strings.Join(parts, "/")
}

// wrapPlainChunks 包装普通文本切片
func wrapPlainChunks(contents []string, sectionPath string) []Chunk {
	chunks := make([]Chunk, 0, len(contents))
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		chunks = append(chunks, Chunk{
			Content:     content,
			SectionPath: sectionPath,
			TokenCount:  EstimateTokens(content),
		})
	}
	return chunks
}

func splitParagraphs(text string) []string {
	parts := strings.Split(text, "\n\n")
	if len(parts) > 1 {
		return parts
	}
	return strings.Split(text, "\n")
}

func splitLongText(text string, chunkSize, overlap int) []string {
	runes := []rune(text)
	var chunks []string
	step := chunkSize - overlap
	if step <= 0 {
		step = chunkSize
	}
	for start := 0; start < len(runes); start += step {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, strings.TrimSpace(string(runes[start:end])))
		if end == len(runes) {
			break
		}
	}
	return chunks
}

func addOverlap(chunks []string, overlap, chunkSize int) []string {
	result := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		if i == 0 {
			result = append(result, chunk)
			continue
		}
		prevSuffix := tailRunes(chunks[i-1], overlap)
		merged := strings.TrimSpace(prevSuffix + "\n" + chunk)
		if runeLen(merged) > chunkSize+overlap {
			merged = string([]rune(merged)[:chunkSize+overlap])
		}
		result = append(result, merged)
	}
	return result
}

func tailRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[len(runes)-n:])
}

// EstimateTokens 估算文本 token 数
func EstimateTokens(text string) int {
	runes := utf8.RuneCountInString(text)
	if runes == 0 {
		return 0
	}
	return (runes + 1) / 2
}

func runeLen(text string) int {
	return utf8.RuneCountInString(text)
}
