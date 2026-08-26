package ingest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	reScript  = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reStyle   = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	reComment = regexp.MustCompile(`(?is)<!--.*?-->`)
	reTags    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace   = regexp.MustCompile(`[ \t\x0b\f\r]+`)
	reBlank   = regexp.MustCompile(`\n{3,}`)
)

// ExtractText extracts useful plain text from common knowledge-document formats.
// PDF support uses the optional pdftotext executable when it is installed; all
// other supported formats use only the Go standard library.
func ExtractText(name, contentType string, data []byte) (string, string, error) {
	return ExtractTextContext(context.Background(), name, contentType, data)
}

func ExtractTextContext(ctx context.Context, name, contentType string, data []byte) (string, string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	ct, _, _ := mime.ParseMediaType(contentType)
	if ct == "" {
		ct = contentType
	}
	switch {
	case ext == ".html" || ext == ".htm" || ct == "text/html" || ct == "application/xhtml+xml":
		return HTMLToText(string(data)), "text/html", nil
	case ext == ".json" || ct == "application/json":
		var v any
		if json.Unmarshal(data, &v) == nil {
			b, _ := json.MarshalIndent(v, "", "  ")
			return cleanText(string(b)), "application/json", nil
		}
		return cleanText(string(data)), "application/json", nil
	case ext == ".txt" || ext == ".md" || ext == ".markdown" || ext == ".log" || ext == ".yaml" || ext == ".yml" || ext == ".csv" || ext == ".tsv" || strings.HasPrefix(ct, "text/"):
		return cleanText(string(data)), nonempty(ct, "text/plain"), nil
	case ext == ".docx" || ct == "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		t, err := extractDOCX(data)
		return t, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", err
	case ext == ".pdf" || ct == "application/pdf":
		t, err := extractPDF(ctx, data)
		return t, "application/pdf", err
	default:
		return "", ct, fmt.Errorf("unsupported document type %q (supported: txt, md, html, json, csv/tsv, docx, pdf with pdftotext)", ext)
	}
}

func HTMLToText(in string) string {
	s := reScript.ReplaceAllString(in, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reComment.ReplaceAllString(s, " ")
	// preserve rough block boundaries before removing tags.
	r := strings.NewReplacer("</p>", "\n\n", "</div>", "\n", "</li>", "\n", "<br>", "\n", "<br/>", "\n", "<br />", "\n", "</h1>", "\n\n", "</h2>", "\n\n", "</h3>", "\n\n")
	s = r.Replace(s)
	s = reTags.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return cleanText(s)
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(reSpace.ReplaceAllString(line, " "))
		if line != "" {
			out = append(out, line)
		} else if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}
	return strings.TrimSpace(reBlank.ReplaceAllString(strings.Join(out, "\n"), "\n\n"))
}

func extractDOCX(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	var doc *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			doc = f
			break
		}
	}
	if doc == nil {
		return "", errors.New("docx has no word/document.xml")
	}
	rc, err := doc.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	dec := xml.NewDecoder(io.LimitReader(rc, 64<<20))
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				var text string
				if err := dec.DecodeElement(&text, &t); err != nil {
					return "", err
				}
				b.WriteString(text)
			}
		case xml.EndElement:
			if t.Name.Local == "p" {
				b.WriteString("\n\n")
			} else if t.Name.Local == "tab" {
				b.WriteByte('\t')
			}
		}
	}
	return cleanText(b.String()), nil
}

func extractPDF(ctx context.Context, data []byte) (string, error) {
	path, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", errors.New("PDF extraction requires the optional 'pdftotext' executable (poppler-utils); install it or convert the PDF to text/HTML first")
	}
	cmd := exec.CommandContext(ctx, path, "-layout", "-", "-")
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	out := &cappedBuffer{limit: 64 << 20}
	cmd.Stdout = out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(out.err, errExtractedTextTooLarge) {
			return "", errExtractedTextTooLarge
		}
		return "", fmt.Errorf("pdftotext: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return cleanText(out.buf.String()), nil
}

var errExtractedTextTooLarge = errors.New("extracted document text exceeds 64 MiB safety limit")

type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
	err   error
}

func (w *cappedBuffer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.buf.Len()+len(p) > w.limit {
		w.err = errExtractedTextTooLarge
		return 0, w.err
	}
	return w.buf.Write(p)
}

func ChunkText(text string, chunkChars, overlap, maxChunks int) []string {
	text = cleanText(text)
	if text == "" {
		return nil
	}
	if chunkChars <= 0 {
		chunkChars = 2400
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkChars {
		overlap = chunkChars / 8
	}
	if maxChunks <= 0 {
		maxChunks = 2000
	}
	r := []rune(text)
	chunks := make([]string, 0, min(maxChunks, len(r)/chunkChars+1))
	for start := 0; start < len(r) && len(chunks) < maxChunks; {
		end := start + chunkChars
		if end >= len(r) {
			end = len(r)
		} else {
			// Try to end at a paragraph/sentence/space boundary without shrinking too much.
			floor := start + chunkChars*3/4
			for i := end; i > floor; i-- {
				if r[i-1] == '\n' || r[i-1] == '.' || r[i-1] == '!' || r[i-1] == '?' || unicode.IsSpace(r[i-1]) {
					end = i
					break
				}
			}
		}
		chunk := strings.TrimSpace(string(r[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end >= len(r) {
			break
		}
		next := end - overlap
		if next <= start {
			next = end
		}
		start = next
	}
	return chunks
}

func nonempty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
