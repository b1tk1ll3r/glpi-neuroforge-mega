package glpi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/glpi-ai-agent/internal/model"
)

type Client struct {
	baseURL, version, clientID, clientSecret, username, password string
	http                                                         *http.Client
	mu                                                           sync.Mutex
	token                                                        string
	tokenExpiry                                                  time.Time
}

func New(baseURL, version, clientID, clientSecret, username, password string, timeout time.Duration) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), version: version, clientID: clientID, clientSecret: clientSecret, username: username, password: password, http: &http.Client{Timeout: timeout}}
}

func (c *Client) APIBase() string { return c.baseURL + "/api.php/" + c.version }
func (c *Client) authenticate(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.token != "" && time.Until(c.tokenExpiry) > 60*time.Second {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"password"}, "client_id": {c.clientID}, "client_secret": {c.clientSecret}, "username": {c.username}, "password": {c.password}, "scope": {"api"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api.php/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("GLPI OAuth failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", err
	}
	if tr.AccessToken == "" {
		return "", errors.New("GLPI OAuth response contains no access_token")
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 3600
	}
	c.token = tr.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return c.token, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any) ([]byte, http.Header, error) {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.authenticate(ctx, attempt > 0)
		if err != nil {
			return nil, nil, err
		}
		u := c.APIBase() + path
		if len(query) > 0 {
			u += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(payload))
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, nil, err
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue
		}
		if resp.StatusCode/100 != 2 {
			return nil, resp.Header, fmt.Errorf("GLPI %s %s failed: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
		}
		return b, resp.Header, nil
	}
	return nil, nil, errors.New("GLPI request failed after token refresh")
}

func (c *Client) Ping(ctx context.Context) error {
	_, _, err := c.do(ctx, http.MethodGet, "/Assistance/Ticket", url.Values{"limit": {"1"}}, nil)
	return err
}
func (c *Client) FetchOpenAPI(ctx context.Context) (map[string]any, error) {
	tok, err := c.authenticate(ctx, false)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api.php/doc.json", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("OpenAPI HTTP %d", resp.StatusCode)
	}
	var v map[string]any
	return v, json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&v)
}
func (c *Client) ValidateContract(ctx context.Context) error {
	doc, err := c.FetchOpenAPI(ctx)
	if err != nil {
		return fmt.Errorf("fetch GLPI OpenAPI: %w", err)
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return errors.New("GLPI OpenAPI document has no paths map")
	}
	required := map[string][]string{
		"/Assistance/Ticket":                        {http.MethodGet},
		"/Assistance/Ticket/{id}":                   {http.MethodGet, http.MethodPatch},
		"/Assistance/Ticket/{id}/Timeline/Followup": {http.MethodGet, http.MethodPost},
		"/Dropdowns/ITILCategory":                   {http.MethodGet},
	}
	for route, methods := range required {
		op, found := openAPIOperations(paths, route)
		if !found {
			return fmt.Errorf("GLPI OpenAPI is missing required route %s; verify GLPI version/permissions", route)
		}
		for _, method := range methods {
			if _, ok := op[strings.ToLower(method)]; !ok {
				return fmt.Errorf("GLPI OpenAPI route %s does not expose %s; verify service-account permissions and API version", route, method)
			}
		}
	}
	return nil
}

func openAPIOperations(paths map[string]any, route string) (map[string]any, bool) {
	for documented, raw := range paths {
		// Depending on how the installed GLPI renders the schema, documented
		// paths may include the version prefix. Match the canonical route suffix.
		if documented != route && !strings.HasSuffix(documented, route) {
			continue
		}
		ops, ok := raw.(map[string]any)
		return ops, ok
	}
	return nil, false
}
func (c *Client) ListRecentTickets(ctx context.Context, limit int, filter string) ([]model.Ticket, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "sort": {"date_mod"}, "order": {"DESC"}}
	if strings.TrimSpace(filter) != "" {
		q.Set("filter", filter)
	}
	b, _, err := c.do(ctx, http.MethodGet, "/Assistance/Ticket", q, nil)
	if err != nil {
		return nil, err
	}
	arr, err := extractArray(b)
	if err != nil {
		return nil, err
	}
	out := make([]model.Ticket, 0, len(arr))
	for _, raw := range arr {
		out = append(out, decodeTicket(raw))
	}
	return out, nil
}

