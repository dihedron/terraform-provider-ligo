package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const testPassword = "s3cret-Pa55word"

// providerSchema is the provider's schema.
func providerSchema(t *testing.T) fwprovider.SchemaResponse {
	t.Helper()
	var resp fwprovider.SchemaResponse
	New("test")().Schema(context.Background(), fwprovider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	return resp
}

// configure runs Configure with the given string attributes (automation_context
// as a map) and returns the response.
func configure(t *testing.T, attrs map[string]string, ctxMap map[string]string) *fwprovider.ConfigureResponse {
	t.Helper()
	sch := providerSchema(t).Schema
	objType := sch.Type().TerraformType(context.Background()).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		switch {
		case name == "automation_context" && ctxMap != nil:
			m := map[string]tftypes.Value{}
			for k, v := range ctxMap {
				m[k] = tftypes.NewValue(tftypes.String, v)
			}
			vals[name] = tftypes.NewValue(typ, m)
		case attrs[name] != "":
			vals[name] = tftypes.NewValue(tftypes.String, attrs[name])
		default:
			vals[name] = tftypes.NewValue(typ, nil)
		}
	}
	req := fwprovider.ConfigureRequest{Config: tfsdk.Config{Schema: sch, Raw: tftypes.NewValue(objType, vals)}}
	resp := &fwprovider.ConfigureResponse{}
	New("test")().Configure(context.Background(), req, resp)
	return resp
}

func errorText(d diag.Diagnostics) string {
	var b strings.Builder
	for _, e := range d.Errors() {
		b.WriteString(e.Summary() + " " + e.Detail() + "\n")
	}
	return b.String()
}

// admin is an admin API that records what it receives; its certificate is the
// CA the tests give the provider.
type admin struct {
	*httptest.Server
	mu   sync.Mutex
	auth []string
	ca   string // path of the PEM file
}

