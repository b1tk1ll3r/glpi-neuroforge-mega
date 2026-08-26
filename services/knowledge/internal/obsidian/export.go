package obsidian

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Document struct {
	Data       map[string]any
	ModifiedAt string
}

type relation struct {
	ID       string
	Title    string
	ItemType string
	URI      string
	Kind     string
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
type manifest struct {
	Format      string    `json:"format"`
	Version     int       `json:"version"`
	GeneratedAt time.Time `json:"generated_at"`
	Documents   int       `json:"documents"`
	Categories  int       `json:"categories"`
	Relations   int       `json:"relations"`
}

// WriteZIP exports canonical JSON knowledge as a self-contained Obsidian vault.
// Unknown JSON fields remain untouched in the source database; relation-like
// fields are interpreted only for export and never mutate the canonical data.
func WriteZIP(w io.Writer, docs []Document, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	docs = append([]Document(nil), docs...)
	sort.Slice(docs, func(i, j int) bool {
		return strings.ToLower(text(docs[i].Data, "title")) < strings.ToLower(text(docs[j].Data, "title"))
	})
	pageByID := map[string]string{}
	pageByTitle := map[string]string{}
	for _, d := range docs {
		id := text(d.Data, "id")
		title := text(d.Data, "title")
		p := "Wiki/Knowledge/" + pageFilename(title, id)
		if id != "" {
			pageByID[strings.ToLower(id)] = p
		}
		if title != "" {
			pageByTitle[strings.ToLower(title)] = p
		}
	}

	zw := zip.NewWriter(w)
	if err := writeFile(zw, "Wiki/Schema.md", schemaPage()); err != nil {
		return err
	}
	g := graph{}
	categoryPages := map[string]string{}
	categoryTitles := map[string]string{}
	relationStubs := map[string]relation{}
	var relationCount int
	for _, d := range docs {
		id := text(d.Data, "id")
		title := text(d.Data, "title")
		p := pageByID[strings.ToLower(id)]
		if p == "" {
			p = "Wiki/Knowledge/" + pageFilename(title, id)
		}
		g.Nodes = append(g.Nodes, graphNode{ID: id, Title: title, Type: "knowledge", Path: p})
		content, edges, cats, stubs := articlePage(d, p, pageByID, pageByTitle, now)
		g.Edges = append(g.Edges, edges...)
		relationCount += len(edges)
		for _, c := range cats {
			key := strings.ToLower(c)
			cp := "Wiki/Categories/" + pageFilename(c, "")
			categoryPages[key] = cp
			categoryTitles[key] = c
		}
		for k, v := range stubs {
			relationStubs[k] = v
		}
		if err := writeFile(zw, p, content); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(categoryPages))
	for k := range categoryPages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := categoryPages[k]
		title := categoryTitles[k]
		g.Nodes = append(g.Nodes, graphNode{ID: "category:" + k, Title: title, Type: "category", Path: p})
		if err := writeFile(zw, p, categoryPage(title, now)); err != nil {
			return err
		}
	}
	stubKeys := make([]string, 0, len(relationStubs))
	for k := range relationStubs {
		stubKeys = append(stubKeys, k)
	}
	sort.Strings(stubKeys)
	for _, k := range stubKeys {
		r := relationStubs[k]
		p := stubPath(r)
		g.Nodes = append(g.Nodes, graphNode{ID: k, Title: r.Title, Type: "entity", Path: p})
		if err := writeFile(zw, p, stubPage(r, now)); err != nil {
			return err
		}
	}
	if err := writeFile(zw, "Wiki/index.md", indexPage(docs, pageByID, now)); err != nil {
		return err
	}
	gb, _ := json.MarshalIndent(g, "", "  ")
	if err := writeFile(zw, "Wiki/graph.json", string(gb)+"\n"); err != nil {
		return err
	}
	mb, _ := json.MarshalIndent(manifest{Format: "glpi-neuroforge-obsidian", Version: 1, GeneratedAt: now.UTC(), Documents: len(docs), Categories: len(categoryPages), Relations: relationCount}, "", "  ")
	if err := writeFile(zw, "Wiki/.manifest.json", string(mb)+"\n"); err != nil {
		return err
	}
	return zw.Close()
}

