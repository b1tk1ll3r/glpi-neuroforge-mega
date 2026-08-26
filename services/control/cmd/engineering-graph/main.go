package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type node struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Label     string         `json:"label"`
	Group     string         `json:"group,omitempty"`
	Community string         `json:"community,omitempty"`
	Status    string         `json:"status,omitempty"`
	Score     float64        `json:"score,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}
type edge struct {
	ID     string         `json:"id"`
	From   string         `json:"from"`
	To     string         `json:"to"`
	Kind   string         `json:"kind"`
	Label  string         `json:"label,omitempty"`
	Status string         `json:"status,omitempty"`
	Weight float64        `json:"weight,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}
type graph struct {
	Scope string         `json:"scope"`
	Title string         `json:"title"`
	Nodes []node         `json:"nodes"`
	Edges []edge         `json:"edges"`
	Meta  map[string]any `json:"meta,omitempty"`
}

type module struct{ Dir, Name, Component string }
type parsedFile struct {
	Path, Rel, PackageID, PackageName, Component string
	File                                         *ast.File
	Fset                                         *token.FileSet
	Imports                                      map[string]string
}

type builder struct {
	root          string
	g             graph
	nodes         map[string]bool
	edges         map[string]bool
	funcByPkgName map[string]string
	files         []parsedFile
}

func main() {
	root := flag.String("root", "../..", "repository root")
	out := flag.String("out", "engineering-graph.json", "output JSON")
	flag.Parse()
	abs, err := filepath.Abs(*root)
	if err != nil {
		fatal(err)
	}
	b := &builder{root: abs, g: graph{Scope: "engineering", Title: "Engineering Graph", Meta: map[string]any{"generator": "go-ast+compose", "format_version": 1}}, nodes: map[string]bool{}, edges: map[string]bool{}, funcByPkgName: map[string]string{}}
	mods, err := findModules(abs)
	if err != nil {
		fatal(err)
	}
	if err := b.parseModules(mods); err != nil {
		fatal(err)
	}
	b.resolveCallsAndRoutes()
	b.parseCompose(filepath.Join(abs, "docker-compose.yml"))
	sort.Slice(b.g.Nodes, func(i, j int) bool { return b.g.Nodes[i].ID < b.g.Nodes[j].ID })
	sort.Slice(b.g.Edges, func(i, j int) bool { return b.g.Edges[i].ID < b.g.Edges[j].ID })
	b.g.Meta["nodes"] = len(b.g.Nodes)
	b.g.Meta["edges"] = len(b.g.Edges)
	b.g.Meta["modules"] = len(mods)
	data, err := json.MarshalIndent(b.g, "", "  ")
	if err != nil {
		fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("engineering graph: %d nodes, %d edges -> %s\n", len(b.g.Nodes), len(b.g.Edges), *out)
}

func findModules(root string) ([]module, error) {
	var mods []module
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "data" || base == "backups" || base == "exports" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		name := ""
		for _, line := range strings.Split(string(raw), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == "module" {
				name = f[1]
				break
			}
		}
		dir := filepath.Dir(path)
		rel, _ := filepath.Rel(root, dir)
		comp := strings.Split(filepath.ToSlash(rel), "/")[0]
		if strings.HasPrefix(filepath.ToSlash(rel), "services/") {
			p := strings.Split(filepath.ToSlash(rel), "/")
			if len(p) > 1 {
				comp = "services/" + p[1]
			}
		} else if strings.HasPrefix(filepath.ToSlash(rel), "platform/") {
			p := strings.Split(filepath.ToSlash(rel), "/")
			if len(p) > 1 {
				comp = "platform/" + p[1]
			}
		}
		mods = append(mods, module{Dir: dir, Name: name, Component: comp})
		return nil
	})
	sort.Slice(mods, func(i, j int) bool { return mods[i].Dir < mods[j].Dir })
	return mods, err
}

