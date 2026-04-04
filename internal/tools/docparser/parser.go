// Package docparser provides format-agnostic document text extraction.
//
// Supported formats (stdlib only — zero external dependencies):
//   - DOCX  — Office Open XML, extracts paragraphs and tables
//   - XLSX  — Office Open XML, extracts all sheets as tabular text
//   - PPTX  — Office Open XML, extracts slide text in order
//   - HTML  — strips tags, decodes entities
//   - CSV   — formats as aligned table
//   - PDF   — basic text extraction from digital PDFs (not OCR)
//   - TXT / MD / JSON / YAML / any text — read as-is
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
// Proprietary and confidential.
package docparser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/mkvinicius/jarv/internal/core/agent"
)

// ─────────────────────────────────────────────────────────────────────────────
// Public API
// ─────────────────────────────────────────────────────────────────────────────

// ParseFile reads a document at path and returns its text content.
// The output is plain UTF-8 text suitable for passing to an LLM.
func ParseFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("docparser: %w", err)
	}
	return ParseBytes(path, data)
}

// ParseBytes parses document bytes given a hint filename (used for extension detection).
func ParseBytes(name string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".docx":
		return parseDocx(data)
	case ".xlsx":
		return parseXlsx(data)
	case ".pptx":
		return parsePptx(data)
	case ".html", ".htm":
		return parseHTML(data), nil
	case ".csv":
		return parseCSV(data)
	case ".pdf":
		return parsePDF(data), nil
	default:
		// Treat as plain text: .txt .md .json .yaml .toml .xml .log etc.
		return string(data), nil
	}
}

// FormatForLLM wraps the extracted text in a context block ready for the LLM.
// filename is used for context only.
func FormatForLLM(filename, text string, maxChars int) string {
	if maxChars > 0 && len(text) > maxChars {
		text = text[:maxChars] + "\n\n[... conteúdo truncado após " + strconv.Itoa(maxChars) + " caracteres ...]"
	}
	var sb strings.Builder
	sb.WriteString("[Conteúdo extraído de: ")
	sb.WriteString(filepath.Base(filename))
	sb.WriteString("]\n")
	sb.WriteString(strings.Repeat("─", 40))
	sb.WriteString("\n")
	sb.WriteString(text)
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", 40))
	sb.WriteString("\n")
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Agent Tool implementation
// ─────────────────────────────────────────────────────────────────────────────

// Tool implements agent.Tool so the LLM can call parse_document automatically.
type Tool struct{}

func (t *Tool) Name() string { return "parse_document" }

func (t *Tool) Description() string {
	return "Lê e extrai o texto de um arquivo (PDF, DOCX, XLSX, PPTX, HTML, CSV, TXT, MD). " +
		"Use quando o usuário mencionar um arquivo local ou pedir para ler/analisar um documento."
}

func (t *Tool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Caminho absoluto ou relativo para o arquivo a ser lido",
			},
		},
		"required": []string{"path"},
	}
}

func (t *Tool) Execute(_ context.Context, args string) (string, error) {
	// Parse JSON args: {"path": "/some/file.pdf"}
	path := extractStringField(args, "path")
	if path == "" {
		return "", fmt.Errorf("parse_document: campo 'path' obrigatório")
	}

	text, err := ParseFile(path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Sprintf("Arquivo %q não contém texto extraível (pode ser escaneado ou protegido).", path), nil
	}
	return FormatForLLM(path, text, 32000), nil
}

// Ensure Tool implements agent.Tool at compile time.
var _ agent.Tool = (*Tool)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// DOCX — Office Open XML Word document
// ─────────────────────────────────────────────────────────────────────────────

func parseDocx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("docx: não é um arquivo ZIP válido: %w", err)
	}

	xmlData, err := readZipFile(zr, "word/document.xml")
	if err != nil {
		return "", fmt.Errorf("docx: word/document.xml não encontrado: %w", err)
	}

	return extractDocxText(xmlData), nil
}