func articlePage(d Document, page string, byID, byTitle map[string]string, now time.Time) (string, []graphEdge, []string, map[string]relation) {
	m := d.Data
	id := text(m, "id")
	title := text(m, "title")
	cats := stringsList(m["categories"])
	tags := stringsList(m["keywords"])
	rels := extractRelations(m)
	var b strings.Builder
	b.WriteString("---\n")
	front(&b, "type", "knowledge")
	front(&b, "title", title)
	front(&b, "id", id)
	front(&b, "source", text(m, "source"))
	front(&b, "source_uri", text(m, "source_uri"))
	front(&b, "language", text(m, "language"))
	front(&b, "communication_style", text(m, "communication_style"))
	front(&b, "created", isoDate(d.ModifiedAt, now))
	front(&b, "updated", isoDate(d.ModifiedAt, now))
	frontBoolAny(&b, "auto_reply", m["auto_reply"])
	frontNumberAny(&b, "min_score", m["min_score"])
	frontList(&b, "tags", tags)
	frontList(&b, "categories", cats)
	var resolved []string
	stubs := map[string]relation{}
	for _, r := range rels {
		target, _ := resolveRelation(r, byID, byTitle, stubs)
		if target != "" {
			resolved = append(resolved, "[["+trimMD(target)+"|"+r.Title+"]]")
		}
	}
	for _, c := range cats {
		resolved = append(resolved, "[[Wiki/Categories/"+trimMD(pageFilename(c, ""))+"|"+c+"]]")
	}
	frontList(&b, "related", resolved)
	b.WriteString("---\n\n# " + title + "\n\n")
	if v := strings.TrimSpace(text(m, "text")); v != "" {
		b.WriteString("## Kontext / Problem\n\n" + v + "\n\n")
	}
	if v := strings.TrimSpace(text(m, "answer")); v != "" {
		b.WriteString("## Lösung / Antwort\n\n" + v + "\n\n")
	}
	if len(cats) > 0 || len(rels) > 0 || text(m, "source_uri") != "" {
		b.WriteString("## Verknüpfungen\n\n")
	}
	var edges []graphEdge
	for _, c := range cats {
		cp := "Wiki/Categories/" + pageFilename(c, "")
		b.WriteString("- [[" + trimMD(cp) + "|" + c + "]] — Kategorie\n")
		edges = append(edges, graphEdge{From: id, To: "category:" + strings.ToLower(c), Relation: "category"})
	}
	for _, r := range rels {
		target, targetID := resolveRelation(r, byID, byTitle, stubs)
		if target == "" {
			continue
		}
		b.WriteString("- [[" + trimMD(target) + "|" + escapeLinkLabel(r.Title) + "]]")
		if r.ItemType != "" {
			b.WriteString(" — `" + r.ItemType + "`")
		}
		if r.URI != "" {
			b.WriteString(" · `" + strings.ReplaceAll(r.URI, "`", "") + "`")
		}
		b.WriteByte('\n')
		edges = append(edges, graphEdge{From: id, To: targetID, Relation: r.Kind})
	}
	if uri := text(m, "source_uri"); uri != "" {
		b.WriteString("- Quelle: `" + strings.ReplaceAll(uri, "`", "") + "`\n")
	}
	_ = page
	return b.String(), edges, cats, stubs
}

