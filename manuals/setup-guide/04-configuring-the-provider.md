# Configuring the provider

The provider has two ways to authenticate. Both verify the server: there is no option to skip it.

## A pipeline: the SPIFFE Workload API

```terraform
provider "ligo" {
  server_url         = "https://ligo.example.org:9443"
  spiffe_socket      = "unix:///run/spire/agent.sock"
  server_id          = "spiffe://prod.example.org/ns/sys/sa/ligo"
  approval_reference = "CHG-0001"
  approval_timeout   = "10m"
}
```

The provider presents the caller's X.509-SVID over mutual TLS and verifies `ligo` by its SPIFFE ID against the bundle of the Workload API. `server_id` defaults to `spiffe://<the caller's trust domain>/ns/sys/sa/ligo`.

`approval_reference` is the upstream record (a ticket, a pipeline run): `ligo` requires it for a delegated approval, and records it in the audit.

## A person: LDAP

```terraform
provider "ligo" {
  alias         = "person"
  server_url    = "https://ligo.example.org:9443"
  ca_cert       = "/etc/ligo/ca.pem"
  username      = "olivia"
  password_file = "/home/olivia/.ligo/password"
  managed       = false
}
```

The password is read from a private file, never from the configuration. A person cannot own a resource, so `managed = false` is needed (the plan fails with that advice otherwise), and every write waits for an approver.

## Reference

| Attribute | Meaning |
| :------------------- | :------------------------------------------------------------- |
| `server_url` | The admin API, an `https://` URL. Environment: `LIGO_SERVER_URL`. |
| `spiffe_socket` | The Workload API socket. Environment: `SPIFFE_ENDPOINT_SOCKET`. |
| `server_id` | The SPIFFE ID `ligo` must present. |
| `ca_cert` | A PEM bundle that verifies the server, for callers with no Workload API. |
| `username`, `password_file` | LDAP credentials; the password is in a private file. |
| `approval_timeout` | How long a write waits for its change request: a duration, default `10m`, at most `24h`. |
| `approval_reference` | The upstream change record, sent with every filing. |
| `automation_context` | A map (job, workspace, run URL) recorded by `ligo`; never used for authorization. |
| `managed` | The default of `managed` on sources, targets and bindings; `true` unless set. |

## Timeouts

A write waits at most `approval_timeout`. When it ends with the request still open, the apply fails and the request stays in `ligo`; it may still be approved, and the next apply waits for the same request instead of filing another one. Set the time-out of the pipeline job longer than `approval_timeout`.