func (b *builder) parseModules(mods []module) error {
	for _, m := range mods {
		compID := "component:" + m.Component
		b.addNode(node{ID: compID, Kind: "component", Label: m.Component, Group: "engineering", Community: m.Component, Meta: map[string]any{"module": m.Name}})
		err := filepath.WalkDir(m.Dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "vendor" || d.Name() == "data" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, e := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if e != nil {
				return nil
			}
			relMod, _ := filepath.Rel(m.Dir, filepath.Dir(path))
			pkgImport := m.Name
			if relMod != "." {
				pkgImport += "/" + filepath.ToSlash(relMod)
			}
			pkgID := "package:" + pkgImport
			b.addNode(node{ID: pkgID, Kind: "package", Label: pkgImport, Group: "engineering", Community: m.Component, Meta: map[string]any{"package": f.Name.Name}})
			b.addEdge(edge{From: compID, To: pkgID, Kind: "contains_package"})
			rel, _ := filepath.Rel(b.root, path)
			fileID := "file:" + filepath.ToSlash(rel)
			b.addNode(node{ID: fileID, Kind: "file", Label: filepath.Base(path), Group: "engineering", Community: pkgImport, Meta: map[string]any{"path": filepath.ToSlash(rel)}})
			b.addEdge(edge{From: pkgID, To: fileID, Kind: "contains_file"})
			imports := map[string]string{}
			for _, im := range f.Imports {
				p, _ := strconv.Unquote(im.Path.Value)
				alias := filepath.Base(p)
				if im.Name != nil && im.Name.Name != "_" && im.Name.Name != "." {
					alias = im.Name.Name
				}
				imports[alias] = p
				ipid := "package:" + p
				b.addNode(node{ID: ipid, Kind: "package", Label: p, Group: "engineering", Community: moduleCommunity(p, mods)})
				b.addEdge(edge{From: fileID, To: ipid, Kind: "imports"})
			}
			pf := parsedFile{Path: path, Rel: filepath.ToSlash(rel), PackageID: pkgID, PackageName: f.Name.Name, Component: m.Component, File: f, Fset: fset, Imports: imports}
			b.files = append(b.files, pf)
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				name := fd.Name.Name
				recv := ""
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					recv = exprName(fd.Recv.List[0].Type)
					if recv != "" {
						name = recv + "." + name
					}
				}
				fid := "function:" + pkgImport + ":" + name
				pos := fset.Position(fd.Pos())
				b.addNode(node{ID: fid, Kind: "function", Label: name, Group: "engineering", Community: pkgImport, Meta: map[string]any{"path": filepath.ToSlash(rel), "line": pos.Line, "exported": ast.IsExported(fd.Name.Name)}})
				b.addEdge(edge{From: fileID, To: fid, Kind: "defines"})
				key := pkgID + "|" + fd.Name.Name
				if _, exists := b.funcByPkgName[key]; !exists {
					b.funcByPkgName[key] = fid
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) resolveCallsAndRoutes() {
	for _, pf := range b.files {
		for _, decl := range pf.File.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			caller := b.funcByPkgName[pf.PackageID+"|"+fd.Name.Name]
			if caller == "" {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				target, alias := callTarget(call.Fun)
				if target != "" {
					if callee := b.funcByPkgName[pf.PackageID+"|"+target]; callee != "" && callee != caller {
						b.addEdge(edge{From: caller, To: callee, Kind: "calls"})
					} else if alias != "" {
						if imp := pf.Imports[alias]; imp != "" {
							b.addEdge(edge{From: caller, To: "package:" + imp, Kind: "calls_package", Label: target})
						}
					}
				}
				if route, handler := routeCall(call); route != "" {
					rid := "route:" + route
					b.addNode(node{ID: rid, Kind: "route", Label: route, Group: "engineering", Community: pf.Component, Meta: map[string]any{"file": pf.Rel}})
					b.addEdge(edge{From: pf.PackageID, To: rid, Kind: "defines_route"})
					if h := b.funcByPkgName[pf.PackageID+"|"+handler]; h != "" {
						b.addEdge(edge{From: rid, To: h, Kind: "handles"})
					}
				}
				return true
			})
		}
	}
}

