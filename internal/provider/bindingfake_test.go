package provider

import (
	"net/http"
	"strconv"
)

// The bindings of the fake ligo: a binding is created PROVISIONING and goes
// ACTIVE (or FAILED) after some reads; a revocation removes it.

func (l *ligoFake) bindingRoutes(name string) {
	h := func(route string, f func(*http.Request) (int, string)) {
		l.fake.mu.Lock()
		l.routes[route] = f
		l.fake.mu.Unlock()
	}
	base := "/v1/admin/bindings/" + name
	h("GET "+base, func(*http.Request) (int, string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		b, ok := l.bindings[name]
		if !ok {
			return 404, `{"type":"urn:ligo:problem:not-found","title":"not found","status":404,"detail":"no such binding"}`
		}
		if st := b["status"].(map[string]any); st["phase"] == "PROVISIONING" {
			if l.provisioning > 0 {
				l.provisioning--
			} else {
				st["phase"], st["message"] = "ACTIVE", ""
				if l.failing {
					st["phase"], st["message"] = "FAILED", "provisioning failed: no such role"
				}
			}
		}
		return 200, jsonOf(b)
	})
	h("PUT "+base, func(r *http.Request) (int, string) { return l.change(r, "BINDING", name, "UPDATE", "") })
	h("DELETE "+base, func(r *http.Request) (int, string) { return l.change(r, "BINDING", name, "DELETE", "") })
}

func problemJSON(status int, slug, detail string) (int, string) {
	return status, `{"type":"urn:ligo:problem:` + slug + `","title":"` + slug + `","status":` + strconv.Itoa(status) + `,"detail":` + jsonOf(detail) + `}`
}

// bindingChange files a change request for a binding (the lock held).
func (l *ligoFake) bindingChange(r *http.Request, name, op string, body map[string]any) (int, string) {
	if name == "" {
		name, _ = body["name"].(string)
		l.bindingRoutes(name)
	}
	for _, k := range []string{"kind", "sourceRevision", "targetRevision", "requestedSpec"} {
		if _, ok := body[k]; ok {
			return problemJSON(422, "validation-failed", "kind, sourceRevision, targetRevision and requestedSpec are set by ligo")
		}
	}
	cur := l.bindings[name]
	if op == "CREATE" && l.held[name] {
		return problemJSON(409, "resource-held", "binding \""+name+"\" is held (hold 0194b7a0): a person must lift the hold first")
	}
	if cr := l.breakGlass[name]; cr != "" && op != "CREATE" {
		return problemJSON(409, "break-glass-open", "binding \""+name+"\" has an open break-glass change "+cr)
	}
	if op == "DELETE" && r.Method == http.MethodDelete {
		if _, ok := body["managed"]; ok {
			return problemJSON(422, "validation-failed", `body: json: unknown field "managed"`)
		}
		body = map[string]any{"expectedRevision": float64(0)}
		if v, err := strconv.ParseInt(r.URL.Query().Get("expectedRevision"), 10, 64); err == nil {
			body["expectedRevision"] = float64(v)
		}
	}
	if cur != nil {
		if mb, _ := cur["metadata"].(map[string]any)["managedBy"].(string); mb != "" && mb != l.owner {
			return problemJSON(409, "resource-managed", "managed by another")
		}
		if rev, ok := body["expectedRevision"].(float64); ok && int64(rev) != cur["metadata"].(map[string]any)["revision"].(int64) {
			return problemJSON(409, "revision-conflict", "stale revision")
		}
	} else if op != "CREATE" {
		return problemJSON(404, "not-found", "no such binding")
	}
	l.filings++
	id := l.id()
	cr := map[string]any{"id": id, "resourceType": "BINDING", "resourceName": name, "resourceId": nil, "operation": op, "kind": "STANDARD", "consent": "ALL",
		"status": "PENDING", "dependsOn": nil, "policySha256": "ab", "distinctApproversRequired": false, "payloadSha256": "cd", "requestedBy": "ci",
		"requestedAt": "2026-11-14T10:00:00Z", "expiresAt": "2026-11-15T10:00:00Z", "awaitingApproval": []string{"TARGET"},
		"sides": []any{map[string]any{"side": "TARGET", "jurisdiction": "dc/corp-prod/ledger", "basis": "APPROVAL"}}, "payload": body, "level": ""}
	l.crs[id], l.order = cr, append(l.order, id)
	if l.immediate {
		l.apply(id)
	}
	return 202, `{"changeRequest":` + jsonOf(cr) + `,"resource":null}`
}

func (l *ligoFake) applyBinding(name, op string, body map[string]any) {
	if op == "DELETE" {
		delete(l.bindings, name)
		return
	}
	b := l.bindings[name]
	if b == nil {
		b = map[string]any{"kind": "Binding", "apiVersion": "ligo.dihedron.org/v1alpha1",
			"metadata": map[string]any{"id": "0194b7a0-0000-7000-8000-00000000b1b1", "name": name, "revision": int64(0), "managedBy": nil, "platform": "pg-ledger",
				"platformType": "postgres.v1", "realm": "prod", "createdAt": "2026-11-01T10:00:00Z", "updatedAt": "2026-11-01T10:00:00Z"},
			"status": map[string]any{"phase": "PROVISIONING", "message": ""}}
		l.bindings[name] = b
	}
	md := b["metadata"].(map[string]any)
	md["revision"] = md["revision"].(int64) + 1
	for _, k := range []string{"source", "target", "accessLevel", "parameters", "purpose", "expiresAt", "labels"} {
		if v, ok := body[k]; ok {
			b[k] = v
		} else {
			delete(b, k)
		}
	}
	if _, ok := b["parameters"]; !ok {
		b["parameters"] = map[string]any{"connectionLimit": float64(0)} // what a platform's form defaults to
	}
	if m, ok := body["managed"].(bool); ok {
		if m {
			md["managedBy"] = l.owner
		} else {
			md["managedBy"] = nil
		}
	}
	if op == "UPDATE" {
		b["status"] = map[string]any{"phase": "PROVISIONING", "message": ""}
	}
}
