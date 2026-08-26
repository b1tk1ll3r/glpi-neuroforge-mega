package obsidian

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/example/glpi-ai-agent/internal/model"
)

type manifest struct {
	Format      string    `json:"format"`
	Version     int       `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	Documents   int       `json:"documents"`
	Relations   int       `json:"relations"`
}

type graphNode struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
	Path  string `json:"path"`
}

type graphEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
}

type graph struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
}

// WriteZIP exports a snapshot of the live knowledge store as an Obsidian vault.
// It is deliberately read-only: no source files are modified by an export.
func WriteZIP(w io.Writer, docs []model.KnowledgeDoc, generatedAt time.Time) error {
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}
	docs = append([]model.KnowledgeDoc(nil), docs...)
	sort.Slice(docs, func(i, j int) bool {
		if strings.EqualFold(docs[i].Title, docs[j].Title) {
			return docs[i].ID < docs[j].ID
		}
		return strings.ToLower(docs[i].Title) < strings.ToLower(docs[j].Title)
	})

	pageByID := make(map[string]string, len(docs))
	for _, d := range docs {
		pageByID[d.ID] = "Wiki/Knowledge/" + pageFilename(d.Title, d.ID)
	}

	zw := zip.NewWriter(w)
	defer zw.Close()
	if err := writeFile(zw, "Wiki/Schema.md", schemaPage()); err != nil {
		return err
	}

	g := graph{}
	relationCount := 0
	stubPages := map[string]model.LinkedItem{}
	for _, d := range docs {
		p := pageByID[d.ID]
		g.Nodes = append(g.Nodes, graphNode{ID: d.ID, Title: d.Title, Type: "knowledge", Path: p})
		content, edges := articlePage(d, p, pageByID, generatedAt, stubPages)
		relationCount += len(edges)
		g.Edges = append(g.Edges, edges...)
		if err := writeFile(zw, p, content); err != nil {
			return err
		}
	}

	stubKeys := make([]string, 0, len(stubPages))
	for key := range stubPages {
		stubKeys = append(stubKeys, key)
	}
	sort.Strings(stubKeys)
	for _, key := range stubKeys {
		item := stubPages[key]
		p := glpiItemPath(item)
		title := linkedTitle(item)
		g.Nodes = append(g.Nodes, graphNode{ID: key, Title: title, Type: "entity", Path: p})
		if err := writeFile(zw, p, glpiEntityPage(item, generatedAt)); err != nil {
			return err
		}
	}

	if err := writeFile(zw, "Wiki/index.md", indexPage(docs, pageByID, generatedAt)); err != nil {
		return err
	}
	gb, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(zw, "Wiki/graph.json", string(gb)+"\n"); err != nil {
		return err
	}
	mb, err := json.MarshalIndent(manifest{Format: "glpi-neuroforge-obsidian", Version: 1, GeneratedAt: generatedAt.UTC(), Documents: len(docs), Relations: relationCount}, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(zw, "Wiki/.manifest.json", string(mb)+"\n")
}

func articlePage(d model.KnowledgeDoc, page string, pageByID map[string]string, now time.Time, stubs map[string]model.LinkedItem) (string, []graphEdge) {
	var b strings.Builder
	date := isoDate(d.SourceModifiedAt, now)
	b.WriteString("---\n")
	front(&b, "type", "knowledge")
	front(&b, "title", d.Title)
	front(&b, "id", d.ID)
	front(&b, "source", d.Source)
	front(&b, "source_uri", d.SourceURI)
	front(&b, "language", d.Language)
	front(&b, "communication_style", d.CommunicationStyle)
	front(&b, "created", date)
	front(&b, "updated", date)
	frontBool(&b, "auto_reply", d.AutoReply)
	frontFloat(&b, "min_score", d.MinScore)
	frontList(&b, "tags", d.Keywords)
	frontIntList(&b, "categories", d.Categories)
	frontIntList(&b, "glpi_kb_categories", d.SourceCategoryIDs)
	frontList(&b, "external_categories", d.ExternalCategories)
	if len(d.LinkedItems) > 0 {
		b.WriteString("related:\n")
		for _, item := range d.LinkedItems {
			target := relationTarget(item, pageByID, stubs)
			b.WriteString("  - ")
			b.WriteString(yamlQuote("[[" + trimMD(target) + "|" + linkedTitle(item) + "]]"))
			b.WriteByte('\n')
		}
	}
	b.WriteString("---\n\n")
	b.WriteString("# " + d.Title + "\n\n")
	if strings.TrimSpace(d.Text) != "" {
		b.WriteString("## Kontext / Problem\n\n" + strings.TrimSpace(d.Text) + "\n\n")
	}
	if strings.TrimSpace(d.Answer) != "" {
		b.WriteString("## Lösung / Antwort\n\n" + strings.TrimSpace(d.Answer) + "\n\n")
	}
	if len(d.LinkedItems) > 0 || d.SourceURI != "" {
		b.WriteString("## Verknüpfungen\n\n")
	}
	var edges []graphEdge
	for _, item := range d.LinkedItems {
		target := relationTarget(item, pageByID, stubs)
		b.WriteString("- [[" + trimMD(target) + "|" + escapeLinkLabel(linkedTitle(item)) + "]] — `" + item.ItemType + " #" + strconv.FormatInt(item.ID, 10) + "`\n")
		edges = append(edges, graphEdge{From: d.ID, To: relationID(item), Relation: "glpi-linked-item"})
	}
	if d.SourceURI != "" {
		b.WriteString("- Quelle: `" + strings.ReplaceAll(d.SourceURI, "`", "") + "`\n")
	}
	if d.AutoReplyDecision != "" {
		b.WriteString("\n## Governance\n\n")
		b.WriteString("- Auto-Reply: **" + strconv.FormatBool(d.AutoReply) + "**\n")
		b.WriteString("- Entscheidung: `" + strings.ReplaceAll(d.AutoReplyDecision, "`", "") + "`\n")
		if d.AutoReplyDetail != "" {
			b.WriteString("- Begründung: " + strings.TrimSpace(d.AutoReplyDetail) + "\n")
		}
	}
	_ = page
	return b.String(), edges
}