func (c *Client) ListEscalationCandidates(ctx context.Context, limit int, filter string) ([]model.Ticket, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "sort": {"date_creation"}, "order": {"ASC"}}
	if strings.TrimSpace(filter) != "" {
		q.Set("filter", filter)
	}
	b, _, err := c.do(ctx, http.MethodGet, "/Assistance/Ticket", q, nil)
	if err != nil {
		return nil, err
	}
	arr, err := extractArray(b)
	if err != nil {
		return nil, err
	}
	out := make([]model.Ticket, 0, len(arr))
	for _, raw := range arr {
		out = append(out, decodeTicket(raw))
	}
	return out, nil
}

func (c *Client) GetTicket(ctx context.Context, id int64) (model.Ticket, error) {
	b, _, err := c.do(ctx, http.MethodGet, "/Assistance/Ticket/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil {
		return model.Ticket{}, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return model.Ticket{}, err
	}
	return decodeTicket(raw), nil
}
func (c *Client) GetFollowups(ctx context.Context, id int64) ([]model.Followup, error) {
	b, _, err := c.do(ctx, http.MethodGet, "/Assistance/Ticket/"+strconv.FormatInt(id, 10)+"/Timeline/Followup", url.Values{"limit": {"100"}}, nil)
	if err != nil {
		return nil, err
	}
	arr, err := extractArray(b)
	if err != nil {
		return nil, err
	}
	out := make([]model.Followup, 0, len(arr))
	for _, raw := range arr {
		out = append(out, decodeFollowup(raw))
	}
	return out, nil
}
func (c *Client) SetCategory(ctx context.Context, id, categoryID int64) error {
	_, _, err := c.do(ctx, http.MethodPatch, "/Assistance/Ticket/"+strconv.FormatInt(id, 10), nil, map[string]any{"category": map[string]any{"id": categoryID}})
	return err
}
func (c *Client) SetPriority(ctx context.Context, id, priority int64) error {
	_, _, err := c.do(ctx, http.MethodPatch, "/Assistance/Ticket/"+strconv.FormatInt(id, 10), nil, map[string]any{"priority": priority})
	return err
}

func (c *Client) SetAssignedGroups(ctx context.Context, id int64, groupIDs []int64, field string) error {
	return c.setTicketActors(ctx, id, groupIDs, field, "assigned_groups")
}

func (c *Client) SetAssignedUsers(ctx context.Context, id int64, userIDs []int64, field string) error {
	return c.setTicketActors(ctx, id, userIDs, field, "assigned_users")
}

func (c *Client) setTicketActors(ctx context.Context, id int64, actorIDs []int64, field, fallback string) error {
	field = strings.TrimSpace(field)
	if field == "" {
		field = fallback
	}
	ids := uniquePositiveIDs(actorIDs)
	if len(ids) == 0 {
		return errors.New("ticket actor assignment requires at least one positive ID")
	}
	var value any
	switch strings.ToLower(field) {
	case "group", "group_tech", "user", "user_tech":
		value = map[string]any{"id": ids[len(ids)-1]}
	default:
		refs := make([]map[string]any, 0, len(ids))
		for _, actorID := range ids {
			refs = append(refs, map[string]any{"id": actorID})
		}
		value = refs
	}
	_, _, err := c.do(ctx, http.MethodPatch, "/Assistance/Ticket/"+strconv.FormatInt(id, 10), nil, map[string]any{field: value})
	return err
}

func uniquePositiveIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (c *Client) AddPrivateFollowup(ctx context.Context, id int64, content string, richHTML bool) error {
	return c.addFollowup(ctx, id, content, richHTML, true)
}

func (c *Client) AddFollowup(ctx context.Context, id int64, content string, richHTML bool) error {
	return c.addFollowup(ctx, id, content, richHTML, false)
}

func (c *Client) addFollowup(ctx context.Context, id int64, content string, richHTML, private bool) error {
	content = strings.TrimSpace(content)
	if !richHTML {
		content = "<p>" + strings.ReplaceAll(html.EscapeString(content), "\n", "<br>") + "</p>"
	}
	_, _, err := c.do(ctx, http.MethodPost, "/Assistance/Ticket/"+strconv.FormatInt(id, 10)+"/Timeline/Followup", nil, map[string]any{"content": content, "is_private": private})
	return err
}

func (c *Client) LinkITILObject(ctx context.Context, ticketID, targetTicketID int64, pathTemplate, bodyTemplate string) error {
	if ticketID <= 0 || targetTicketID <= 0 {
		return errors.New("ITIL link requires positive source and target ticket IDs")
	}
	pathTemplate = strings.TrimSpace(pathTemplate)
	bodyTemplate = strings.TrimSpace(bodyTemplate)
	if pathTemplate == "" || bodyTemplate == "" {
		return errors.New("ITIL link path/body template is not configured")
	}
	replacer := strings.NewReplacer(
		"{{ticket_id}}", strconv.FormatInt(ticketID, 10),
		"{{source_ticket_id}}", strconv.FormatInt(ticketID, 10),
		"{{major_incident_id}}", strconv.FormatInt(targetTicketID, 10),
		"{{target_ticket_id}}", strconv.FormatInt(targetTicketID, 10),
	)
	path := replacer.Replace(pathTemplate)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	var body any
	if err := json.Unmarshal([]byte(replacer.Replace(bodyTemplate)), &body); err != nil {
		return fmt.Errorf("decode GLPI_ESCALATION_ITIL_LINK_BODY: %w", err)
	}
	_, _, err := c.do(ctx, http.MethodPost, path, nil, body)
	return err
}

func (c *Client) GetCategories(ctx context.Context) ([]model.Category, error) {
	b, _, err := c.do(ctx, http.MethodGet, "/Dropdowns/ITILCategory", url.Values{"limit": {"1000"}}, nil)
	if err != nil {
		return nil, err
	}
	arr, err := extractArray(b)
	if err != nil {
		return nil, err
	}
	out := make([]model.Category, 0, len(arr))
	for _, r := range arr {
		out = append(out, model.Category{ID: int64Val(r["id"]), Name: strVal(r["name"]), CompleteName: strVal(r["completename"]), KnowbaseCategoryID: firstRefID(r, "knowbase_category", "knowbasecategory")})
	}
	return out, nil
}

