package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// ligoFake is a ligo that keeps sources and change requests: a filing makes a
// change request that is applied at once (immediate) or when a person decides
// (decide), as ligo does for automation and for people.
type ligoFake struct {
	*fake
	mu        sync.Mutex
	immediate bool                      // a filing is applied at once
	sources   map[string]map[string]any // by name
	crs       map[string]map[string]any // by id
	order     []string
	filings   int // POSTs and PUTs that filed a change request
	owner     string
	serial    int
}

func newLigoFake(t *testing.T) *ligoFake {
	t.Helper()
	l := &ligoFake{fake: newFake(t), immediate: true, sources: map[string]map[string]any{}, crs: map[string]map[string]any{}, owner: "spiffe://prod.example.org/ci/terraform"}
	handle := func(route string, h func(r *http.Request) (int, string)) {
		l.fake.mu.Lock()
		l.routes[route] = h
		l.fake.mu.Unlock()
	}
	handle("POST /v1/admin/sources", l.fileSource)
	handle("GET /v1/admin/change-requests", l.listCRs)
	l.serveCR()
	return l
}

func (l *ligoFake) id() string {
	l.serial++
	return fmt.Sprintf("0194b7a0-0000-7000-8000-%012d", l.serial)
}

// routes for one source are added when it appears.
func (l *ligoFake) route(name string) {
	h := func(route string, f func(*http.Request) (int, string)) {
		l.fake.mu.Lock()
		l.routes[route] = f
		l.fake.mu.Unlock()
	}
	base := "/v1/admin/sources/" + name
	h("GET "+base, func(*http.Request) (int, string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		s, ok := l.sources[name]
		if !ok {
			return 404, `{"type":"urn:ligo:problem:not-found","title":"not found","status":404,"detail":"no such source"}`
		}
		return 200, jsonOf(s)
	})
	h("PUT "+base, func(r *http.Request) (int, string) { return l.change(r, name, "UPDATE") })
	for _, op := range []string{"deprecate", "reactivate", "retire"} {
		h("POST "+base+"/"+op, func(r *http.Request) (int, string) { return l.change(r, name, strings.ToUpper(op)) })
	}
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

func (l *ligoFake) fileSource(r *http.Request) (int, string) {
	return l.change(r, "", "CREATE")
}

// change files a change request for an operation on a source.
func (l *ligoFake) change(r *http.Request, name, op string) (int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	if name == "" {
		name = body["metadata"].(map[string]any)["name"].(string)
		l.route(name)
	}
	if _, ok := body["managed"]; ok && op != "CREATE" && op != "UPDATE" {
		return 422, `{"type":"urn:ligo:problem:validation-failed","title":"invalid","status":422,"detail":"body: json: unknown field \"managed\""}`
	}
	cur := l.sources[name]
	if cur != nil {
		if mb, _ := cur["metadata"].(map[string]any)["managedBy"].(string); mb != "" && mb != l.owner {
			return 409, `{"type":"urn:ligo:problem:resource-managed","title":"resource managed","status":409,"detail":"managed by another"}`
		}
		if rev, ok := body["expectedRevision"].(float64); ok && int64(rev) != cur["metadata"].(map[string]any)["revision"].(int64) {
			return 409, `{"type":"urn:ligo:problem:revision-conflict","title":"conflict","status":409,"detail":"stale revision"}`
		}
	}
	l.filings++
	id := l.id()
	cr := map[string]any{"id": id, "resourceType": "SOURCE", "resourceName": name, "resourceId": nil, "operation": op, "kind": "STANDARD", "consent": "ALL",
		"status": "PENDING", "dependsOn": nil, "policySha256": "ab", "distinctApproversRequired": false, "payloadSha256": "cd", "requestedBy": "ci",
		"requestedAt": "2026-11-14T10:00:00Z", "expiresAt": "2026-11-15T10:00:00Z", "awaitingApproval": []string{"SOURCE"},
		"sides": []any{map[string]any{"side": "SOURCE", "jurisdiction": "dc/corp-prod", "basis": "APPROVAL"}}, "payload": body}
	l.crs[id], l.order = cr, append(l.order, id)
	if l.immediate {
		l.apply(id)
	}
	return 202, `{"changeRequest":` + jsonOf(cr) + `,"resource":null}`
}

// apply applies a change request to the stored source (the lock held).
func (l *ligoFake) apply(id string) {
	cr := l.crs[id]
	cr["status"], cr["awaitingApproval"] = "APPLIED", []string{}
	name, op := cr["resourceName"].(string), cr["operation"].(string)
	body, _ := cr["payload"].(map[string]any)
	s := l.sources[name]
	if s == nil {
		s = map[string]any{"apiVersion": "ligo.dihedron.org/v1alpha1", "kind": "Source", "status": map[string]any{"bindings": 0}, "visibility": map[string]any{"mode": "PUBLIC"},
			"metadata": map[string]any{"id": id, "name": name, "jurisdiction": "dc/corp-prod", "state": "ACTIVE", "revision": int64(0), "managedBy": nil,
				"createdAt": "2026-11-01T10:00:00Z", "createdBy": "x", "updatedAt": "2026-11-02T10:00:00Z", "updatedBy": "x", "pendingChangeRequestId": nil}}
		l.sources[name] = s
	}
	md := s["metadata"].(map[string]any)
	md["revision"] = md["revision"].(int64) + 1
	switch op {
	case "CREATE", "UPDATE":
		for _, k := range []string{"identity", "displayName", "description", "labels"} {
			if v, ok := body[k]; ok {
				s[k] = v
			} else {
				delete(s, k)
			}
		}
		if v, ok := body["visibility"]; ok {
			s["visibility"] = v
		}
		if m, ok := body["managed"].(bool); ok {
			if m {
				md["managedBy"] = l.owner
			} else {
				md["managedBy"] = nil
			}
		}
	case "DEPRECATE":
		md["state"] = "DEPRECATED"
	case "REACTIVATE":
		md["state"] = "ACTIVE"
	case "RETIRE":
		md["state"] = "RETIRED"
	}
}

// decide ends a pending change request as a person would.
func (l *ligoFake) decide(id, status string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if status == "APPLIED" {
		l.apply(id)
		return
	}
	l.crs[id]["status"], l.crs[id]["awaitingApproval"] = status, []string{}
}

func (l *ligoFake) listCRs(r *http.Request) (int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	q := r.URL.Query()
	var items []string
	for _, id := range l.order {
		cr := l.crs[id]
		if s := q.Get("status"); s != "" && cr["status"] != s {
			continue
		}
		if n := q.Get("resourceName"); n != "" && cr["resourceName"] != n {
			continue
		}
		items = append(items, jsonOf(cr))
	}
	return 200, `{"items":[` + strings.Join(items, ",") + `],"nextPageToken":null}`
}

// route for a single change request, by id.
func (l *ligoFake) serveCR() {
	l.fake.mu.Lock()
	defer l.fake.mu.Unlock()
	l.prefix["GET /v1/admin/change-requests/"] = func(r *http.Request) (int, string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		cr, ok := l.crs[id]
		if !ok {
			return 404, `{"type":"urn:ligo:problem:not-found","title":"not found","status":404,"detail":"no such request"}`
		}
		return 200, jsonOf(cr)
	}
}

func (l *ligoFake) count() (filings int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.filings
}

// tfNull is the null state of a schema, before a resource exists.
func tfNull(sch resource.SchemaResponse) tftypes.Value {
	return tftypes.NewValue(sch.Schema.Type().TerraformType(context.Background()), nil)
}
