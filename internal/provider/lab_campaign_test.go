//go:build lab

package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type terraformState = terraform.State

// The pre-release campaign (.specs/ligo-prerelease-campaign.md, phase 2). The
// scenarios need people and incidents: an approver who decides, a person who
// edits, an operator who contains a binding, a replica that dies. The tests run
// on ligo1 and cannot do those; lab.sh iac campaign does, on request: labAct
// writes a request file in the test's directory, lab.sh performs it (as the
// right person, with the CLI) and writes the answer. "drain" (ack at once) keeps
// approving the operator's requests for a few minutes, for the removal that
// ends a scenario.

var actSeq atomic.Int64

// labAct asks lab.sh to do something and waits for it; "ok" is the only answer
// that passes.
func labAct(t testing.TB, args ...string) {
	t.Helper()
	if err := labActErr(args...); err != nil {
		t.Fatalf("lab action %v: %v", args, err)
	}
}

func labActErr(args ...string) error {
	dir := os.Getenv("TMPDIR")
	id := fmt.Sprintf("%d-%d", os.Getpid(), actSeq.Add(1))
	req, ack := filepath.Join(dir, "req-"+id), filepath.Join(dir, "ack-"+id)
	if err := os.WriteFile(req+".tmp", []byte(strings.Join(args, " ")+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(req+".tmp", req); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Clean(ack)); err == nil && strings.HasSuffix(string(b), "\n") {
			if got := strings.TrimSpace(string(b)); got != "ok" {
				return fmt.Errorf("%s", got)
			}
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("no answer in 10 minutes")
}

// labActAsync runs the action while the next step applies; its failure fails the test.
func labActAsync(t *testing.T, args ...string) func() {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- labActErr(args...) }()
	return func() {
		t.Helper()
		if err := <-done; err != nil {
			t.Errorf("lab action %v: %v", args, err)
		}
	}
}

// labSettle waits for the asynchronous actions that a step started.
func labSettle(waits ...func()) func() {
	return func() {
		for _, w := range waits {
			w()
		}
	}
}

// labCampaign skips a scenario that lab.sh iac test runs alone: it needs the
// responder of lab.sh iac campaign, and waits for it otherwise.
func labCampaign(t *testing.T) {
	t.Helper()
	if os.Getenv("LIGO_LAB_CAMPAIGN") == "" {
		t.Skip("run by lab.sh iac campaign")
	}
}

func labProviderAt(t *testing.T, server, timeout string) string {
	t.Helper()
	return fmt.Sprintf(`
provider "ligo" {
  server_url         = %q
  spiffe_socket      = %q
  server_id          = %q
  approval_timeout   = %q
  approval_reference = "CHG-IAC-LAB"
}
`, server, labEnv(t, "LIGO_LAB_SOCKET"), labEnv(t, "LIGO_LAB_SERVER_ID"), timeout)
}

func campaignSource(name, desc string) string {
	return labSource(name, `^spiffe://prod\.lab/ns/cmp/sa/`+name+`$`, desc, "managed = false")
}

// TestLabCampaignApproval runs as an operator: its writes wait for people. An
// approver approves while the apply waits; another request is declined; one
// is withdrawn by its requester; a request that stays open blocks a change of
// another content until it is decided. Every step counts the requests.
func TestLabCampaignApproval(t *testing.T) {
	labCampaign(t)
	name := "cmp-ap-" + labEnv(t, "LIGO_E2E_SUFFIX")
	src := func(timeout, desc string) string { return labProvider(t, timeout) + campaignSource(name, desc) }
	var settle func()
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			// P2: an approver approves while the apply waits
			{PreConfig: func() { settle = labSettle(labActAsync(t, "approve", "source", name)) }, Config: src("3m", "v1"),
				Check: func(*terraformState) error { settle(); labAct(t, "count", "source", name, "1"); return nil }},
			// P4: declined
			{PreConfig: func() { settle = labSettle(labActAsync(t, "decline", "source", name)) }, Config: src("3m", "v2"),
				ExpectError: regexp.MustCompile(`(?s)ended\s+REJECTED`)},
			{PreConfig: func() { settle(); labAct(t, "count", "source", name, "2") }, Config: src("3m", "v1"), PlanOnly: true},
			// P4: withdrawn by its requester
			{PreConfig: func() { settle = labSettle(labActAsync(t, "withdraw", "source", name)) }, Config: src("3m", "v2"),
				ExpectError: regexp.MustCompile(`(?s)ended\s+WITHDRAWN`)},
			{PreConfig: func() { settle(); labAct(t, "count", "source", name, "3") }, Config: src("3m", "v1"), PlanOnly: true},
			// P3: the time-out leaves the request open; P5: another content is refused until it is decided
			{Config: src("6s", "v2"), ExpectError: regexp.MustCompile(`(?s)still\s+PENDING`)},
			{Config: src("6s", "v3"), ExpectError: regexp.MustCompile(`(?s)pending\s+with\s+another\s+content`)},
			{PreConfig: func() { labAct(t, "count", "source", name, "4"); labAct(t, "decline", "source", name) }, Config: src("3m", "v1"), PlanOnly: true},
			// the same change again: converges, one new request
			{PreConfig: func() { settle = labSettle(labActAsync(t, "approve", "source", name)) }, Config: src("3m", "v3"),
				Check: func(*terraformState) error {
					settle()
					labAct(t, "count", "source", name, "5")
					labAct(t, "drain")
					return nil
				}},
		},
	})
}

