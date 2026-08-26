package obsidian

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestWriteZIPCreatesCategoryAndExplicitRelationGraph(t *testing.T) {
	docs := []Document{
		{Data: map[string]any{"id": "KB-1", "title": "VPN", "text": "Fehler", "answer": "Neu verbinden", "source": "internal-kb", "categories": []any{"Netzwerk > VPN"}, "keywords": []any{"vpn"}, "related_articles": []any{map[string]any{"id": "KB-2", "title": "Netzwerk"}}}, ModifiedAt: "2026-08-20"},
		{Data: map[string]any{"id": "KB-2", "title": "Netzwerk", "text": "Netz", "answer": "Pruefen", "source": "internal-kb"}, ModifiedAt: "2026-08-20"},
	}
	var buf bytes.Buffer
	if err := WriteZIP(&buf, docs, time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		files[f.Name] = string(b)
	}
	var vpn string
	for n, b := range files {
		if strings.Contains(n, "vpn--kb-1") {
			vpn = b
		}
	}
	if !strings.Contains(vpn, "[[Wiki/Categories/netzwerk-vpn|Netzwerk > VPN]]") {
		t.Fatalf("category wikilink missing:\n%s", vpn)
	}
	if !strings.Contains(vpn, "[[Wiki/Knowledge/netzwerk--kb-2|Netzwerk]]") {
		t.Fatalf("article relation missing:\n%s", vpn)
	}
	if !strings.Contains(files["Wiki/graph.json"], `"relation": "related_articles"`) {
		t.Fatalf("graph relation missing: %s", files["Wiki/graph.json"])
	}
}