func (b *builder) parseCompose(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(raw), "\n")
	inServices := false
	service := ""
	inDepends := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if trim == "services:" {
			inServices = true
			continue
		}
		if !inServices {
			continue
		}
		if indent == 0 && trim != "" {
			break
		}
		if indent == 2 && strings.HasSuffix(trim, ":") {
			service = strings.TrimSuffix(trim, ":")
			inDepends = false
			sid := "service:" + service
			b.addNode(node{ID: sid, Kind: "service", Label: service, Group: "runtime", Community: "compose", Status: "configured"})
			continue
		}
		if service == "" {
			continue
		}
		if indent == 4 && trim == "depends_on:" {
			inDepends = true
			continue
		}
		if indent == 4 && strings.HasPrefix(trim, "image:") {
			b.setNodeMeta("service:"+service, "image", strings.TrimSpace(strings.TrimPrefix(trim, "image:")))
			inDepends = false
			continue
		}
		if indent == 4 && strings.HasPrefix(trim, "build:") {
			inDepends = false
			continue
		}
		if inDepends && indent >= 6 && strings.HasSuffix(trim, ":") {
			dep := strings.TrimSuffix(trim, ":")
			b.addNode(node{ID: "service:" + dep, Kind: "service", Label: dep, Group: "runtime", Community: "compose"})
			b.addEdge(edge{From: "service:" + service, To: "service:" + dep, Kind: "depends_on"})
			continue
		}
		if inDepends && indent == 6 && strings.HasPrefix(trim, "-") {
			dep := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
			b.addNode(node{ID: "service:" + dep, Kind: "service", Label: dep, Group: "runtime", Community: "compose"})
			b.addEdge(edge{From: "service:" + service, To: "service:" + dep, Kind: "depends_on"})
			continue
		}
		if indent <= 4 {
			inDepends = false
		}
	}
}

func (b *builder) addNode(n node) {
	if b.nodes[n.ID] {
		return
	}
	b.nodes[n.ID] = true
	b.g.Nodes = append(b.g.Nodes, n)
}
func (b *builder) addEdge(e edge) {
	if e.ID == "" {
		e.ID = e.From + "->" + e.To + ":" + e.Kind
	}
	if b.edges[e.ID] || e.From == "" || e.To == "" {
		return
	}
	b.edges[e.ID] = true
	b.g.Edges = append(b.g.Edges, e)
}
func (b *builder) setNodeMeta(id, k string, v any) {
	for i := range b.g.Nodes {
		if b.g.Nodes[i].ID == id {
			if b.g.Nodes[i].Meta == nil {
				b.g.Nodes[i].Meta = map[string]any{}
			}
			b.g.Nodes[i].Meta[k] = v
			return
		}
	}
}
func exprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return exprName(x.X)
	case *ast.IndexExpr:
		return exprName(x.X)
	case *ast.IndexListExpr:
		return exprName(x.X)
	}
	return ""
}
func callTarget(e ast.Expr) (name, alias string) {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name, ""
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return x.Sel.Name, id.Name
		}
		return x.Sel.Name, ""
	}
	return "", ""
}
func routeCall(c *ast.CallExpr) (route, handler string) {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") || len(c.Args) < 2 {
		return "", ""
	}
	lit, ok := c.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", ""
	}
	route, _ = strconv.Unquote(lit.Value)
	handler = deepHandlerName(c.Args[1])
	return route, handler
}
func deepHandlerName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.CallExpr:
		if len(x.Args) > 0 {
			return deepHandlerName(x.Args[len(x.Args)-1])
		}
	}
	return ""
}
func moduleCommunity(p string, mods []module) string {
	for _, m := range mods {
		if p == m.Name || strings.HasPrefix(p, m.Name+"/") {
			return m.Component
		}
	}
	return "external"
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