// TestLabCampaignHold runs as the pipeline: a person contains the binding (a
// hold), the apply that would create it again fails and names the hold, the
// person lifts it, the next apply converges (T-P14).
func TestLabCampaignHold(t *testing.T) {
	labCampaign(t)
	suffix := labEnv(t, "LIGO_E2E_SUFFIX")
	binding := "cmp-hb-" + suffix
	cfg := labProvider(t, "3m") + fmt.Sprintf(`
resource "ligo_source" "s" {
  name         = "cmp-hs-%[1]s"
  trust_domain = "prod.lab"
  svid_regex   = "^spiffe://prod\\.lab/ns/cmp/sa/h%[1]s$"
}
resource "ligo_target" "t" {
  name          = "cmp-ht-%[1]s"
  platform      = "pg-ledger"
  jurisdiction  = "dc/corp-prod/ledger"
  definition    = jsonencode({ database = "ledger", schema = "ledger" })
  access_levels = { read = { grants = jsonencode({ roles = ["LEDGER_RO_ROLE"] }) } }
  network_flows = jsonencode([{ name = "ledger", purpose = "DATA", protocol = "TCP", destination = "pg.ligo.lab", ports = [5432], tls = true, serverName = "pg.ligo.lab" }])
}
resource "ligo_binding" "b" {
  name         = %[2]q
  source       = ligo_source.s.name
  target       = ligo_target.t.name
  access_level = "read"
}
`, suffix, binding)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttr("ligo_binding.b", "phase", "ACTIVE")},
			{PreConfig: func() { labAct(t, "contain", binding) }, Config: cfg, ExpectError: regexp.MustCompile(`(?s)is\s+held`)},
			// the provider must not have filed anything while the hold stood: no request for the binding is pending
			{PreConfig: func() { labAct(t, "nopending", "binding", binding) }, Config: cfg, ExpectError: regexp.MustCompile(`(?s)is\s+held`)},
			{PreConfig: func() { labAct(t, "lift", binding) }, Config: cfg, Check: resource.TestCheckResourceAttr("ligo_binding.b", "phase", "ACTIVE")},
		},
	})
}

// TestLabCampaignLock runs as the pipeline: a person's change of what it owns
// is refused; its release (managed = false) lets a person edit; adopting it
// again needs the sides' people (P1, P8).
func TestLabCampaignLock(t *testing.T) {
	labCampaign(t)
	owner := labEnv(t, "LIGO_LAB_OWNER")
	name := "cmp-lk-" + labEnv(t, "LIGO_E2E_SUFFIX")
	src := func(desc, managed string) string {
		return labProvider(t, "3m") + labSource(name, `^spiffe://prod\.lab/ns/cmp/sa/`+name+`$`, desc, managed)
	}
	var settle func()
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: src("v1", ""), Check: resource.TestCheckResourceAttr("ligo_source.s", "managed_by", owner)},
			{PreConfig: func() { labAct(t, "edit-refused", "source", name) }, Config: src("v1", ""), PlanOnly: true},
			{Config: src("v1", "managed = false"), Check: resource.TestCheckNoResourceAttr("ligo_source.s", "managed_by")},
			// a person edits the released source; the pipeline sees the drift
			{PreConfig: func() { labAct(t, "edit", "source", name, "edited-by-a-person") }, Config: src("v1", "managed = false"), ExpectNonEmptyPlan: true, PlanOnly: true},
			// adoption: the sides' people consent while the apply waits
			{PreConfig: func() { settle = labSettle(labActAsync(t, "approve", "source", name)) }, Config: src("v2", "managed = true"),
				Check: func(*terraformState) error { settle(); return nil }},
			{Config: src("v2", "managed = true"), Check: resource.TestCheckResourceAttr("ligo_source.s", "managed_by", owner)},
		},
	})
}

// TestLabCampaignRestart runs as an operator: ligo is restarted while the
// apply waits for a person; the wait survives (P11).
func TestLabCampaignRestart(t *testing.T) {
	labCampaign(t)
	name := "cmp-rs-" + labEnv(t, "LIGO_E2E_SUFFIX")
	var settle func()
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			PreConfig: func() {
				restart := labActAsync(t, "restart-ligo", "ligo1")
				approve := labActAsync(t, "approve-after-restart", "source", name)
				settle = labSettle(restart, approve)
			},
			Config: labProvider(t, "5m") + campaignSource(name, "v1"),
			Check: func(*terraformState) error {
				settle()
				labAct(t, "count", "source", name, "1")
				labAct(t, "drain")
				return nil
			},
		}},
	})
}