func extractRelations(m map[string]any) []relation {
	keys := []string{"linked_items", "relations", "related", "related_articles", "references", "links", "connections", "associations", "glpi_relations"}
	var out []relation
	seen := map[string]struct{}{}
	var add func(any, string)
	add = func(v any, kind string) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				add(e, kind)
			}
		case []string:
			for _, e := range x {
				add(e, kind)
			}
		case string:
			x = strings.TrimSpace(x)
			if x == "" {
				return
			}
			r := relation{ID: x, Title: x, Kind: kind}
			k := strings.ToLower(kind + "|" + x)
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				out = append(out, r)
			}
		case map[string]any:
			id := firstText(x, "id", "items_id", "item_id", "target_id", "knowledge_id")
			title := firstText(x, "title", "name", "label", "target_title")
			itemType := firstText(x, "item_type", "itemtype", "type")
			uri := firstText(x, "uri", "url", "source_uri", "href")
			relKind := firstText(x, "relation", "kind")
			if relKind == "" {
				relKind = kind
			}
			if title == "" {
				if itemType != "" && id != "" {
					title = itemType + " #" + id
				} else {
					title = id
				}
			}
			if id == "" {
				id = title
			}
			if id == "" {
				return
			}
			r := relation{ID: id, Title: title, ItemType: itemType, URI: uri, Kind: relKind}
			k := strings.ToLower(relKind + "|" + itemType + "|" + id)
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				out = append(out, r)
			}
		}
	}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			add(v, k)
		}
	}
	return out
}

