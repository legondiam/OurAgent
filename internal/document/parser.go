package document

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	einoparser "github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/schema"
	"github.com/ledongthuc/pdf"
)

// ParseFile 按文件类型解析文档文本
func ParseFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return ParseReader(context.Background(), filepath.Base(path), file)
}

// ParseReader 使用Eino Parser组件从数据流解析文档文本
func ParseReader(ctx context.Context, filename string, reader io.Reader) (string, error) {
	parser, err := einoparser.NewExtParser(ctx, &einoparser.ExtParserConfig{
		Parsers: map[string]einoparser.Parser{
			".txt": einoparser.TextParser{},
			".md":  einoparser.TextParser{},
			".pdf": pdfParser{},
		},
		FallbackParser: unsupportedParser{},
	})
	if err != nil {
		return "", err
	}

	docs, err := parser.Parse(ctx, reader, einoparser.WithURI(filename))
	if err != nil {
		return "", err
	}
	return mergeDocuments(docs), nil
}

type pdfParser struct{}

func (p pdfParser) Parse(_ context.Context, reader io.Reader, _ ...einoparser.Option) ([]*schema.Document, error) {
	text, err := parsePDFReader(reader)
	if err != nil {
		return nil, err
	}
	return []*schema.Document{{Content: text}}, nil
}

type unsupportedParser struct{}

func (p unsupportedParser) Parse(_ context.Context, _ io.Reader, _ ...einoparser.Option) ([]*schema.Document, error) {
	return nil, errors.New("文件类型不支持")
}

func parsePDFReader(reader io.Reader) (string, error) {
	raw, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	pdfReader, err := pdf.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", err
	}
	textReader, err := pdfReader.GetPlainText()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, textReader); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func mergeDocuments(docs []*schema.Document) string {
	contents := make([]string, 0, len(docs))
	for _, doc := range docs {
		if doc == nil || strings.TrimSpace(doc.Content) == "" {
			continue
		}
		contents = append(contents, doc.Content)
	}
	return strings.Join(contents, "\n")
}

// NormalizeText 规范化文档文本格式
func NormalizeText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned = append(cleaned, strings.TrimSpace(line))
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}