// TestLabCampaignCrash: the provider process is killed after it filed (P13,
// AC-P4); LIGO_LAB_PHASE=file dies, =adopt runs again and waits for the same
// request, which lab.sh approves; the requests are counted.
func TestLabCampaignCrash(t *testing.T) {
	labCampaign(t)
	phase := labEnv(t, "LIGO_LAB_PHASE")
	name := "cmp-cr-" + labEnv(t, "LIGO_E2E_SUFFIX")
	switch phase {
	case "file":
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: protoV6,
			Steps: []resource.TestStep{{
				PreConfig: func() {
					go func() {
						if err := labActErr("pending", "source", name); err == nil {
							_ = syscall.Kill(os.Getpid(), syscall.SIGKILL) // the provider is this process
						}
					}()
				},
				Config: labProvider(t, "3m") + campaignSource(name, "crash"),
			}},
		})
		t.Fatal("the process was not killed after the filing")
	case "adopt":
		var settle func()
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: protoV6,
			Steps: []resource.TestStep{{
				PreConfig: func() { settle = labSettle(labActAsync(t, "approve", "source", name)) },
				Config:    labProvider(t, "3m") + campaignSource(name, "crash"),
				Check: func(*terraformState) error {
					settle()
					labAct(t, "count", "source", name, "1")
					labAct(t, "drain")
					return nil
				},
			}},
		})
	default:
		t.Fatalf("phase %q", phase)
	}
}

// TestLabCampaignReplica runs as the pipeline, through the load balancer: the
// replica that serves it is killed while it applies a set of sources; the
// apply ends (it may fail with a message), and a second apply converges with
// one change request per source (P12).
const replicaSources = 60

func TestLabCampaignReplica(t *testing.T) {
	labCampaign(t)
	suffix := labEnv(t, "LIGO_E2E_SUFFIX")
	lb := labEnv(t, "LIGO_LAB_LB_URL")
	var b strings.Builder
	for i := range replicaSources {
		fmt.Fprintf(&b, `
resource "ligo_source" "s%[1]d" {
  name         = "cmp-rp%[1]d-%[2]s"
  trust_domain = "prod.lab"
  svid_regex   = "^spiffe://prod\\.lab/ns/cmp/sa/rp%[1]d%[2]s$"
}
`, i, suffix)
	}
	cfg := labProviderAt(t, lb, "3m") + b.String()
	var settle func()
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		// the replica dies while the apply files: the apply fails with a message when a write was in
		// flight (it is never retried), or succeeds when the load balancer had moved the rest to ligo2
		ErrorCheck: func(err error) error { t.Logf("the apply while the replica died: %v", err); return nil },
		Steps: []resource.TestStep{
			{PreConfig: func() { settle = labSettle(labActAsync(t, "kill-ligo", "ligo1")) }, Config: cfg},
			{PreConfig: func() { settle(); labAct(t, "start-ligo", "ligo1") }, Config: cfg,
				Check: func(*terraformState) error {
					for i := range replicaSources {
						labAct(t, "count", "source", fmt.Sprintf("cmp-rp%d-%s", i, suffix), "1")
					}
					return nil
				}},
		},
	})
}

// TestLabCampaignBurst runs as the pipeline: forty sources at once meet ligo's
// rate limit; the bounded retry of a refused filing absorbs it, or the apply
// says so, and no source is filed twice (P14).
func TestLabCampaignBurst(t *testing.T) {
	labCampaign(t)
	suffix := labEnv(t, "LIGO_E2E_SUFFIX")
	var b strings.Builder
	const n = 40
	for i := range n {
		fmt.Fprintf(&b, `
resource "ligo_source" "s%[1]d" {
  name         = "cmp-bu%[1]d-%[2]s"
  trust_domain = "prod.lab"
  svid_regex   = "^spiffe://prod\\.lab/ns/cmp/sa/bu%[1]d%[2]s$"
}
`, i, suffix)
	}
	cfg := labProvider(t, "3m") + b.String()
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{
			{Config: cfg, Check: func(*terraformState) error {
				for i := range n {
					labAct(t, "count", "source", fmt.Sprintf("cmp-bu%d-%s", i, suffix), "1")
				}
				return nil
			}},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// TestLabCampaignAgentRestart runs as an operator: the SPIRE agent of the host
// restarts while the apply waits for a person; the provider's SVID source
// reconnects and the wait ends with the change applied, filed once (phase 3,
// lxd-spiffe's agents).
func TestLabCampaignAgentRestart(t *testing.T) {
	labCampaign(t)
	name := "cmp-ag-" + labEnv(t, "LIGO_E2E_SUFFIX")
	var settle func()
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6,
		Steps: []resource.TestStep{{
			PreConfig: func() {
				settle = labSettle(labActAsync(t, "restart-agent"), labActAsync(t, "approve-after-restart", "source", name))
			},
			Config: labProvider(t, "5m") + campaignSource(name, "v1"),
			Check: func(*terraformState) error {
				settle()
				labAct(t, "count", "source", name, "1")
				labAct(t, "drain")
				return nil
			},
		}},
	})
}