func extractDocxText(data []byte) string {
	var sb strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(data))

	var inText bool
	var inParagraph bool

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p": // w:p — paragraph
				inParagraph = true
			case "t": // w:t — text run
				inText = true
			case "br": // w:br — line break
				sb.WriteString("\n")
			case "tab": // w:tab
				sb.WriteString("\t")
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p": // end paragraph
				if inParagraph {
					sb.WriteString("\n")
					inParagraph = false
				}
			case "t":
				inText = false
			case "tr": // table row end
				sb.WriteString("\n")
			case "tc": // table cell end
				sb.WriteString("\t")
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

// ─────────────────────────────────────────────────────────────────────────────
// XLSX — Office Open XML Spreadsheet
// ─────────────────────────────────────────────────────────────────────────────

func parseXlsx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("xlsx: não é um arquivo ZIP válido: %w", err)
	}

	// Build shared strings table
	sharedStrings := parseXlsxSharedStrings(zr)

	// Find all sheet files
	var sheetFiles []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheetFiles = append(sheetFiles, f.Name)
		}
	}
	sort.Strings(sheetFiles)

	var sb strings.Builder
	for _, sheetName := range sheetFiles {
		sheetData, err := readZipFile(zr, sheetName)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(sheetName, "xl/worksheets/"), ".xml")
		sb.WriteString("[Aba: ")
		sb.WriteString(name)
		sb.WriteString("]\n")
		sb.WriteString(extractXlsxSheet(sheetData, sharedStrings))
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String()), nil
}

func parseXlsxSharedStrings(zr *zip.Reader) []string {
	data, err := readZipFile(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil
	}

	var result []string
	dec := xml.NewDecoder(bytes.NewReader(data))
	var inT bool
	var current strings.Builder
	var inSI bool

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				current.Reset()
			case "t":
				if inSI {
					inT = true
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				result = append(result, current.String())
				inSI = false
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				current.Write(t)
			}
		}
	}
	return result
}

func extractXlsxSheet(data []byte, sharedStrings []string) string {
	var sb strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(data))

	var inV bool
	var cellType string // "s" = shared string, "n" = number, "" = number
	var rowCells []string
	var currentCell strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				rowCells = nil
			case "c": // cell
				cellType = ""
				for _, attr := range t.Attr {
					if attr.Name.Local == "t" {
						cellType = attr.Value
					}
				}
				currentCell.Reset()
			case "v":
				inV = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "c": // end cell — resolve value
				raw := currentCell.String()
				if cellType == "s" {
					// Shared string index
					idx, err := strconv.Atoi(raw)
					if err == nil && idx < len(sharedStrings) {
						raw = sharedStrings[idx]
					}
				}
				rowCells = append(rowCells, raw)
			case "row":
				sb.WriteString(strings.Join(rowCells, "\t"))
				sb.WriteString("\n")
			}
		case xml.CharData:
			if inV {
				currentCell.Write(t)
			}
		}
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// PPTX — Office Open XML Presentation
// ─────────────────────────────────────────────────────────────────────────────

func parsePptx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("pptx: não é um arquivo ZIP válido: %w", err)
	}

	// Collect slide files in order
	var slideFiles []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") &&
			!strings.Contains(f.Name, "_rels") {
			slideFiles = append(slideFiles, f.Name)
		}
	}
	// Sort by slide number
	sort.Slice(slideFiles, func(i, j int) bool {
		ni := extractSlideNumber(slideFiles[i])
		nj := extractSlideNumber(slideFiles[j])
		return ni < nj
	})

	var sb strings.Builder
	for idx, slidePath := range slideFiles {
		slideData, err := readZipFile(zr, slidePath)
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("[Slide %d]\n", idx+1))
		sb.WriteString(extractPptxSlideText(slideData))
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String()), nil
}

