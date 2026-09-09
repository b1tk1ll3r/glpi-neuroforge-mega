package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"neuroforge/internal/ingest"
)

type SearchConfig struct {
	BaseURL       string
	Language      string
	Categories    string
	SafeSearch    int
	Timeout       time.Duration
	MaxResults    int
	Authorization string
}

// Result mirrors the useful fields emitted by the SearXNG JSON API. File-result
// engines may additionally populate filename/mimetype/size/template.
type Result struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Content     string   `json:"content"`
	Engine      string   `json:"engine,omitempty"`
	Engines     []string `json:"engines,omitempty"`
	Score       float64  `json:"score,omitempty"`
	PublishedAt string   `json:"publishedDate,omitempty"`
	Template    string   `json:"template,omitempty"`
	Filename    string   `json:"filename,omitempty"`
	MIMEType    string   `json:"mimetype,omitempty"`
	Size        string   `json:"size,omitempty"`
	Abstract    string   `json:"abstract,omitempty"`
	Category    string   `json:"category,omitempty"`
}

type searchResponse struct {
	Results []Result `json:"results"`
}

func Search(ctx context.Context, cfg SearchConfig, query string) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("search query required")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, errors.New("SearXNG base URL is empty")
	}
	u, err := url.Parse(base + "/search")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	if cfg.Language != "" {
		q.Set("language", cfg.Language)
	}
	if cfg.Categories != "" {
		q.Set("categories", cfg.Categories)
	}
	q.Set("safesearch", fmt.Sprint(cfg.SafeSearch))
	u.RawQuery = q.Encode()
	to := cfg.Timeout
	if to <= 0 {
		to = 20 * time.Second
	}
	client := &http.Client{Timeout: to}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(cfg.Authorization) != "" {
		req.Header.Set("Authorization", cfg.Authorization)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("SearXNG HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out searchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, err
	}
	limit := cfg.MaxResults
	if limit <= 0 {
		limit = 10
	}
	if len(out.Results) > limit {
		out.Results = out.Results[:limit]
	}
	return out.Results, nil
}

type FetchConfig struct {
	Timeout             time.Duration
	MaxBytes            int64
	MaxDocumentBytes    int64
	MaxChars            int
	UserAgent           string
	AllowPrivateTargets bool
	HintFilename        string
	HintMIMEType        string
}

type Page struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	ContentType string `json:"content_type"`
	Text        string `json:"text"`
	Bytes       int64  `json:"bytes"`
}

// Resource is a safely fetched SearXNG result target. Page resources expose
// Text; document resources expose Data so the normal document ingestion stack
// (PDF/DOCX/TXT/MD/JSON/CSV/...) can extract and chunk them.
type Resource struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type"`
	Kind        string `json:"kind"` // page | document
	Text        string `json:"text,omitempty"`
	Data        []byte `json:"-"`
	Bytes       int64  `json:"bytes"`
}