// DiscoverKnowledgeBasePath finds the read-only KnowbaseItem collection route
// from the OpenAPI document of the installed GLPI instance. This avoids
// hard-coding a path that may move between high-level API versions.
func (c *Client) DiscoverKnowledgeBasePath(ctx context.Context, configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" && !strings.EqualFold(configured, "auto") {
		if err := c.ValidateReadRoutes(ctx, []string{configured}); err != nil {
			return "", err
		}
		return configured, nil
	}
	doc, err := c.FetchOpenAPI(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch GLPI OpenAPI: %w", err)
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return "", errors.New("GLPI OpenAPI document has no paths map")
	}
	type candidate struct {
		path  string
		score int
	}
	var candidates []candidate
	for path, raw := range paths {
		if strings.Contains(path, "{") {
			continue
		}
		ops, ok := raw.(map[string]any)
		if !ok || ops["get"] == nil {
			continue
		}
		l := strings.ToLower(path)
		score := 0
		if strings.Contains(l, "knowbaseitem") {
			score += 100
		}
		if strings.Contains(l, "knowledge") {
			score += 40
		}
		if strings.Contains(l, "knowbase") {
			score += 40
		}
		if strings.HasSuffix(l, "/knowbaseitem") {
			score += 30
		}
		if score > 0 {
			candidates = append(candidates, candidate{path: path, score: score})
		}
	}
	if len(candidates) == 0 {
		return "", errors.New("GLPI OpenAPI exposes no readable KnowbaseItem collection route; verify GLPI version and service-account knowledge-base rights")
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].score > candidates[j].score
	})
	path := candidates[0].path
	// The OpenAPI document may include /api.php/vX.Y in documented paths while
	// c.do already prepends the configured API base. Keep only the route suffix.
	if i := strings.Index(path, "/api.php/"); i >= 0 {
		rest := path[i+len("/api.php/"):]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			path = rest[slash:]
		}
	}
	versionPrefix := "/" + strings.Trim(c.version, "/")
	if strings.HasPrefix(path, versionPrefix+"/") {
		path = strings.TrimPrefix(path, versionPrefix)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path, nil
}

// ListKnowledgeBaseItems reads only items visible to the authenticated GLPI
// service account. Visibility is therefore enforced by GLPI itself; an
// optional filter can further restrict the collection on a per-instance basis.
func (c *Client) ListKnowledgeBaseItems(ctx context.Context, path string, limit int, filter string) ([]model.GLPIKnowledgeItem, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "sort": {"date_mod"}, "order": {"DESC"}}
	if strings.TrimSpace(filter) != "" {
		q.Set("filter", filter)
	}
	b, _, err := c.do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	arr, err := extractArray(b)
	if err != nil {
		return nil, err
	}
	out := make([]model.GLPIKnowledgeItem, 0, len(arr))
	for _, r := range arr {
		id := int64Val(r["id"])
		if id <= 0 {
			continue
		}
		if firstString(r, "answer", "content", "text", "description") == "" {
			if detail, _, e := c.do(ctx, http.MethodGet, strings.TrimRight(path, "/")+"/"+strconv.FormatInt(id, 10), nil, nil); e == nil {
				var full map[string]any
				if json.Unmarshal(detail, &full) == nil {
					for k, v := range full {
						r[k] = v
					}
				}
			}
		}
		title := firstString(r, "name", "title", "subject")
		content := firstString(r, "answer", "content", "text", "description")
		if title == "" || content == "" {
			continue
		}
		out = append(out, model.GLPIKnowledgeItem{
			ID:          id,
			Title:       title,
			Content:     content,
			CategoryIDs: knowledgeCategoryIDs(r),
			Language:    firstString(r, "language", "locale"),
			ModifiedAt:  firstString(r, "date_mod", "modified_at", "date_creation"),
		})
	}
	return out, nil
}

