package glpi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOAuthAndGetTicket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Assistance/Ticket/5":
			json.NewEncoder(w).Encode(map[string]any{"id": 5, "name": "Hello", "category": map[string]any{"id": 2}, "status": map[string]any{"id": 1}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	got, err := c.GetTicket(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 5 || got.CategoryID != 2 {
		t.Fatalf("unexpected %+v", got)
	}
}

func TestValidateContractAcceptsVersionPrefixedOpenAPIPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/doc.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{
				"/v2.3/Assistance/Ticket":                        map[string]any{"get": map[string]any{}},
				"/v2.3/Assistance/Ticket/{id}":                   map[string]any{"get": map[string]any{}, "patch": map[string]any{}},
				"/v2.3/Assistance/Ticket/{id}/Timeline/Followup": map[string]any{"get": map[string]any{}, "post": map[string]any{}},
				"/v2.3/Dropdowns/ITILCategory":                   map[string]any{"get": map[string]any{}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	if err := c.ValidateContract(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAndListKnowledgeBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/doc.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{
				"/v2.3/Knowledge/KnowbaseItem": map[string]any{"get": map[string]any{}},
			}})
		case "/api.php/v2.3/Knowledge/KnowbaseItem":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"id": 7, "name": "Konto gesperrt", "answer": "<p>Konto entsperren</p>", "date_mod": "2026-07-27 10:00:00",
				"categories": []any{map[string]any{"id": 4}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	path, err := c.DiscoverKnowledgeBasePath(context.Background(), "auto")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/Knowledge/KnowbaseItem" {
		t.Fatalf("path=%q", path)
	}
	items, err := c.ListKnowledgeBaseItems(context.Background(), path, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != 7 || len(items[0].CategoryIDs) != 1 || items[0].CategoryIDs[0] != 4 {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestDiscoverKnowledgeBaseStripsPatchVersionPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/doc.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{
				"/v2.3.0/Knowledge/KnowbaseItem": map[string]any{"get": map[string]any{}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	path, err := c.DiscoverKnowledgeBasePath(context.Background(), "auto")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/Knowledge/KnowbaseItem" {
		t.Fatalf("path=%q, want /Knowledge/KnowbaseItem", path)
	}
}

func TestListKnowledgeBasePaginatesLargeCollections(t *testing.T) {
	const total = 235
	var starts []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Knowledge/KnowbaseItem":
			start, _ := strconv.Atoi(r.URL.Query().Get("start"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			starts = append(starts, start)
			if limit <= 0 || limit > 100 {
				t.Fatalf("unexpected page limit %d", limit)
			}
			end := start + limit
			if end > total {
				end = total
			}
			rows := make([]map[string]any, 0, max(0, end-start))
			for i := start; i < end; i++ {
				rows = append(rows, map[string]any{
					"id": i + 1, "name": fmt.Sprintf("Artikel %d", i+1), "answer": "Inhalt", "date_mod": "2026-09-09 10:00:00",
				})
			}
			_ = json.NewEncoder(w).Encode(rows)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	items, err := c.ListKnowledgeBaseItems(context.Background(), "/Knowledge/KnowbaseItem", total, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != total {
		t.Fatalf("items=%d, want %d", len(items), total)
	}
	wantStarts := []int{0, 100, 200}
	if !reflect.DeepEqual(starts, wantStarts) {
		t.Fatalf("starts=%v, want %v", starts, wantStarts)
	}
}

func TestListKnowledgeBaseContinuesWhenServerCapsPageSize(t *testing.T) {
	const total = 121
	const serverCap = 37
	var starts []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Knowledge/KnowbaseItem":
			start, _ := strconv.Atoi(r.URL.Query().Get("start"))
			requested, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			starts = append(starts, start)
			pageLimit := requested
			if pageLimit > serverCap {
				pageLimit = serverCap
			}
			end := start + pageLimit
			if end > total {
				end = total
			}
			rows := make([]map[string]any, 0, max(0, end-start))
			for i := start; i < end; i++ {
				rows = append(rows, map[string]any{"id": i + 1, "name": fmt.Sprintf("Artikel %d", i+1), "answer": "Inhalt"})
			}
			if len(rows) > 0 {
				w.Header().Set("Content-Range", fmt.Sprintf("items %d-%d/%d", start, end-1, total))
			}
			_ = json.NewEncoder(w).Encode(rows)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	items, err := c.ListKnowledgeBaseItems(context.Background(), "/Knowledge/KnowbaseItem", total, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != total {
		t.Fatalf("items=%d, want %d", len(items), total)
	}
	wantStarts := []int{0, 37, 74, 111}
	if !reflect.DeepEqual(starts, wantStarts) {
		t.Fatalf("starts=%v, want %v", starts, wantStarts)
	}
}

func TestListKnowledgeBaseFailsWhenRequiredDetailCannotBeRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Knowledge/KnowbaseItem":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 7, "name": "Konto gesperrt"}})
		case "/api.php/v2.3/Knowledge/KnowbaseItem/7":
			http.Error(w, "missing right", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	_, err := c.ListKnowledgeBaseItems(context.Background(), "/Knowledge/KnowbaseItem", 50, "")
	if err == nil || !strings.Contains(err.Error(), "knowledge item 7 detail") {
		t.Fatalf("expected explicit detail-read error, got %v", err)
	}
}

func TestAddFollowupPreservesRichHTML(t *testing.T) {
	var gotContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Assistance/Ticket/5/Timeline/Followup":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			gotContent, _ = body["content"].(string)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	rich := `<p><strong>Fett</strong></p><ul><li>Eintrag</li></ul><p><a href="https://example.invalid">Link</a></p>`
	if err := c.AddFollowup(context.Background(), 5, rich, true); err != nil {
		t.Fatal(err)
	}
	if gotContent != rich {
		t.Fatalf("rich HTML changed:\nwant %s\n got %s", rich, gotContent)
	}
}

func TestAddFollowupEscapesPlainText(t *testing.T) {
	var gotContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Assistance/Ticket/5/Timeline/Followup":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			gotContent, _ = body["content"].(string)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	if err := c.AddFollowup(context.Background(), 5, "Hallo <b>nicht HTML</b>\nZeile 2", false); err != nil {
		t.Fatal(err)
	}
	want := "<p>Hallo &lt;b&gt;nicht HTML&lt;/b&gt;<br>Zeile 2</p>"
	if gotContent != want {
		t.Fatalf("plain content not escaped:\nwant %s\n got %s", want, gotContent)
	}
}

func TestTicketPriorityFieldsAndPriorityWrite(t *testing.T) {
	var patchBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Assistance/Ticket/9":
			if r.Method == http.MethodPatch {
				if err := json.NewDecoder(r.Body).Decode(&patchBody); err != nil {
					t.Fatal(err)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": 9})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 9, "name": "Ausfall", "date_creation": "2026-08-01 08:00:00", "date_mod": "2026-08-02 08:00:00",
				"status": map[string]any{"id": 1}, "category": map[string]any{"id": 3}, "priority": 4, "impact": 5, "urgency": 4,
				"entity": map[string]any{"id": 7}, "location": map[string]any{"id": 8}, "time_to_resolve": "2026-08-02 12:00:00",
				"assigned_groups": []any{map[string]any{"id": 12}}, "assigned_users": []any{map[string]any{"id": 22}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	got, err := c.GetTicket(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != 4 || got.Impact != 5 || got.Urgency != 4 || got.EntityID != 7 || got.LocationID != 8 || len(got.AssignedGroups) != 1 || got.AssignedGroups[0] != 12 || len(got.AssignedUsers) != 1 || got.AssignedUsers[0] != 22 {
		t.Fatalf("priority-related fields not decoded: %+v", got)
	}
	if err := c.SetPriority(context.Background(), 9, 5); err != nil {
		t.Fatal(err)
	}
	if patchBody["priority"] != float64(5) {
		t.Fatalf("unexpected priority PATCH body: %#v", patchBody)
	}
}

func TestListEscalationCandidatesUsesOldestFirstQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Assistance/Ticket":
			gotQuery = r.URL.RawQuery
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "name": "Alt", "status": map[string]any{"id": 1}, "date_creation": "2026-08-01 01:00:00"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	items, err := c.ListEscalationCandidates(context.Background(), 25, "status.id==1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("unexpected candidates: %+v", items)
	}
	if gotQuery == "" || !strings.Contains(gotQuery, "sort=date_creation") || !strings.Contains(gotQuery, "order=ASC") || !strings.Contains(gotQuery, "limit=25") || !strings.Contains(gotQuery, "filter=status.id%3D%3D1") {
		t.Fatalf("unexpected escalation query: %s", gotQuery)
	}
}

func TestSetAssignedGroupsMergesConfiguredActorPayload(t *testing.T) {
	var patchBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Assistance/Ticket/12":
			if r.Method != http.MethodPatch {
				t.Fatalf("method=%s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&patchBody); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 12})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	if err := c.SetAssignedGroups(context.Background(), 12, []int64{7, 9, 7, 0}, "assigned_groups"); err != nil {
		t.Fatal(err)
	}
	refs, ok := patchBody["assigned_groups"].([]any)
	if !ok || len(refs) != 2 {
		t.Fatalf("unexpected assignment payload: %#v", patchBody)
	}
	if refs[0].(map[string]any)["id"] != float64(7) || refs[1].(map[string]any)["id"] != float64(9) {
		t.Fatalf("unexpected actor refs: %#v", refs)
	}
}

func TestSetAssignedUserSupportsSingularConfiguredField(t *testing.T) {
	var patchBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Assistance/Ticket/13":
			_ = json.NewDecoder(r.Body).Decode(&patchBody)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 13})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	if err := c.SetAssignedUsers(context.Background(), 13, []int64{4, 8}, "user_tech"); err != nil {
		t.Fatal(err)
	}
	ref, ok := patchBody["user_tech"].(map[string]any)
	if !ok || ref["id"] != float64(8) {
		t.Fatalf("unexpected singular user payload: %#v", patchBody)
	}
}

func TestAddPrivateFollowupSetsPrivacyFlag(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/Assistance/Ticket/14/Timeline/Followup":
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	if err := c.AddPrivateFollowup(context.Background(), 14, "interner Hinweis", false); err != nil {
		t.Fatal(err)
	}
	if body["is_private"] != true || body["content"] != "<p>interner Hinweis</p>" {
		t.Fatalf("unexpected private followup payload: %#v", body)
	}
}

func TestLinkITILObjectRendersConfiguredAdapter(t *testing.T) {
	var body map[string]any
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/v2.3/ITIL/Link/15":
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	if err := c.LinkITILObject(context.Background(), 15, 99, "/ITIL/Link/{{ticket_id}}", `{"source":{"id":{{source_ticket_id}}},"target":{"id":{{major_incident_id}}}}`); err != nil {
		t.Fatal(err)
	}
	if gotPath == "" || body["source"].(map[string]any)["id"] != float64(15) || body["target"].(map[string]any)["id"] != float64(99) {
		t.Fatalf("unexpected link request path=%q body=%#v", gotPath, body)
	}
}

func TestListKnowledgeBaseLinkedItemsFromOpenAPICollection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.php/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "expires_in": 3600})
		case "/api.php/doc.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{
				"/v2.3/Knowledge/KnowbaseItem_Item": map[string]any{"get": map[string]any{}},
			}})
		case "/api.php/v2.3/Knowledge/KnowbaseItem_Item":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"knowbaseitems_id": 12, "itemtype": "Computer", "items_id": 42, "item_name": "NB-042"},
				{"knowbaseitems_id": 12, "itemtype": "Ticket", "items_id": 99},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "v2.3", "cid", "sec", "u", "p", time.Second)
	links, err := c.ListKnowledgeBaseLinkedItems(context.Background(), []int64{12}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(links[12]) != 2 || links[12][0].ItemType != "Computer" || links[12][0].Name != "NB-042" {
		t.Fatalf("unexpected links: %#v", links)
	}
}
