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
	"time"
)

// ligoFake is a ligo that keeps sources and change requests: a filing makes a
// change request that is applied at once (immediate) or when a person decides
// (decide), as ligo does for automation and for people.
type ligoFake struct {
	*fake
	mu           sync.Mutex
	immediate    bool                      // a filing is applied at once
	sources      map[string]map[string]any // by name
	targets      map[string]map[string]any // by name
	crs          map[string]map[string]any // by id
	order        []string
	filings      int // POSTs and PUTs that filed a change request
	owner        string
	serial       int
	bindings     map[string]map[string]any // by name
	instances    map[string]map[string]any // by kind and name ("IDENTITY_PROVIDER/vault-prod")
	approveIn    time.Duration             // an instance request is approved by a person after this long (0: it stays pending)
	inUse        map[string]bool           // instances that cannot be retired
	viewer       bool                      // the caller may not file on instances: no definition, no revision
	quarantined  map[string]bool
	unavailable  int               // GETs of an instance that answer 503 (its plugin is not serving yet)
	held         map[string]bool   // bindings that a hold stops from being created
	breakGlass   map[string]string // bindings under a break-glass review, with its change request
	provisioning int               // GETs of a new binding that say PROVISIONING before ACTIVE
	failing      bool              // a binding ends FAILED
	refuse       map[int]int       // a status to answer to the next n filings, before they are processed (429, 503)
	attempts     int               // every POST and PUT that arrived, filed or refused
	refuseReads  int               // the next n reads of change requests are answered 429, as ligo's rate limit does
}

func newLigoFake(t *testing.T) *ligoFake {
	t.Helper()
	l := &ligoFake{fake: newFake(t), immediate: true, bindings: map[string]map[string]any{}, instances: map[string]map[string]any{}, inUse: map[string]bool{}, quarantined: map[string]bool{}, held: map[string]bool{}, breakGlass: map[string]string{}, sources: map[string]map[string]any{}, targets: map[string]map[string]any{}, crs: map[string]map[string]any{}, owner: "spiffe://prod.example.org/ci/terraform"}
	handle := func(route string, h func(r *http.Request) (int, string)) {
		l.fake.mu.Lock()
		l.routes[route] = h
		l.fake.mu.Unlock()
	}
	handle("POST /v1/admin/sources", func(r *http.Request) (int, string) { return l.change(r, "SOURCE", "", "CREATE", "") })
	handle("POST /v1/admin/targets", func(r *http.Request) (int, string) { return l.change(r, "TARGET", "", "CREATE", "") })
	handle("POST /v1/admin/bindings", func(r *http.Request) (int, string) { return l.change(r, "BINDING", "", "CREATE", "") })
	for typ, base := range instanceBases {
		handle("POST "+base, func(r *http.Request) (int, string) { return l.change(r, typ, "", "CREATE", "") })
	}
	handle("GET /v1/admin/change-requests", l.listCRs)
	l.serveCR()
	return l
}

func (l *ligoFake) id() string {
	l.serial++
	return fmt.Sprintf("0194b7a0-0000-7000-8000-%012d", l.serial)
}

