package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dihedron/ligo/pkg/adminclient"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// Default and largest time a write waits for its change request (spec §3.1).
const (
	defaultApprovalTimeout = 10 * time.Minute
	maxApprovalTimeout     = 24 * time.Hour
)

// Session is what Configure hands to every resource and data source: the
// admin client and the settings of every filing.
type Session struct {
	Client            *adminclient.Client
	Server            string // the URL, for logs
	ApprovalTimeout   time.Duration
	ApprovalReference string
	AutomationContext map[string]any
}

func env(name string) string { return os.Getenv(name) }

// connect validates the configuration and builds the session. It makes no
// call to the server. Its errors name an attribute and never hold a password.
func connect(ctx context.Context, m model) (*Session, error) {
	server := first(m.ServerURL.ValueString(), env("LIGO_SERVER_URL"))
	if u, err := url.Parse(server); server == "" || err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("server_url (or LIGO_SERVER_URL): an https:// URL is required")
	}
	s := &Session{Server: server, ApprovalTimeout: defaultApprovalTimeout, ApprovalReference: m.ApprovalReference.ValueString()}
	if v := m.ApprovalTimeout.ValueString(); v != "" {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			return nil, fmt.Errorf("approval_timeout %q: a duration such as 10m is required", v)
		case d <= 0 || d > maxApprovalTimeout:
			return nil, fmt.Errorf("approval_timeout %q: more than 0 and at most %v is required", v, maxApprovalTimeout)
		}
		s.ApprovalTimeout = d
	}
	if !m.AutomationContext.IsNull() && !m.AutomationContext.IsUnknown() {
		s.AutomationContext = map[string]any{}
		for k, v := range m.AutomationContext.Elements() {
			s.AutomationContext[k] = strings.Trim(v.String(), `"`)
		}
	}

	cfg := adminclient.Config{ServerURL: strings.TrimSuffix(server, "/"), UserAgent: "terraform-provider-ligo"}
	if m.CACert.ValueString() != "" {
		pem, err := os.ReadFile(m.CACert.ValueString())
		if err != nil {
			return nil, fmt.Errorf("ca_cert: %w", err)
		}
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ca_cert %s: no certificate", m.CACert.ValueString())
		}
	}

	socket := m.SpiffeSocket.ValueString()
	user := m.Username.ValueString()
	switch {
	case user != "" && socket != "":
		return nil, errors.New("username and spiffe_socket: set one of them, not both")
	case user == "" && socket == "":
		socket = env("SPIFFE_ENDPOINT_SOCKET")
		if socket == "" {
			return nil, errors.New("no way to authenticate: set spiffe_socket (or SPIFFE_ENDPOINT_SOCKET), or username and password_file")
		}
	}
	if user != "" {
		password, err := readPassword(m.PasswordFile.ValueString())
		if err != nil {
			return nil, err
		}
		cfg.Basic = &adminclient.BasicAuth{Username: user, Password: adminclient.Secret(password)}
		if cfg.RootCAs == nil {
			pool, err := x509.SystemCertPool()
			if err != nil {
				return nil, fmt.Errorf("the system's certificates: %w", err)
			}
			cfg.RootCAs = pool
		}
	} else {
		src, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(socket)))
		if err != nil {
			return nil, fmt.Errorf("spiffe_socket: %w", err)
		}
		cfg.X509Source = src
		if cfg.RootCAs == nil {
			id := m.ServerID.ValueString()
			if id == "" {
				svid, err := src.GetX509SVID()
				if err != nil {
					_ = src.Close()
					return nil, fmt.Errorf("spiffe_socket: %w", err)
				}
				id = "spiffe://" + svid.ID.TrustDomain().Name() + "/ns/sys/sa/ligo"
			}
			cfg.ServerID, cfg.Bundles = id, src
		}
	}
	if id := m.ServerID.ValueString(); id != "" {
		if _, err := spiffeid.FromString(id); err != nil {
			return nil, fmt.Errorf("server_id %q: %w", id, err)
		}
		cfg.ServerID = id
	}

	c, err := adminclient.New(cfg)
	if err != nil {
		return nil, err
	}
	s.Client = c
	return s, nil
}

// readPassword reads the LDAP password from path, which nobody but its owner
// may be able to read (the CLI's rule).
func readPassword(path string) (string, error) {
	if path == "" {
		return "", errors.New("password_file: required with username")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("password_file: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("password_file %s must not be accessible by group or others (mode %v)", path, info.Mode().Perm())
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: the operator's own password file, named in the configuration
	if err != nil {
		return "", fmt.Errorf("password_file: %w", err)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