func newAdmin(t *testing.T) *admin {
	t.Helper()
	a := &admin{}
	a.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.auth = append(a.auth, r.Header.Get("Authorization"))
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"policy":{},"sha256":"` + strings.Repeat("a", 64) + `"}`))
	}))
	t.Cleanup(a.Close)
	a.ca = writeCA(t, a.Certificate())
	return a
}

func writeCA(t *testing.T, c *x509.Certificate) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// strangerCA is the PEM file of a CA that vouches for no test server.
func strangerCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "stranger"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return writeCA(t, c)
}

func passwordFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(p, []byte(testPassword+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestSchema is AC-P1, first part: the attributes of spec §3.1, and none that
// could hold a password.
func TestSchema(t *testing.T) {
	attrs := providerSchema(t).Schema.Attributes
	for _, name := range []string{"server_url", "server_id", "ca_cert", "spiffe_socket", "username", "password_file", "approval_timeout", "approval_reference", "automation_context"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("no attribute %s", name)
		}
	}
	for name := range attrs {
		if strings.Contains(name, "password") && name != "password_file" || strings.Contains(name, "token") || strings.Contains(name, "secret") {
			t.Errorf("attribute %s could hold a credential", name)
		}
	}
}

// TestBasicRoundTrip: the provider configured with LDAP credentials makes a
// call that the admin API authenticates, over a server verified by ca_cert.
func TestBasicRoundTrip(t *testing.T) {
	a := newAdmin(t)
	resp := configure(t, map[string]string{"server_url": a.URL, "ca_cert": a.ca, "username": "olga", "password_file": passwordFile(t, 0o600),
		"approval_timeout": "5m", "approval_reference": "CHG-1"}, map[string]string{"job": "42"})
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	s, ok := resp.ResourceData.(*Session)
	if !ok || s == nil || resp.DataSourceData != resp.ResourceData {
		t.Fatalf("session: %T", resp.ResourceData)
	}
	if s.ApprovalTimeout != 5*time.Minute || s.ApprovalReference != "CHG-1" || s.AutomationContext["job"] != "42" {
		t.Errorf("session: %+v", s)
	}
	if c, err := s.Client.GetCharter(context.Background()); err != nil || len(c.Sha256) != 64 {
		t.Fatalf("GetCharter: %v", err)
	}
	if len(a.auth) != 1 || !strings.HasPrefix(a.auth[0], "Basic ") {
		t.Errorf("the server saw %q", a.auth)
	}
}

// TestWrongServerGetsNoCredentials is T-P1: a server that the CA does not
// vouch for is refused before the password is sent.
func TestWrongServerGetsNoCredentials(t *testing.T) {
	a := newAdmin(t)
	resp := configure(t, map[string]string{"server_url": a.URL, "ca_cert": strangerCA(t), "username": "olga", "password_file": passwordFile(t, 0o600)}, nil)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if _, err := resp.ResourceData.(*Session).Client.GetCharter(context.Background()); err == nil {
		t.Fatal("a server with an unknown certificate was accepted")
	}
	if len(a.auth) != 0 {
		t.Errorf("the server received %d requests", len(a.auth))
	}
}

// TestConfigurationErrors: what is refused, with a message that never holds
// the password.
func TestConfigurationErrors(t *testing.T) {
	a := newAdmin(t)
	good := passwordFile(t, 0o600)
	cases := map[string]map[string]string{
		"no server":              {"ca_cert": a.ca, "username": "o", "password_file": good},
		"http":                   {"server_url": "http://x.example:9443", "ca_cert": a.ca, "username": "o", "password_file": good},
		"no authentication":      {"server_url": a.URL, "ca_cert": a.ca},
		"username, no password":  {"server_url": a.URL, "ca_cert": a.ca, "username": "o"},
		"both ways":              {"server_url": a.URL, "ca_cert": a.ca, "username": "o", "password_file": good, "spiffe_socket": "unix:///nonexistent.sock"},
		"readable password file": {"server_url": a.URL, "ca_cert": a.ca, "username": "o", "password_file": passwordFile(t, 0o644)},
		"missing password file":  {"server_url": a.URL, "ca_cert": a.ca, "username": "o", "password_file": "/nonexistent/pw"},
		"bad CA":                 {"server_url": a.URL, "ca_cert": good, "username": "o", "password_file": good},
		"bad timeout":            {"server_url": a.URL, "ca_cert": a.ca, "username": "o", "password_file": good, "approval_timeout": "soon"},
		"timeout too long":       {"server_url": a.URL, "ca_cert": a.ca, "username": "o", "password_file": good, "approval_timeout": "25h"},
		"timeout not positive":   {"server_url": a.URL, "ca_cert": a.ca, "username": "o", "password_file": good, "approval_timeout": "0s"},
		"bad server id":          {"server_url": a.URL, "ca_cert": a.ca, "username": "o", "password_file": good, "server_id": "not a spiffe id"},
	}
	for name, attrs := range cases {
		t.Run(name, func(t *testing.T) {
			resp := configure(t, attrs, nil)
			if !resp.Diagnostics.HasError() {
				t.Fatal("accepted")
			}
			if strings.Contains(errorText(resp.Diagnostics), testPassword) {
				t.Error("the password is in a diagnostic")
			}
		})
	}
}

// TestEnvironment: the server and the socket come from the environment when
// the configuration does not set them.
func TestEnvironment(t *testing.T) {
	a := newAdmin(t)
	t.Setenv("LIGO_SERVER_URL", a.URL)
	resp := configure(t, map[string]string{"ca_cert": a.ca, "username": "olga", "password_file": passwordFile(t, 0o600)}, nil)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if s := resp.ResourceData.(*Session); s.ApprovalTimeout != 10*time.Minute {
		t.Errorf("the default approval timeout is %v", s.ApprovalTimeout)
	}
}

// TestPasswordFileTrimmed: the line end of the file is not part of the password.
func TestPasswordFileTrimmed(t *testing.T) {
	a := newAdmin(t)
	resp := configure(t, map[string]string{"server_url": a.URL, "ca_cert": a.ca, "username": "olga", "password_file": passwordFile(t, 0o600)}, nil)
	if resp.Diagnostics.HasError() {
		t.Fatal(errorText(resp.Diagnostics))
	}
	if _, err := resp.ResourceData.(*Session).Client.GetCharter(context.Background()); err != nil {
		t.Fatal(err)
	}
	user, pass, _ := (&http.Request{Header: http.Header{"Authorization": {a.auth[0]}}}).BasicAuth()
	if user != "olga" || pass != testPassword {
		t.Errorf("the server saw %q:%q", user, pass)
	}
}

// The Workload API path: a socket nobody listens on is an error, not a hang,
// whether the socket is configured or comes from the environment.
func TestUnreachableSpiffeSocket(t *testing.T) {
	a := newAdmin(t)
	sock := "unix://" + filepath.Join(t.TempDir(), "none.sock")
	for name, tc := range map[string]struct {
		m   model
		env bool
	}{
		"attribute":   {m: model{ServerURL: types.StringValue(a.URL), SpiffeSocket: types.StringValue(sock)}},
		"environment": {m: model{ServerURL: types.StringValue(a.URL)}, env: true},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.env {
				t.Setenv("SPIFFE_ENDPOINT_SOCKET", sock)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			if _, err := connect(ctx, tc.m); err == nil || !strings.Contains(err.Error(), "spiffe_socket") {
				t.Fatalf("connect: %v, want a spiffe_socket error", err)
			}
		})
	}
}

// A server_id that is no SPIFFE ID is refused with a password, too.
func TestBadServerIDWithPassword(t *testing.T) {
	a := newAdmin(t)
	resp := configure(t, map[string]string{"server_url": a.URL, "ca_cert": a.ca, "username": "o", "password_file": passwordFile(t, 0o600), "server_id": "not an id"}, nil)
	if !strings.Contains(errorText(resp.Diagnostics), "server_id") {
		t.Fatalf("diagnostics: %s", errorText(resp.Diagnostics))
	}
}
