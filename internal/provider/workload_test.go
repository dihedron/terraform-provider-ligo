package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/spiffe/go-spiffe/v2/proto/spiffe/workload"
	"google.golang.org/grpc"
)

// workloadAPI is a SPIFFE Workload API with one SVID, on a Unix socket.
type workloadAPI struct {
	workload.UnimplementedSpiffeWorkloadAPIServer
	resp *workload.X509SVIDResponse
}

func (w *workloadAPI) FetchX509SVID(_ *workload.X509SVIDRequest, s grpc.ServerStreamingServer[workload.X509SVIDResponse]) error {
	if err := s.Send(w.resp); err != nil {
		return err
	}
	<-s.Context().Done()
	return nil
}

func startWorkloadAPI(t *testing.T) string {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := &url.URL{Scheme: "spiffe", Host: "example.org", Path: "/ns/pipeline/sa/terraform"}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		URIs: []*url.URL{id}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "agent.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	workload.RegisterSpiffeWorkloadAPIServer(srv, &workloadAPI{resp: &workload.X509SVIDResponse{Svids: []*workload.X509SVID{
		{SpiffeId: id.String(), X509Svid: leafDER, X509SvidKey: keyDER, Bundle: caDER}}}})
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(srv.Stop)
	return "unix://" + sock
}

// With a Workload API the provider presents the SVID, and verifies ligo by
// its SPIFFE ID: the one that is given, or the one of ligo in the caller's
// trust domain.
func TestSpiffeSession(t *testing.T) {
	sock := startWorkloadAPI(t)
	a := newAdmin(t)
	for name, id := range map[string]types.String{"derived server id": types.StringNull(), "given server id": types.StringValue("spiffe://example.org/ns/sys/sa/ligo")} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s, err := connect(ctx, model{ServerURL: types.StringValue(a.URL), SpiffeSocket: types.StringValue(sock), ServerID: id})
			if err != nil || s.Client == nil {
				t.Fatalf("connect: %v", err)
			}
		})
	}
}