func relationTarget(item model.LinkedItem, pageByID map[string]string, stubs map[string]model.LinkedItem) string {
	if strings.EqualFold(item.ItemType, "KnowbaseItem") {
		if p, ok := pageByID["GLPI-KB-"+strconv.FormatInt(item.ID, 10)]; ok {
			return p
		}
	}
	key := relationID(item)
	stubs[key] = item
	return glpiItemPath(item)
}

func relationID(item model.LinkedItem) string {
	return "GLPI-" + safePart(item.ItemType) + "-" + strconv.FormatInt(item.ID, 10)
}

func glpiItemPath(item model.LinkedItem) string {
	return "Wiki/GLPI/" + safePart(item.ItemType) + "/" + pageFilename(linkedTitle(item), strconv.FormatInt(item.ID, 10))
}

func linkedTitle(item model.LinkedItem) string {
	if strings.TrimSpace(item.Name) != "" {
		return strings.TrimSpace(item.Name)
	}
	t := strings.TrimSpace(item.ItemType)
	if t == "" {
		t = "GLPI-Objekt"
	}
	return fmt.Sprintf("%s #%d", t, item.ID)
}

func glpiEntityPage(item model.LinkedItem, now time.Time) string {
	var b strings.Builder
	b.WriteString("---\n")
	front(&b, "type", "entity")
	front(&b, "entity_type", strings.ToLower(safePart(item.ItemType)))
	front(&b, "title", linkedTitle(item))
	front(&b, "source", "glpi")
	front(&b, "source_uri", fmt.Sprintf("glpi://%s/%d", item.ItemType, item.ID))
	front(&b, "created", now.UTC().Format("2006-01-02"))
	front(&b, "updated", now.UTC().Format("2006-01-02"))
	b.WriteString("---\n\n# " + linkedTitle(item) + "\n\n")
	b.WriteString("Von GLPI mit einem oder mehreren Knowledge-Base-Artikeln verknüpft.\n")
	return b.String()
}