// ListKnowledgeBaseLinkedItems reads GLPI's KnowbaseItem_Item relation using
// the route exposed by the installed OpenAPI contract. GLPI models these
// records as (knowbaseitems_id, itemtype, items_id). The implementation
// supports both a global relation collection and nested per-article routes.
func (c *Client) ListKnowledgeBaseLinkedItems(ctx context.Context, articleIDs []int64, limit int) (map[int64][]model.LinkedItem, error) {
	doc, err := c.FetchOpenAPI(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch GLPI OpenAPI for knowledge links: %w", err)
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return nil, errors.New("GLPI OpenAPI document has no paths map")
	}
	type candidate struct {
		path   string
		nested bool
		score  int
	}
	var candidates []candidate
	for documented, raw := range paths {
		ops, ok := raw.(map[string]any)
		if !ok || ops["get"] == nil {
			continue
		}
		l := strings.ToLower(documented)
		compact := strings.NewReplacer("_", "", "-", "", "/", "").Replace(l)
		if !strings.Contains(compact, "knowbaseitemitem") {
			continue
		}
		nested := strings.Contains(documented, "{")
		score := 100
		if !nested {
			score += 40
		}
		if strings.Contains(l, "knowledge") {
			score += 10
		}
		candidates = append(candidates, candidate{path: documented, nested: nested, score: score})
	}
	if len(candidates) == 0 {
		return nil, errors.New("GLPI OpenAPI exposes no readable KnowbaseItem_Item relation route")
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].score > candidates[j].score
	})
	chosen := candidates[0]
	cleanPath := func(p string) string {
		if i := strings.Index(p, "/api.php/"); i >= 0 {
			rest := p[i+len("/api.php/"):]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				p = rest[slash:]
			}
		}
		versionPrefix := "/" + strings.Trim(c.version, "/")
		if strings.HasPrefix(p, versionPrefix+"/") {
			p = strings.TrimPrefix(p, versionPrefix)
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		return p
	}
	chosen.path = cleanPath(chosen.path)
	if limit <= 0 {
		limit = 10000
	}
	result := map[int64][]model.LinkedItem{}
	seen := map[string]struct{}{}
	addRows := func(articleHint int64, body []byte) error {
		arr, err := extractArray(body)
		if err != nil {
			return err
		}
		for _, r := range arr {
			articleID := firstPositiveInt(r, "knowbaseitems_id", "knowbaseitem_id")
			if articleID <= 0 {
				articleID = articleHint
			}
			itemType := firstString(r, "itemtype", "item_type", "type")
			itemID := firstPositiveInt(r, "items_id", "item_id")
			name := firstString(r, "item_name", "name", "completename", "title")
			if articleID <= 0 || itemID <= 0 || strings.TrimSpace(itemType) == "" {
				continue
			}
			key := fmt.Sprintf("%d|%s|%d", articleID, strings.ToLower(itemType), itemID)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result[articleID] = append(result[articleID], model.LinkedItem{ItemType: itemType, ID: itemID, Name: name})
		}
		return nil
	}

	if !chosen.nested {
		body, _, err := c.do(ctx, http.MethodGet, chosen.path, url.Values{"limit": {strconv.Itoa(limit)}}, nil)
		if err != nil {
			return nil, err
		}
		if err := addRows(0, body); err != nil {
			return nil, err
		}
	} else {
		placeholder := regexp.MustCompile(`\{[^/{}]+\}`)
		if len(placeholder.FindAllString(chosen.path, -1)) != 1 {
			return nil, fmt.Errorf("unsupported GLPI knowledge relation route %q", chosen.path)
		}
		for _, articleID := range articleIDs {
			p := placeholder.ReplaceAllString(chosen.path, strconv.FormatInt(articleID, 10))
			body, _, err := c.do(ctx, http.MethodGet, p, url.Values{"limit": {strconv.Itoa(limit)}}, nil)
			if err != nil {
				return nil, err
			}
			if err := addRows(articleID, body); err != nil {
				return nil, err
			}
		}
	}
	for id := range result {
		sort.Slice(result[id], func(i, j int) bool {
			if result[id][i].ItemType == result[id][j].ItemType {
				return result[id][i].ID < result[id][j].ID
			}
			return result[id][i].ItemType < result[id][j].ItemType
		})
	}
	return result, nil
}

func firstPositiveInt(r map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if id := int64Val(r[key]); id > 0 {
			return id
		}
	}
	return 0
}

