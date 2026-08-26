package ingest

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestExtractTextHTMLAndChunks(t *testing.T) {
	text, mime, err := ExtractText("page.html", "text/html", []byte(`<html><head><style>.x{}</style><script>alert(1)</script></head><body><h1>NVIDIA</h1><p>CUDA accelerates parallel workloads.</p></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if mime != "text/html" || !strings.Contains(text, "CUDA accelerates") || strings.Contains(text, "alert(1)") {
		t.Fatalf("unexpected extraction mime=%q text=%q", mime, text)
	}
	chunks := ChunkText(strings.Repeat("alpha beta gamma delta. ", 200), 240, 30, 10)
	if len(chunks) < 2 || len(chunks) > 10 {
		t.Fatalf("unexpected chunks=%d", len(chunks))
	}
}

func TestExtractDOCX(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body><w:p><w:r><w:t>First paragraph.</w:t></w:r></w:p><w:p><w:r><w:t>Second paragraph.</w:t></w:r></w:p></w:body></w:document>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	text, _, err := ExtractText("note.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "First paragraph") || !strings.Contains(text, "Second paragraph") {
		t.Fatalf("unexpected text %q", text)
	}
}