func indexPage(docs []model.KnowledgeDoc, pages map[string]string, now time.Time) string {
	var b strings.Builder
	b.WriteString("---\n")
	front(&b, "type", "overview")
	front(&b, "title", "Knowledge Index")
	front(&b, "created", now.UTC().Format("2006-01-02"))
	front(&b, "updated", now.UTC().Format("2006-01-02"))
	b.WriteString("---\n\n# Knowledge Index\n\n")
	b.WriteString("Exportierte Artikel: **" + strconv.Itoa(len(docs)) + "**\n\n")
	for _, d := range docs {
		b.WriteString("- [[" + trimMD(pages[d.ID]) + "|" + escapeLinkLabel(d.Title) + "]] — `" + d.ID + "` · `" + d.Source + "`\n")
	}
	return b.String()
}

func schemaPage() string {
	return `---
type: meta
title: GLPI NeuroForge Wiki Schema
status: active
---

# Wiki Schema

Dieser Export ist für Obsidian und llm-wiki-artige Workflows ausgelegt.

## Seitentypen

- ` + "`knowledge`" + ` — Knowledge-Base-Artikel.
- ` + "`entity`" + ` — aus GLPI verknüpfte Objekte.
- ` + "`overview`" + ` — Indexseiten.
- ` + "`meta`" + ` — Schema- und Steuerseiten.

## Konventionen

- Metadaten stehen in YAML-Frontmatter.
- Interne Beziehungen verwenden ` + "`[[Wiki/...]]`" + `.
- Datumswerte verwenden ISO-8601 (` + "`YYYY-MM-DD`" + `).
- ` + "`source_uri`" + ` bewahrt die Herkunft; Secrets werden nicht exportiert.
- ` + "`graph.json`" + ` enthält dieselben expliziten Beziehungen maschinenlesbar.
`
}

func writeFile(zw *zip.Writer, name, content string) error {
	h := &zip.FileHeader{Name: path.Clean(name), Method: zip.Deflate}
	h.SetMode(0o644)
	f, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, bytes.NewBufferString(content))
	return err
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func pageFilename(title, id string) string {
	s := slug(title)
	if s == "" {
		s = "artikel"
	}
	i := slug(id)
	if i != "" && !strings.Contains(s, i) {
		s += "--" + i
	}
	return s + ".md"
}

func slug(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		switch r {
		case 'ä':
			b.WriteString("ae")
		case 'ö':
			b.WriteString("oe")
		case 'ü':
			b.WriteString("ue")
		case 'ß':
			b.WriteString("ss")
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			} else {
				b.WriteByte('-')
			}
		}
	}
	return strings.Trim(nonSlug.ReplaceAllString(b.String(), "-"), "-")
}

func safePart(v string) string {
	s := slug(v)
	if s == "" {
		return "item"
	}
	return s
}
func trimMD(v string) string          { return strings.TrimSuffix(v, ".md") }
func escapeLinkLabel(v string) string { return strings.ReplaceAll(v, "]", "\\]") }
func yamlQuote(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
func front(b *strings.Builder, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString(key + ": " + yamlQuote(strings.TrimSpace(value)) + "\n")
}
func frontBool(b *strings.Builder, key string, value bool) {
	b.WriteString(key + ": " + strconv.FormatBool(value) + "\n")
}
func frontFloat(b *strings.Builder, key string, value float64) {
	b.WriteString(key + ": " + strconv.FormatFloat(value, 'f', -1, 64) + "\n")
}
func frontList(b *strings.Builder, key string, values []string) {
	values = uniqueStrings(values)
	if len(values) == 0 {
		return
	}
	b.WriteString(key + ":\n")
	for _, v := range values {
		b.WriteString("  - " + yamlQuote(v) + "\n")
	}
}
func frontIntList(b *strings.Builder, key string, values []int64) {
	if len(values) == 0 {
		return
	}
	b.WriteString(key + ":\n")
	for _, v := range values {
		b.WriteString("  - " + strconv.FormatInt(v, 10) + "\n")
	}
}
func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		k := strings.ToLower(v)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func isoDate(v string, fallback time.Time) string {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return fallback.UTC().Format("2006-01-02")
}