func resolveRelation(r relation, byID, byTitle map[string]string, stubs map[string]relation) (string, string) {
	if p := byID[strings.ToLower(strings.TrimSpace(r.ID))]; p != "" {
		return p, r.ID
	}
	if p := byTitle[strings.ToLower(strings.TrimSpace(r.Title))]; p != "" {
		return p, r.ID
	}
	if strings.EqualFold(r.ItemType, "KnowbaseItem") {
		if p := byID[strings.ToLower("GLPI-KB-"+r.ID)]; p != "" {
			return p, "GLPI-KB-" + r.ID
		}
	}
	key := "relation:" + strings.ToLower(strings.TrimSpace(r.ItemType)) + ":" + strings.ToLower(strings.TrimSpace(r.ID))
	stubs[key] = r
	return stubPath(r), key
}
func stubPath(r relation) string {
	typ := slug(r.ItemType)
	if typ == "" {
		typ = "related"
	}
	return "Wiki/Relations/" + typ + "/" + pageFilename(r.Title, r.ID)
}
func stubPage(r relation, now time.Time) string {
	var b strings.Builder
	b.WriteString("---\n")
	front(&b, "type", "entity")
	front(&b, "entity_type", r.ItemType)
	front(&b, "title", r.Title)
	front(&b, "source", "relation")
	front(&b, "source_uri", r.URI)
	front(&b, "created", now.UTC().Format("2006-01-02"))
	front(&b, "updated", now.UTC().Format("2006-01-02"))
	b.WriteString("---\n\n# " + r.Title + "\n\nVerknüpftes Wissens- oder GLPI-Objekt.\n")
	return b.String()
}
func categoryPage(title string, now time.Time) string {
	var b strings.Builder
	b.WriteString("---\n")
	front(&b, "type", "entity")
	front(&b, "entity_type", "category")
	front(&b, "title", title)
	front(&b, "created", now.UTC().Format("2006-01-02"))
	front(&b, "updated", now.UTC().Format("2006-01-02"))
	b.WriteString("---\n\n# " + title + "\n\nKategorie der GLPI/NeuroForge-Wissensbasis.\n")
	return b.String()
}
func indexPage(docs []Document, pages map[string]string, now time.Time) string {
	var b strings.Builder
	b.WriteString("---\n")
	front(&b, "type", "overview")
	front(&b, "title", "Knowledge Index")
	front(&b, "created", now.UTC().Format("2006-01-02"))
	front(&b, "updated", now.UTC().Format("2006-01-02"))
	b.WriteString("---\n\n# Knowledge Index\n\n")
	for _, d := range docs {
		id := text(d.Data, "id")
		title := text(d.Data, "title")
		p := pages[strings.ToLower(id)]
		b.WriteString("- [[" + trimMD(p) + "|" + escapeLinkLabel(title) + "]] — `" + id + "`\n")
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

Obsidian-kompatibler Export nach llm-wiki-artigen Konventionen.

- Metadaten: YAML-Frontmatter
- Beziehungen: [[Wiki/Namespace/Page]]
- Datumswerte: ISO-8601 (YYYY-MM-DD)
- Knowledge-Seiten: type=knowledge
- Kategorien/GLPI-Objekte: type=entity
- Index: type=overview
- graph.json: maschinenlesbare Knoten und Kanten

Der Export ist read-only und enthält keine Zugangsdaten.
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
func text(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		switch x := v.(type) {
		case string:
			return strings.TrimSpace(x)
		case json.Number:
			return x.String()
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		case int:
			return strconv.Itoa(x)
		case int64:
			return strconv.FormatInt(x, 10)
		}
	}
	return ""
}
func firstText(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := text(m, k); v != "" {
			return v
		}
	}
	return ""
}
func stringsList(v any) []string {
	var out []string
	seen := map[string]struct{}{}
	var add func(any)
	add = func(x any) {
		switch y := x.(type) {
		case []any:
			for _, e := range y {
				add(e)
			}
		case []string:
			for _, e := range y {
				add(e)
			}
		case string:
			y = strings.TrimSpace(y)
			if y != "" {
				k := strings.ToLower(y)
				if _, ok := seen[k]; !ok {
					seen[k] = struct{}{}
					out = append(out, y)
				}
			}
		case json.Number:
			add(y.String())
		case float64:
			add(strconv.FormatFloat(y, 'f', -1, 64))
		}
	}
	add(v)
	sort.Strings(out)
	return out
}
func front(b *strings.Builder, k, v string) {
	if strings.TrimSpace(v) == "" {
		return
	}
	raw, _ := json.Marshal(strings.TrimSpace(v))
	b.WriteString(k + ": " + string(raw) + "\n")
}
func frontList(b *strings.Builder, k string, vs []string) {
	if len(vs) == 0 {
		return
	}
	b.WriteString(k + ":\n")
	for _, v := range vs {
		raw, _ := json.Marshal(v)
		b.WriteString("  - " + string(raw) + "\n")
	}
}
func frontBoolAny(b *strings.Builder, k string, v any) {
	switch x := v.(type) {
	case bool:
		b.WriteString(k + ": " + strconv.FormatBool(x) + "\n")
	case string:
		if x != "" {
			b.WriteString(k + ": " + strings.ToLower(x) + "\n")
		}
	}
}
func frontNumberAny(b *strings.Builder, k string, v any) {
	switch x := v.(type) {
	case json.Number:
		b.WriteString(k + ": " + x.String() + "\n")
	case float64:
		b.WriteString(k + ": " + strconv.FormatFloat(x, 'f', -1, 64) + "\n")
	case int:
		b.WriteString(k + ": " + strconv.Itoa(x) + "\n")
	case string:
		if x != "" {
			b.WriteString(k + ": " + x + "\n")
		}
	}
}
func pageFilename(title, id string) string {
	s := slug(title)
	if s == "" {
		s = "artikel"
	}
	sid := slug(id)
	if sid != "" && !strings.Contains(s, sid) {
		s += "--" + sid
	}
	return s + ".md"
}
func slug(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	dash := false
	for _, r := range v {
		var repl string
		switch r {
		case 'ä':
			repl = "ae"
		case 'ö':
			repl = "oe"
		case 'ü':
			repl = "ue"
		case 'ß':
			repl = "ss"
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
				dash = false
				continue
			}
			repl = "-"
		}
		for _, rr := range repl {
			if rr == '-' {
				if !dash && b.Len() > 0 {
					b.WriteByte('-')
					dash = true
				}
			} else {
				b.WriteRune(rr)
				dash = false
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
func trimMD(v string) string          { return strings.TrimSuffix(v, ".md") }
func escapeLinkLabel(v string) string { return strings.ReplaceAll(v, "]", "\\]") }
func isoDate(v string, fallback time.Time) string {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return fallback.UTC().Format("2006-01-02")
}