// FetchResource downloads one public HTTP(S) resource with DNS-rebinding and
// redirect protection. It distinguishes browser pages from knowledge documents
// using the response Content-Type, Content-Disposition and final URL.
func FetchResource(ctx context.Context, cfg FetchConfig, rawURL string) (Resource, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return Resource{}, errors.New("only absolute http(s) URLs are fetchable")
	}
	if !cfg.AllowPrivateTargets {
		if err := rejectPrivateHost(ctx, u.Hostname()); err != nil {
			return Resource{}, err
		}
	}
	pageMax := cfg.MaxBytes
	if pageMax <= 0 {
		pageMax = 4 << 20
	}
	docMax := cfg.MaxDocumentBytes
	if docMax <= 0 {
		docMax = 25 << 20
	}
	to := cfg.Timeout
	if to <= 0 {
		to = 20 * time.Second
	}
	client := newSafeFetchClient(cfg, to)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document,text/plain,text/markdown,text/csv,application/json;q=0.9,*/*;q=0.2")
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua == "" {
		ua = "NeuroForge/0.8.3 research bot"
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return Resource{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Resource{}, fmt.Errorf("fetch HTTP %d", resp.StatusCode)
	}
	ct := normalizedContentType(resp.Header.Get("Content-Type"))
	name := responseFilename(resp)
	if name == "" {
		name = filepath.Base(resp.Request.URL.Path)
	}
	// Some download endpoints intentionally respond as application/octet-stream
	// and have no extension in the URL. In that case SearXNG File-result hints
	// are useful. A specific final HTTP type (e.g. text/html) always wins.
	if (name == "" || name == "." || !IsDocumentResource(name, "")) && strings.TrimSpace(cfg.HintFilename) != "" {
		if ct == "" || ct == "application/octet-stream" {
			name = filepath.Base(strings.TrimSpace(cfg.HintFilename))
		}
	}
	if (ct == "" || ct == "application/octet-stream") && IsDocumentResource(name, cfg.HintMIMEType) {
		ct = normalizedContentType(cfg.HintMIMEType)
	}
	document := IsDocumentResource(name, ct)
	limit := pageMax
	if document {
		limit = docMax
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return Resource{}, err
	}
	if int64(len(body)) > limit {
		if document {
			return Resource{}, fmt.Errorf("document exceeds max_document_bytes=%d", limit)
		}
		return Resource{}, fmt.Errorf("page exceeds max_bytes=%d", limit)
	}
	finalURL := resp.Request.URL.String()
	if document {
		if name == "" || name == "." || name == "/" {
			name = "research-document" + extensionForMIME(ct)
		}
		title := strings.TrimSpace(name)
		if title == "" {
			title = resp.Request.URL.Hostname()
		}
		return Resource{URL: finalURL, Title: title, Filename: name, ContentType: ct, Kind: "document", Data: body, Bytes: int64(len(body))}, nil
	}
	text := ""
	lowerCT := strings.ToLower(ct)
	pathLower := strings.ToLower(resp.Request.URL.Path)
	if strings.Contains(lowerCT, "html") || strings.HasSuffix(pathLower, ".html") || strings.HasSuffix(pathLower, ".htm") {
		text = ingest.HTMLToText(string(body))
	} else if strings.HasPrefix(lowerCT, "text/") || strings.Contains(lowerCT, "json") || ct == "" {
		text = strings.TrimSpace(string(body))
	} else {
		return Resource{}, fmt.Errorf("unsupported web content type %q", ct)
	}
	if cfg.MaxChars > 0 {
		r := []rune(text)
		if len(r) > cfg.MaxChars {
			text = string(r[:cfg.MaxChars])
		}
	}
	title := extractTitle(string(body))
	if title == "" {
		title = resp.Request.URL.Hostname()
	}
	return Resource{URL: finalURL, Title: title, Filename: name, ContentType: ct, Kind: "page", Text: text, Bytes: int64(len(body))}, nil
}

func FetchPage(ctx context.Context, cfg FetchConfig, rawURL string) (Page, error) {
	r, err := FetchResource(ctx, cfg, rawURL)
	if err != nil {
		return Page{}, err
	}
	if r.Kind != "page" {
		return Page{}, fmt.Errorf("resource is a document (%s); use FetchResource", r.ContentType)
	}
	return Page{URL: r.URL, Title: r.Title, ContentType: r.ContentType, Text: r.Text, Bytes: r.Bytes}, nil
}

// IsDocumentResource returns whether a result target should be routed through
// NeuroForge's document extractor rather than the HTML/text page extractor.
func IsDocumentResource(name, contentType string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
	switch ext {
	case ".pdf", ".docx", ".txt", ".md", ".markdown", ".log", ".yaml", ".yml", ".csv", ".tsv", ".json":
		return true
	}
	ct := normalizedContentType(contentType)
	switch ct {
	case "application/pdf", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "text/plain", "text/markdown", "text/csv", "text/tab-separated-values", "application/json", "application/yaml", "text/yaml":
		return true
	}
	return false
}

func ResultLooksLikeDocument(r Result) bool {
	if IsDocumentResource(r.Filename, r.MIMEType) {
		return true
	}
	if u, err := url.Parse(r.URL); err == nil && IsDocumentResource(filepath.Base(u.Path), r.MIMEType) {
		return true
	}
	return strings.Contains(strings.ToLower(r.Template), "file")
}

func newSafeFetchClient(cfg FetchConfig, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{TLSHandshakeTimeout: 8 * time.Second, ResponseHeaderTimeout: timeout}
	if cfg.AllowPrivateTargets {
		transport.DialContext = dialer.DialContext
	} else {
		transport.DialContext = func(dctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if err := rejectPrivateHostname(host); err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIP(dctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("resolve research target: %w", err)
			}
			var lastErr error
			for _, ip := range ips {
				if isPrivateIP(ip) {
					continue
				}
				conn, err := dialer.DialContext(dctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, fmt.Errorf("research target %s has no public address on port %s", host, strconv.Quote(port))
		}
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if !cfg.AllowPrivateTargets {
			if err := rejectPrivateHost(req.Context(), req.URL.Hostname()); err != nil {
				return err
			}
		}
		return nil
	}}
}

func normalizedContentType(v string) string {
	ct, _, err := mime.ParseMediaType(strings.TrimSpace(v))
	if err == nil && ct != "" {
		return strings.ToLower(ct)
	}
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = v[:i]
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func responseFilename(resp *http.Response) string {
	cd := strings.TrimSpace(resp.Header.Get("Content-Disposition"))
	if cd == "" {
		return ""
	}
	_, p, err := mime.ParseMediaType(cd)
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSpace(p["filename"]))
}

func extensionForMIME(ct string) string {
	switch normalizedContentType(ct) {
	case "application/pdf":
		return ".pdf"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/json":
		return ".json"
	case "text/csv":
		return ".csv"
	case "text/markdown":
		return ".md"
	default:
		return ".txt"
	}
}

func rejectPrivateHost(ctx context.Context, host string) error {
	if err := rejectPrivateHostname(host); err != nil {
		return err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve research target: %w", err)
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("private research target %s is blocked", ip)
		}
	}
	return nil
}

func rejectPrivateHostname(host string) error {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return errors.New("private/local research target is blocked")
	}
	if ip := net.ParseIP(host); ip != nil && isPrivateIP(ip) {
		return fmt.Errorf("private research target %s is blocked", ip)
	}
	return nil
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

func extractTitle(raw string) string {
	lower := strings.ToLower(raw)
	i := strings.Index(lower, "<title")
	if i < 0 {
		return ""
	}
	start := strings.Index(raw[i:], ">")
	if start < 0 {
		return ""
	}
	start += i + 1
	end := strings.Index(strings.ToLower(raw[start:]), "</title>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(ingest.HTMLToText(raw[start : start+end]))
}