// route adds the routes of one source or target when it appears.
func (l *ligoFake) route(typ, name string) {
	h := func(route string, f func(*http.Request) (int, string)) {
		l.fake.mu.Lock()
		l.routes[route] = f
		l.fake.mu.Unlock()
	}
	path, store := "sources", l.sources
	if typ == "TARGET" {
		path, store = "targets", l.targets
	}
	base := "/v1/admin/" + path + "/" + name
	h("GET "+base, func(*http.Request) (int, string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		s, ok := store[name]
		if !ok {
			return 404, `{"type":"urn:ligo:problem:not-found","title":"not found","status":404,"detail":"no such resource"}`
		}
		return 200, jsonOf(s)
	})
	h("PUT "+base, func(r *http.Request) (int, string) { return l.change(r, typ, name, "UPDATE", "") })
	for _, op := range []string{"deprecate", "reactivate", "retire"} {
		h("POST "+base+"/"+op, func(r *http.Request) (int, string) { return l.change(r, typ, name, strings.ToUpper(op), "") })
		if typ == "TARGET" {
			for _, level := range []string{"read", "write", "extra"} {
				h("POST "+base+"/access-levels/"+level+"/"+op, func(r *http.Request) (int, string) {
					return l.change(r, typ, name, strings.ToUpper(op)+"_LEVEL", level)
				})
			}
		}
	}
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

// change files a change request for an operation on a source.
func (l *ligoFake) change(r *http.Request, typ, name, op, level string) (int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts++
	for status, n := range l.refuse {
		if n > 0 {
			l.refuse[status]--
			return status, `{"type":"urn:ligo:problem:rate-limited","title":"slow down","status":` + fmt.Sprint(status) + `}`
		}
	}
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	if typ == "BINDING" {
		return l.bindingChange(r, name, op, body)
	}
	if _, ok := instanceBases[typ]; ok {
		return l.instanceChange(r, typ, name, op, body)
	}
	if name == "" {
		name = body["metadata"].(map[string]any)["name"].(string)
		l.route(typ, name)
	}
	store := l.sources
	if typ == "TARGET" {
		store = l.targets
	}
	if _, ok := body["managed"]; ok && op != "CREATE" && op != "UPDATE" {
		return 422, `{"type":"urn:ligo:problem:validation-failed","title":"invalid","status":422,"detail":"body: json: unknown field \"managed\""}`
	}
	cur := store[name]
	if cur != nil && op == "CREATE" {
		return 409, `{"type":"urn:ligo:problem:invalid-transition","title":"invalid transition","status":409,"detail":"metadata.name \"` + name + `\" is in use: a change of it needs expectedRevision"}`
	}
	if cur != nil {
		if mb, _ := cur["metadata"].(map[string]any)["managedBy"].(string); mb != "" && mb != l.owner {
			return 409, `{"type":"urn:ligo:problem:resource-managed","title":"resource managed","status":409,"detail":"managed by another"}`
		}
		if rev, ok := body["expectedRevision"].(float64); ok && int64(rev) != cur["metadata"].(map[string]any)["revision"].(int64) {
			return 409, `{"type":"urn:ligo:problem:revision-conflict","title":"conflict","status":409,"detail":"stale revision"}`
		}
	}
	if typ == "TARGET" && op == "UPDATE" && cur != nil {
		sent := map[string]bool{}
		for _, e := range body["accessLevels"].([]any) {
			sent[e.(map[string]any)["name"].(string)] = true
		}
		for _, e := range cur["accessLevels"].([]any) {
			if n := e.(map[string]any)["name"].(string); !sent[n] {
				return 422, `{"type":"urn:ligo:problem:validation-failed","title":"invalid","status":422,"detail":"accessLevels: ` + n + ` cannot be removed; retire it instead"}`
			}
		}
	}
	l.filings++
	id := l.id()
	cr := map[string]any{"id": id, "resourceType": typ, "resourceName": name, "resourceId": nil, "operation": op, "kind": "STANDARD", "consent": "ALL",
		"status": "PENDING", "dependsOn": nil, "policySha256": "ab", "distinctApproversRequired": false, "payloadSha256": "cd", "requestedBy": l.owner,
		"requestedAt": "2026-11-14T10:00:00Z", "expiresAt": "2026-11-15T10:00:00Z", "awaitingApproval": []string{"SOURCE"},
		"sides": []any{map[string]any{"side": "SOURCE", "jurisdiction": "dc/corp-prod", "basis": "APPROVAL"}}, "payload": body, "level": level}
	l.crs[id], l.order = cr, append(l.order, id)
	if l.immediate {
		l.apply(id)
	}
	return 202, `{"changeRequest":` + jsonOf(cr) + `,"resource":null}`
}

// apply applies a change request to the stored resource (the lock held).
func (l *ligoFake) apply(id string) {
	cr := l.crs[id]
	cr["status"], cr["awaitingApproval"] = "APPLIED", []string{}
	typ, name, op, level := cr["resourceType"].(string), cr["resourceName"].(string), cr["operation"].(string), cr["level"].(string)
	body, _ := cr["payload"].(map[string]any)
	if typ == "BINDING" {
		l.applyBinding(name, op, body)
		return
	}
	if _, ok := instanceBases[typ]; ok {
		l.applyInstance(typ, name, op, body)
		return
	}
	store := l.sources
	if typ == "TARGET" {
		store = l.targets
	}
	s := store[name]
	if s == nil {
		s = map[string]any{"apiVersion": "ligo.dihedron.org/v1alpha1", "kind": "Source", "status": map[string]any{"bindings": 0}, "visibility": map[string]any{"mode": "PUBLIC"},
			"metadata": map[string]any{"id": id, "name": name, "jurisdiction": "dc/corp-prod", "state": "ACTIVE", "revision": int64(0), "managedBy": nil,
				"createdAt": "2026-11-01T10:00:00Z", "createdBy": "x", "updatedAt": "2026-11-02T10:00:00Z", "updatedBy": "x", "pendingChangeRequestId": nil}}
		if typ == "TARGET" {
			s["kind"], s["accessLevels"] = "Target", []any{}
			md := s["metadata"].(map[string]any)
			md["platform"], md["platformJurisdiction"], md["platformType"], md["realm"] = "pg", "dc/corp-prod", "postgres.v1", "prod"
			md["jurisdiction"] = "dc/corp-prod/ledger"
		}
		store[name] = s
	}
	md := s["metadata"].(map[string]any)
	md["revision"] = md["revision"].(int64) + 1
	switch op {
	case "CREATE", "UPDATE":
		keys := []string{"identity", "displayName", "description", "annotations"}
		if typ == "TARGET" {
			keys = []string{"definition", "accessLevels", "displayName", "description", "documentationUrl", "annotations", "networkFlows"}
			if m, ok := body["metadata"].(map[string]any); ok {
				md["platform"] = m["platform"]
				if j, ok := m["jurisdiction"].(string); ok {
					md["jurisdiction"] = j
				}
			}
		}
		for _, k := range keys {
			if v, ok := body[k]; ok {
				if k == "accessLevels" { // ligo keeps each level's state, ACTIVE for a new one
					old := map[string]any{}
					for _, o := range s["accessLevels"].([]any) {
						old[o.(map[string]any)["name"].(string)] = o.(map[string]any)["state"]
					}
					var lv []any
					for _, e := range v.([]any) {
						m := e.(map[string]any)
						if _, given := m["state"]; !given {
							m["state"] = "ACTIVE"
							if st, ok := old[m["name"].(string)]; ok {
								m["state"] = st
							}
						}
						lv = append(lv, m)
					}
					v = lv
				}
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
	case "DEPRECATE_LEVEL", "REACTIVATE_LEVEL", "RETIRE_LEVEL":
		levels, _ := s["accessLevels"].([]any)
		var kept []any
		for _, lv := range levels {
			m := lv.(map[string]any)
			if m["name"] == level {
				switch op {
				case "DEPRECATE_LEVEL":
					m["state"] = "DEPRECATED"
				case "REACTIVATE_LEVEL":
					m["state"] = "ACTIVE"
				case "RETIRE_LEVEL":
					m["state"] = "RETIRED"
				}
			}
			kept = append(kept, m)
		}
		s["accessLevels"] = kept
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
	if l.refuseReads > 0 {
		l.refuseReads--
		return http.StatusTooManyRequests, `{"type":"urn:ligo:problem:rate-limited","title":"slow down","status":429}`
	}
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

// seedSource makes a source exist with no change request in the list: one that
// somebody else, or an older run, filed.
func (l *ligoFake) seedSource(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.id()
	l.crs[id] = map[string]any{"id": id, "resourceType": "SOURCE", "resourceName": name, "operation": "CREATE", "level": "",
		"payload": map[string]any{"metadata": map[string]any{"name": name}, "identity": map[string]any{"trustDomain": "prod.lab", "svidRegex": "^z$"}, "visibility": map[string]any{"mode": "PRIVATE"}}}
	l.apply(id)
	delete(l.crs, id)
}
