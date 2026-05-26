package document

import (
	"strings"
	"unicode/utf8"
)

// SplitText 按段落和长度切分文本 chunk
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
