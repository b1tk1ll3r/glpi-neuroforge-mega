package research

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"neuroforge/internal/ingest"
)

func TestSearchUsesJSONAPIAndLimitsResults(t *testing.T) {
	var gotQuery, gotFormat, gotLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotFormat = r.URL.Query().Get("format")
		gotLang = r.URL.Query().Get("language")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"A","url":"https://example.com/a","content":"one"},{"title":"B","url":"https://example.com/b","content":"two"}]}`))
	}))
	defer srv.Close()
	out, err := Search(context.Background(), SearchConfig{BaseURL: srv.URL, Language: "de-DE", MaxResults: 1, Timeout: time.Second}, "NVIDIA CUDA")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "NVIDIA CUDA" || gotFormat != "json" || gotLang != "de-DE" {
		t.Fatalf("bad query q=%q format=%q lang=%q", gotQuery, gotFormat, gotLang)
	}
	if len(out) != 1 || out[0].Title != "A" {
		t.Fatalf("unexpected results %#v", out)
	}
}

func TestFetchPageBlocksPrivateTargets(t *testing.T) {
	_, err := FetchPage(context.Background(), FetchConfig{Timeout: time.Second}, "http://127.0.0.1:8080/private")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "private") {
		t.Fatalf("expected private target block, got %v", err)
	}
}

func TestFetchPageExtractsHTMLWhenPrivateExplicitlyAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>GPU page</title></head><body><p>CUDA fact.</p><script>ignore()</script></body></html>`))
	}))
	defer srv.Close()
	p, err := FetchPage(context.Background(), FetchConfig{Timeout: time.Second, AllowPrivateTargets: true, MaxBytes: 1 << 20, MaxChars: 1000}, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "GPU page" || !strings.Contains(p.Text, "CUDA fact") || strings.Contains(p.Text, "ignore()") {
		t.Fatalf("unexpected page %#v", p)
	}
}

func TestFetchResourceRecognizesAndReturnsDOCX(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body><w:p><w:r><w:t>CUDA document evidence.</w:t></w:r></w:p></w:body></w:document>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		w.Header().Set("Content-Disposition", `attachment; filename="cuda-paper.docx"`)
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	res, err := FetchResource(context.Background(), FetchConfig{Timeout: time.Second, AllowPrivateTargets: true, MaxBytes: 1 << 20, MaxDocumentBytes: 2 << 20}, srv.URL+"/download")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "document" || res.Filename != "cuda-paper.docx" || len(res.Data) == 0 {
		t.Fatalf("unexpected resource %#v", res)
	}
	text, _, err := ingest.ExtractText(res.Filename, res.ContentType, res.Data)
	if err != nil || !strings.Contains(text, "CUDA document evidence") {
		t.Fatalf("document extraction failed text=%q err=%v", text, err)
	}
}

func TestResultLooksLikeDocumentFromSearXNGFileFields(t *testing.T) {
	if !ResultLooksLikeDocument(Result{Template: "file.html", Filename: "paper.pdf", MIMEType: "application/pdf", URL: "https://example.org/download"}) {
		t.Fatal("expected SearXNG file result to be recognized as a document")
	}
	if ResultLooksLikeDocument(Result{Template: "default.html", URL: "https://example.org/article", MIMEType: "text/html"}) {
		t.Fatal("normal HTML result must not be classified as document")
	}
}

func TestFetchResourceUsesSearXNGFileHintsForGenericDownload(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body><w:p><w:r><w:t>Generic download document.</w:t></w:r></w:p></w:body></w:document>`))
	_ = zw.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()
	res, err := FetchResource(context.Background(), FetchConfig{Timeout: time.Second, AllowPrivateTargets: true, MaxDocumentBytes: 2 << 20, HintFilename: "paper.docx", HintMIMEType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, srv.URL+"/download?id=42")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "document" || res.Filename != "paper.docx" {
		t.Fatalf("SearXNG file hints were not applied: %#v", res)
	}
}