func knowledgeCategoryIDs(r map[string]any) []int64 {
	seen := map[int64]struct{}{}
	var out []int64
	var add func(any)
	add = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				add(e)
			}
		case map[string]any:
			if id := int64Val(x["id"]); id > 0 {
				if _, ok := seen[id]; !ok {
					seen[id] = struct{}{}
					out = append(out, id)
				}
				return
			}
			for _, v2 := range x {
				add(v2)
			}
		default:
			if id := int64Val(x); id > 0 {
				if _, ok := seen[id]; !ok {
					seen[id] = struct{}{}
					out = append(out, id)
				}
			}
		}
	}
	for _, k := range []string{"knowbase_category", "knowbase_categories", "knowbaseitemcategory", "knowbaseitemcategories", "categories", "category"} {
		if v, ok := r[k]; ok {
			add(v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func extractArray(b []byte) ([]map[string]any, error) {
	var arr []map[string]any
	if json.Unmarshal(b, &arr) == nil {
		return arr, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, err
	}
	for _, k := range []string{"data", "items", "results"} {
		if raw, ok := obj[k]; ok && json.Unmarshal(raw, &arr) == nil {
			return arr, nil
		}
	}
	return nil, fmt.Errorf("unexpected GLPI collection response: %.200s", string(b))
}
func decodeTicket(r map[string]any) model.Ticket {
	t := model.Ticket{
		ID:            int64Val(r["id"]),
		Name:          strVal(r["name"]),
		Content:       strVal(r["content"]),
		DateCreation:  firstString(r, "date_creation", "date", "created_at"),
		DateMod:       firstString(r, "date_mod", "modified_at"),
		StatusID:      refID(r["status"]),
		CategoryID:    firstRefID(r, "category", "itil_category", "itilcategory"),
		Priority:      int64Val(r["priority"]),
		Impact:        int64Val(r["impact"]),
		Urgency:       int64Val(r["urgency"]),
		EntityID:      firstRefID(r, "entity", "entities_id"),
		LocationID:    firstRefID(r, "location", "locations_id"),
		TimeToResolve: firstString(r, "time_to_resolve", "sla_deadline"),
	}
	t.RequesterIDs = extractRequesterIDs(r)
	t.AssignedGroups = extractActorIDs(r, "group", "assign")
	t.AssignedUsers = extractActorIDs(r, "user", "assign")
	t.Items = extractLinkedItems(r)
	return t
}

func extractActorIDs(r map[string]any, actorKind, roleNeedle string) []int64 {
	seen := map[int64]struct{}{}
	var out []int64
	add := func(v any) {
		id := refID(v)
		if id <= 0 {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	keys := []string{"assigned_" + actorKind + "s", actorKind + "s_assigned", actorKind + "_assigned"}
	for _, key := range keys {
		v, ok := r[key]
		if !ok {
			continue
		}
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				add(item)
			}
		} else {
			add(v)
		}
	}
	if actors, ok := r["actors"].([]any); ok {
		for _, raw := range actors {
			m, _ := raw.(map[string]any)
			role := strings.ToLower(firstString(m, "role", "type", "actor_type"))
			kind := strings.ToLower(firstString(m, "itemtype", "actor_kind", "kind"))
			if strings.Contains(role, roleNeedle) && (kind == "" || strings.Contains(kind, actorKind)) {
				if v, ok := m[actorKind]; ok {
					add(v)
				} else {
					add(m)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func extractRequesterIDs(r map[string]any) []int64 {
	seen := map[int64]struct{}{}
	var out []int64
	add := func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				addRequesterID(e, seen, &out)
			}
		default:
			addRequesterID(x, seen, &out)
		}
	}
	for _, k := range []string{"requester", "requesters", "users_requester", "users_requesters", "requester_users"} {
		if v, ok := r[k]; ok {
			add(v)
		}
	}
	if actors, ok := r["actors"].([]any); ok {
		for _, raw := range actors {
			m, _ := raw.(map[string]any)
			role := strings.ToLower(firstString(m, "role", "type", "actor_type"))
			if strings.Contains(role, "request") || strings.Contains(role, "demande") {
				addRequesterID(m, seen, &out)
			}
		}
	}
	return out
}

func addRequesterID(v any, seen map[int64]struct{}, out *[]int64) {
	id := int64(0)
	if m, ok := v.(map[string]any); ok {
		id = firstRefID(m, "user", "requester")
		if id == 0 {
			id = int64Val(m["id"])
		}
	} else {
		id = refID(v)
	}
	if id <= 0 {
		return
	}
	if _, ok := seen[id]; ok {
		return
	}
	seen[id] = struct{}{}
	*out = append(*out, id)
}

func extractLinkedItems(r map[string]any) []model.LinkedItem {
	seen := map[string]struct{}{}
	var out []model.LinkedItem
	visit := func(v any) {
		arr, ok := v.([]any)
		if !ok {
			arr = []any{v}
		}
		for _, raw := range arr {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typ := firstString(m, "itemtype", "type", "item_type")
			id := int64Val(m["id"])
			if id == 0 {
				id = int64Val(m["items_id"])
			}
			if id == 0 {
				id = firstRefID(m, "item")
			}
			if typ == "" {
				if item, ok := m["item"].(map[string]any); ok {
					typ = firstString(item, "itemtype", "type")
					if id == 0 {
						id = int64Val(item["id"])
					}
				}
			}
			if id <= 0 || typ == "" {
				continue
			}
			key := strings.ToLower(typ) + ":" + strconv.FormatInt(id, 10)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, model.LinkedItem{ItemType: typ, ID: id, Name: firstString(m, "name", "completename")})
		}
	}
	for _, k := range []string{"items", "assets", "associated_items", "linked_items", "item"} {
		if v, ok := r[k]; ok {
			visit(v)
		}
	}
	if typ := firstString(r, "itemtype"); typ != "" {
		if id := int64Val(r["items_id"]); id > 0 {
			visit(map[string]any{"itemtype": typ, "id": id})
		}
	}
	return out
}

func decodeFollowup(r map[string]any) model.Followup {
	return model.Followup{ID: int64Val(r["id"]), Content: strVal(r["content"]), IsPrivate: boolVal(r["is_private"]), UserID: firstRefID(r, "user", "author", "user_editor"), Date: strVal(r["date"])}
}
func firstRefID(r map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := r[k]; ok {
			if id := refID(v); id != 0 {
				return id
			}
		}
	}
	return 0
}
func refID(v any) int64 {
	if m, ok := v.(map[string]any); ok {
		return int64Val(m["id"])
	}
	return int64Val(v)
}
func int64Val(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}
func strVal(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func boolVal(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x == "1" || strings.EqualFold(x, "true")
	}
	return false
}

// ValidateReadRoutes checks optional, read-only context routes against the
// OpenAPI document exposed by the installed GLPI instance. This keeps optional
// integrations explicit and catches renamed/unavailable routes at startup.
func (c *Client) ValidateReadRoutes(ctx context.Context, routes []string) error {
	if len(routes) == 0 {
		return nil
	}
	doc, err := c.FetchOpenAPI(ctx)
	if err != nil {
		return fmt.Errorf("fetch GLPI OpenAPI: %w", err)
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return errors.New("GLPI OpenAPI document has no paths map")
	}
	for _, route := range routes {
		op, found := openAPIOperations(paths, route)
		if !found {
			return fmt.Errorf("GLPI OpenAPI is missing optional context route %s", route)
		}
		if _, ok := op[strings.ToLower(http.MethodGet)]; !ok {
			return fmt.Errorf("GLPI context route %s does not expose GET", route)
		}
	}
	return nil
}

func (c *Client) ListChanges(ctx context.Context, path string, limit int, filter string) ([]model.ChangeContext, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "sort": {"date_mod"}, "order": {"DESC"}}
	if strings.TrimSpace(filter) != "" {
		q.Set("filter", filter)
	}
	b, _, err := c.do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	arr, err := extractArray(b)
	if err != nil {
		return nil, err
	}
	out := make([]model.ChangeContext, 0, len(arr))
	for _, r := range arr {
		out = append(out, model.ChangeContext{
			ID:           int64Val(r["id"]),
			Name:         strVal(r["name"]),
			Content:      firstString(r, "content", "description"),
			StatusID:     firstRefID(r, "status"),
			CategoryID:   firstRefID(r, "category", "itil_category", "itilcategory"),
			PlannedBegin: firstString(r, "planned_begin", "planned_start", "date_begin"),
			PlannedEnd:   firstString(r, "planned_end", "planned_finish", "date_end"),
			DateMod:      firstString(r, "date_mod", "modified_at"),
			Source:       "glpi-change",
		})
	}
	return out, nil
}

func (c *Client) ListMajorIncidents(ctx context.Context, limit int, filter string) ([]model.MajorIncidentContext, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "sort": {"date_mod"}, "order": {"DESC"}}
	if strings.TrimSpace(filter) != "" {
		q.Set("filter", filter)
	}
	b, _, err := c.do(ctx, http.MethodGet, "/Assistance/Ticket", q, nil)
	if err != nil {
		return nil, err
	}
	arr, err := extractArray(b)
	if err != nil {
		return nil, err
	}
	out := make([]model.MajorIncidentContext, 0, len(arr))
	for _, r := range arr {
		out = append(out, model.MajorIncidentContext{
			ID:         int64Val(r["id"]),
			Name:       strVal(r["name"]),
			Content:    firstString(r, "content", "description"),
			StatusID:   firstRefID(r, "status"),
			CategoryID: firstRefID(r, "category", "itil_category", "itilcategory"),
			Priority:   int64Val(r["priority"]),
			Impact:     int64Val(r["impact"]),
			Urgency:    int64Val(r["urgency"]),
			DateMod:    firstString(r, "date_mod", "modified_at"),
			Source:     "glpi-major-incident",
		})
	}
	return out, nil
}

// ListUserDevices searches one or more read-only asset collection routes for
// assets assigned to a requester. The filter template is configuration-driven
// because field aliases can differ with GLPI API versions/plugins.
func (c *Client) ListUserDevices(ctx context.Context, userID int64, paths []string, filterTemplate string, limit int) ([]model.UserDeviceContext, error) {
	if userID <= 0 {
		return nil, nil
	}
	filter := strings.ReplaceAll(filterTemplate, "{{user_id}}", strconv.FormatInt(userID, 10))
	var out []model.UserDeviceContext
	for _, path := range paths {
		q := url.Values{"limit": {strconv.Itoa(limit)}, "sort": {"date_mod"}, "order": {"DESC"}}
		if strings.TrimSpace(filter) != "" {
			q.Set("filter", filter)
		}
		b, _, err := c.do(ctx, http.MethodGet, path, q, nil)
		if err != nil {
			return nil, fmt.Errorf("query %s for user %d: %w", path, userID, err)
		}
		arr, err := extractArray(b)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		itemType := strings.TrimPrefix(path[strings.LastIndex(path, "/"):], "/")
		for _, r := range arr {
			out = append(out, model.UserDeviceContext{
				UserID:            userID,
				ItemType:          itemType,
				ID:                int64Val(r["id"]),
				Name:              strVal(r["name"]),
				Serial:            firstString(r, "serial", "serial_number"),
				InventoryNumber:   firstString(r, "otherserial", "inventory_number", "inventory_no"),
				Status:            refName(r["status"]),
				Location:          refName(r["location"]),
				LastInventoryDate: firstString(r, "last_inventory_update", "last_inventory_date", "date_mod"),
				Source:            "glpi-user-device",
			})
			if len(out) >= limit {
				return out[:limit], nil
			}
		}
	}
	return out, nil
}

func firstString(r map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := r[k]; ok {
			s := strings.TrimSpace(strVal(v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func refName(v any) string {
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"completename", "name", "label"} {
			if s := strings.TrimSpace(strVal(m[k])); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return strings.TrimSpace(strVal(v))
}