func extractPptxSlideText(data []byte) string {
	var sb strings.Builder
	dec := xml.NewDecoder(bytes.NewReader(data))
	var inT bool

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t": // a:t — text
				inT = true
			case "p": // a:p — paragraph
				// handled at end
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "p":
				sb.WriteString("\n")
			case "sp": // shape end
				sb.WriteString("\n")
			}
		case xml.CharData:
			if inT {
				sb.Write(t)
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

func extractSlideNumber(path string) int {
	// e.g. "ppt/slides/slide3.xml" → 3
	base := filepath.Base(path)
	base = strings.TrimPrefix(base, "slide")
	base = strings.TrimSuffix(base, ".xml")
	n, _ := strconv.Atoi(base)
	return n
}

// ─────────────────────────────────────────────────────────────────────────────
// HTML — tag stripping
// ─────────────────────────────────────────────────────────────────────────────

func parseHTML(data []byte) string {
	s := string(data)

	// Remove script and style blocks entirely
	s = removeHTMLBlock(s, "script")
	s = removeHTMLBlock(s, "style")

	// Replace block-level tags with newlines
	blockTags := []string{"p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote"}
	for _, tag := range blockTags {
		s = strings.ReplaceAll(s, "<"+tag, "\n<"+tag)
		s = strings.ReplaceAll(s, "</"+tag+">", "\n")
	}

	// Strip all remaining tags
	var result strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			result.WriteRune(r)
		}
	}

	// Decode common HTML entities
	text := result.String()
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&mdash;", "—")
	text = strings.ReplaceAll(text, "&ndash;", "–")

	return normalizeWhitespace(text)
}

