package provider

import (
	"net/http"
	"strconv"
	"time"
)

// The instances of the fake ligo: a request for one is never applied at once,
// as a person decides it (approveIn, or decide in the test).

var instanceBases = map[string]string{
	"IDENTITY_PROVIDER": "/v1/admin/identity-providers",
	"SECRET_STORE":      "/v1/admin/secret-stores",
	"PLATFORM":          "/v1/admin/platforms",
}

func (l *ligoFake) instanceRoutes(typ, name string) {
	h := func(route string, f func(*http.Request) (int, string)) {
		l.fake.mu.Lock()
		l.routes[route] = f
		l.fake.mu.Unlock()
	}
	base := instanceBases[typ] + "/" + name
	h("GET "+base, func(*http.Request) (int, string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		i, ok := l.instances[typ+"/"+name]
		if !ok {
			return problemJSON(404, "not-found", "no such instance")
		}
		out := map[string]any{}
		for k, v := range i {
			out[k] = v
		}
		if l.viewer {
			delete(out, "definition")
			delete(out, "revision")
		}
		if l.quarantined[typ+"/"+name] {
			out["quarantined"] = true
		}
		return 200, jsonOf(out)
	})
	h("PUT "+base, func(r *http.Request) (int, string) { return l.change(r, typ, name, "UPDATE", "") })
	h("DELETE "+base, func(r *http.Request) (int, string) { return l.change(r, typ, name, "DELETE", "") })
}

func (l *ligoFake) instanceChange(r *http.Request, typ, name, op string, body map[string]any) (int, string) {
	md, _ := body["metadata"].(map[string]any)
	if name == "" {
		name, _ = md["name"].(string)
		l.instanceRoutes(typ, name)
	}
	if op == "DELETE" {
		body = map[string]any{}
		if v, err := strconv.ParseInt(r.URL.Query().Get("expectedRevision"), 10, 64); err == nil {
			body["expectedRevision"] = float64(v)
		}
	}
	if cfg, ok := body["config"].(map[string]any); ok {
		if _, bad := cfg["password"]; bad {
			return problemJSON(422, "validation-failed", "config: additional property password is not allowed")
		}
	}
	cur := l.instances[typ+"/"+name]
	switch {
	case cur == nil && op != "CREATE":
		return problemJSON(404, "not-found", "no such instance")
	case cur != nil && op == "CREATE":
		return problemJSON(409, "already-exists", "the instance exists")
	case op == "DELETE" && l.inUse[name]:
		return problemJSON(409, "instance-in-use", "a target or a platform still uses it")
	}
	if cur != nil {
		if rev, ok := body["expectedRevision"].(float64); ok && int64(rev) != cur["revision"].(int64) {
			return problemJSON(409, "revision-conflict", "stale revision")
		}
	}
	l.filings++
	id := l.id()
	cr := map[string]any{"id": id, "resourceType": typ, "resourceName": name, "resourceId": nil, "operation": op, "kind": "STANDARD", "consent": "ALL",
		"status": "PENDING", "dependsOn": nil, "policySha256": "ab", "distinctApproversRequired": true, "payloadSha256": "cd", "requestedBy": "ci",
		"requestedAt": "2026-11-14T10:00:00Z", "expiresAt": "2026-11-15T10:00:00Z", "awaitingApproval": []string{"OWNER"},
		"sides": []any{map[string]any{"side": "OWNER", "jurisdiction": "dc/corp-prod", "basis": "APPROVAL"}}, "payload": body, "level": ""}
	l.crs[id], l.order = cr, append(l.order, id)
	if l.approveIn > 0 {
		time.AfterFunc(l.approveIn, func() { l.decide(id, "APPLIED") })
	}
	return 202, `{"changeRequest":` + jsonOf(cr) + `,"resource":null}`
}

func (l *ligoFake) applyInstance(typ, name, op string, body map[string]any) {
	if op == "DELETE" {
		delete(l.instances, typ+"/"+name)
		return
	}
	i := l.instances[typ+"/"+name]
	if i == nil {
		i = map[string]any{"apiVersion": "ligo.dihedron.org/v1alpha1", "networkFlows": []any{}, "revision": int64(0)}
		l.instances[typ+"/"+name] = i
	}
	i["revision"] = i["revision"].(int64) + 1
	md, _ := body["metadata"].(map[string]any)
	i["name"], i["kind"], i["type"] = name, map[string]string{"IDENTITY_PROVIDER": "IdentityProvider", "SECRET_STORE": "SecretStore", "PLATFORM": "Platform"}[typ], body["type"]
	for _, k := range []string{"jurisdiction", "realm", "displayName", "description", "contacts"} {
		if v, ok := md[k]; ok {
			i[k] = v
		} else {
			delete(i, k)
		}
	}
	def := map[string]any{}
	for k, v := range body {
		if k != "expectedRevision" {
			def[k] = v
		}
	}
	i["definition"] = def
	if f, ok := body["networkFlows"]; ok {
		i["networkFlows"] = f
	}
}
