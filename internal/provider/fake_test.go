package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// fake is an admin API with canned answers, over TLS, that records requests.
type fake struct {
	*httptest.Server
	mu         sync.Mutex
	routes     map[string]func(r *http.Request) (int, string) // "GET /v1/admin/sources"
	prefix     map[string]func(r *http.Request) (int, string) // "GET /v1/admin/change-requests/": by the start of the path
	requests   []string                                       // method, path and query of each request
	ca         string
	retryAfter string // the Retry-After header of a 429
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{routes: map[string]func(*http.Request) (int, string){}, prefix: map[string]func(*http.Request) (int, string){}}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
		h := f.routes[r.Method+" "+r.URL.Path]
		for p, ph := range f.prefix {
			if h == nil && strings.HasPrefix(r.Method+" "+r.URL.Path, p) {
				h = ph
			}
		}
		f.mu.Unlock()
		if h == nil {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"urn:ligo:problem:not-found","title":"not found","status":404,"detail":"nothing at ` + r.URL.Path + `"}`))
			return
		}
		code, body := h(r)
		if code >= 400 && !strings.HasPrefix(body, "{") {
			w.Header().Set("Content-Type", "text/plain")
		} else if code >= 400 {
			w.Header().Set("Content-Type", "application/problem+json")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		if code == http.StatusTooManyRequests && f.retryAfter != "" {
			w.Header().Set("Retry-After", f.retryAfter)
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	f.ca = writeCA(t, f.Certificate())
	return f
}

// on answers a route with a fixed body.
func (f *fake) on(route string, code int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[route] = func(*http.Request) (int, string) { return code, body }
}

func (f *fake) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// session is the provider's session, configured against the fake.
func (f *fake) session(t *testing.T) *Session {
	t.Helper()
	resp := configure(t, map[string]string{"server_url": f.URL, "ca_cert": f.ca, "username": "olga", "password_file": passwordFile(t, 0o600)}, nil)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	return resp.DataSourceData.(*Session)
}

// datasourceNamed builds the data source of a type name, configured with s.
func datasourceNamed(t *testing.T, name string, s *Session) datasource.DataSource {
	t.Helper()
	for _, mk := range dataSources() {
		d := mk()
		var m datasource.MetadataResponse
		d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "ligo"}, &m)
		if m.TypeName == name {
			if c, ok := d.(datasource.DataSourceWithConfigure); ok {
				c.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: s}, &datasource.ConfigureResponse{})
			}
			return d
		}
	}
	t.Fatalf("no data source %s", name)
	return nil
}

// read reads the data source name with the configuration cfg (strings, bools
// and int64s by attribute; the others are null).
func read(t *testing.T, name string, s *Session, cfg map[string]any) (tfsdk.State, diag.Diagnostics) {
	t.Helper()
	d := datasourceNamed(t, name, s)
	var sch datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &sch)
	obj := sch.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for k, typ := range obj.AttributeTypes {
		v, ok := cfg[k]
		if !ok {
			vals[k] = tftypes.NewValue(typ, nil)
			continue
		}
		vals[k] = tftypes.NewValue(typ, v)
	}
	raw := tftypes.NewValue(obj, vals)
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: sch.Schema, Raw: tftypes.NewValue(obj, nil)}}
	d.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{Schema: sch.Schema, Raw: raw}}, &resp)
	return resp.State, resp.Diagnostics
}

// attr reads one attribute of a state as a Go value of a type.
func attrOf[T any](t *testing.T, st tfsdk.State, p path.Path) T {
	t.Helper()
	var v T
	if d := st.GetAttribute(context.Background(), p, &v); d.HasError() {
		t.Fatalf("%s: %s", p, errorText(d))
	}
	return v
}