func removeHTMLBlock(s, tag string) string {
	for {
		open := strings.Index(strings.ToLower(s), "<"+tag)
		if open < 0 {
			break
		}
		close := strings.Index(strings.ToLower(s[open:]), "</"+tag+">")
		if close < 0 {
			break
		}
		s = s[:open] + s[open+close+len("</"+tag+">"):]
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
// CSV — formatted as readable table
// ─────────────────────────────────────────────────────────────────────────────

func parseCSV(data []byte) (string, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.LazyQuotes = true
	r.TrimLeadingSpace = true

	records, err := r.ReadAll()
	if err != nil {
		// Fall back to treating it as plain text
		return string(data), nil
	}
	if len(records) == 0 {
		return "", nil
	}

	// Calculate column widths for alignment
	widths := make([]int, len(records[0]))
	for _, row := range records {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	var sb strings.Builder
	for rowIdx, row := range records {
		for i, cell := range row {
			if i > 0 {
				sb.WriteString(" | ")
			}
			w := 0
			if i < len(widths) {
				w = widths[i]
			}
			sb.WriteString(cell)
			sb.WriteString(strings.Repeat(" ", w-len(cell)))
		}
		sb.WriteString("\n")
		// Header separator after first row
		if rowIdx == 0 {
			for i, w := range widths {
				if i > 0 {
					sb.WriteString("-+-")
				}
				sb.WriteString(strings.Repeat("-", w))
			}
			sb.WriteString("\n")
		}
	}
	return sb.String(), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// PDF — basic text extraction for digital (non-scanned) PDFs
// ─────────────────────────────────────────────────────────────────────────────

// parsePDF extracts text from digital PDFs by scanning for text operators.
// Works for the majority of computer-generated PDFs. Returns empty string
// for scanned/image-only PDFs (use OCR for those).
func parsePDF(data []byte) string {
	var sb strings.Builder

	// Process each page's content streams
	// Strategy: find BT...ET blocks, extract text from Tj/TJ/Tf operators
	i := 0
	for i < len(data) {
		// Find begin-text marker
		btIdx := indexOfBytes(data[i:], []byte("BT"))
		if btIdx < 0 {
			break
		}
		btIdx += i

		// Find end-text marker
		etIdx := indexOfBytes(data[btIdx:], []byte("ET"))
		if etIdx < 0 {
			break
		}
		etIdx += btIdx

		block := data[btIdx : etIdx+2]
		sb.WriteString(extractPDFTextBlock(block))
		sb.WriteString(" ")
		i = etIdx + 2
	}

	text := sb.String()
	if strings.TrimSpace(text) == "" {
		return "[PDF sem texto extraível — pode ser um documento escaneado. Use Docling para OCR.]"
	}
	return normalizeWhitespace(text)
}

func extractPDFTextBlock(block []byte) string {
	var sb strings.Builder
	s := string(block)
	i := 0

	for i < len(s) {
		// Look for string literals: (text)Tj  or  (text)Tj with positioning
		if s[i] == '(' {
			end, text := readPDFString(s, i)
			if end > i {
				sb.WriteString(text)
				i = end
				continue
			}
		}
		// Hex strings: <hex digits>
		if s[i] == '<' && i+1 < len(s) && isHexChar(s[i+1]) {
			end, text := readPDFHexString(s, i)
			if end > i {
				sb.WriteString(text)
				i = end
				continue
			}
		}
		// Td/TD/T* operators = new line
		if i+2 <= len(s) {
			op := s[i : i+2]
			if op == "Td" || op == "TD" || op == "T*" {
				sb.WriteString("\n")
			}
		}
		i++
	}
	return sb.String()
}

func readPDFString(s string, start int) (int, string) {
	// s[start] == '('
	var text strings.Builder
	depth := 0
	i := start
	for i < len(s) {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			// Escape sequence
			switch s[i+1] {
			case 'n':
				text.WriteByte('\n')
			case 'r':
				text.WriteByte('\r')
			case 't':
				text.WriteByte('\t')
			default:
				text.WriteByte(s[i+1])
			}
			i += 2
			continue
		}
		if c == '(' {
			depth++
			if depth > 1 {
				text.WriteByte(c)
			}
		} else if c == ')' {
			depth--
			if depth == 0 {
				return i + 1, text.String()
			}
			text.WriteByte(c)
		} else if depth > 0 {
			if c >= 0x20 && c < 0x80 {
				text.WriteByte(c)
			} else if c == 0 {
				// skip null bytes (common in UTF-16 encoded PDFs)
			} else {
				text.WriteByte(c)
			}
		}
		i++
	}
	return start, ""
}

func readPDFHexString(s string, start int) (int, string) {
	end := strings.IndexByte(s[start:], '>')
	if end < 0 {
		return start, ""
	}
	hex := s[start+1 : start+end]
	hex = strings.ReplaceAll(hex, " ", "")
	hex = strings.ReplaceAll(hex, "\n", "")

	var text strings.Builder
	for i := 0; i+1 < len(hex); i += 2 {
		hi := hexVal(hex[i])
		lo := hexVal(hex[i+1])
		if hi < 0 || lo < 0 {
			continue
		}
		b := byte(hi<<4 | lo)
		if b >= 0x20 && b < 0x80 {
			text.WriteByte(b)
		}
	}
	return start + end + 1, text.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			var buf bytes.Buffer
			_, err = buf.ReadFrom(rc)
			return buf.Bytes(), err
		}
	}
	return nil, fmt.Errorf("%q não encontrado no arquivo", name)
}

func normalizeWhitespace(s string) string {
	var sb strings.Builder
	prevSpace := false
	prevNewline := false
	newlineCount := 0

	for _, r := range s {
		if r == '\n' || r == '\r' {
			newlineCount++
			if newlineCount <= 2 {
				sb.WriteRune('\n')
			}
			prevSpace = false
			prevNewline = true
			continue
		}
		if unicode.IsSpace(r) {
			if !prevSpace && !prevNewline {
				sb.WriteRune(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		prevNewline = false
		newlineCount = 0
		sb.WriteRune(r)
	}
	return strings.TrimSpace(sb.String())
}

func indexOfBytes(data, needle []byte) int {
	if len(needle) == 0 || len(data) < len(needle) {
		return -1
	}
	for i := 0; i <= len(data)-len(needle); i++ {
		if bytes.Equal(data[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

func isHexChar(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// extractStringField pulls a JSON string field value from a raw JSON string.
// Avoids importing encoding/json for a trivial key lookup.
func extractStringField(jsonStr, key string) string {
	needle := `"` + key + `"`
	idx := strings.Index(jsonStr, needle)
	if idx < 0 {
		return ""
	}
	rest := jsonStr[idx+len(needle):]
	// skip whitespace and colon
	rest = strings.TrimSpace(rest)
	if len(rest) == 0 || rest[0] != ':' {
		return ""
	}
	rest = strings.TrimSpace(rest[1:])
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	var val strings.Builder
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == '\\' && i+1 < len(rest) {
			switch rest[i+1] {
			case '"':
				val.WriteByte('"')
			case '\\':
				val.WriteByte('\\')
			case 'n':
				val.WriteByte('\n')
			case 't':
				val.WriteByte('\t')
			default:
				val.WriteByte(rest[i+1])
			}
			i++
			continue
		}
		if c == '"' {
			break
		}
		val.WriteByte(c)
	}
	return val.String()
}
