package obsidian

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

func TestWriteZIPPreservesGLPILinksAsWikilinks(t *testing.T) {
	docs := []model.KnowledgeDoc{
		{ID: "GLPI-KB-12", Title: "VPN Hilfe", Text: "VPN Fehler", Answer: "Neu verbinden", Source: "glpi-kb", SourceURI: "glpi://KnowbaseItem/12", SourceModifiedAt: "2026-08-20 10:00:00", LinkedItems: []model.LinkedItem{{ItemType: "Computer", ID: 42, Name: "NB-042"}, {ItemType: "KnowbaseItem", ID: 13, Name: "Netzwerk"}}},
		{ID: "GLPI-KB-13", Title: "Netzwerk", Text: "Netzwerk", Answer: "Pruefen", Source: "glpi-kb"},
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
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = string(b)
	}
	var vpn string
	for name, body := range files {
		if strings.Contains(name, "vpn-hilfe") {
			vpn = body
		}
	}
	if vpn == "" {
		t.Fatalf("VPN article missing: %v", keys(files))
	}
	if !strings.Contains(vpn, "type: \"knowledge\"") || !strings.Contains(vpn, "[[Wiki/GLPI/computer/") || !strings.Contains(vpn, "|NB-042]]") {
		t.Fatalf("missing Obsidian metadata/relation:\n%s", vpn)
	}
	if !strings.Contains(vpn, "[[Wiki/Knowledge/netzwerk--glpi-kb-13|Netzwerk]]") {
		t.Fatalf("linked KB article did not resolve to article page:\n%s", vpn)
	}
	if _, ok := files["Wiki/Schema.md"]; !ok {
		t.Fatal("schema missing")
	}
	if _, ok := files["Wiki/graph.json"]; !ok {
		t.Fatal("graph missing")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
