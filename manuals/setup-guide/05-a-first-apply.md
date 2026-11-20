# A first apply

A source, a target and a binding, owned by a pipeline.

```terraform
resource "ligo_source" "ledger_ci" {
  name         = "ledger-ci"
  display_name = "Ledger CI"
  trust_domain = "prod.lab"
  svid_regex   = "^spiffe://prod\\.lab/ns/fin/sa/ledger-ci$"
}

resource "ligo_target" "ledger" {
  name         = "ledger-db"
  display_name = "Ledger database"
  platform     = "pg-ledger"
  jurisdiction = "dc/corp-prod/ledger"
  definition   = jsonencode({ database = "ledger", schema = "ledger" })
  access_levels = {
    read = { grants = jsonencode({ roles = ["LEDGER_RO_ROLE"] }) }
  }
}

resource "ligo_binding" "ledger_ci_reads" {
  name         = "ledger-ci-reads-ledger-db"
  source       = ligo_source.ledger_ci.name
  target       = ligo_target.ledger.name
  access_level = "read"
  description  = "Month-end reports"
}
```

Run `terraform plan`, then `terraform apply` (or `tofu`). With a trusted pipeline, each resource is applied at once inside its jurisdictions, and the binding apply continues until the binding is `ACTIVE`. With a person, each write waits for the approvers of its sides: see the *Operator's Guide*.

After the apply, `managed_by` of each resource holds the SPIFFE ID of the pipeline: only it changes them now.

## Checking the result

`terraform state show ligo_binding.ledger_ci_reads` shows `phase = "ACTIVE"` and `last_change_request`. `ligo cr show <id>` on the `ligo` side shows who approved and on what basis.

## Importing what exists

`terraform import ligo_source.ledger_ci ledger-ci` reads the resource and its revision into the state; the first plan after shows what the configuration would change. Adding `managed = true` to an imported resource needs the consent of the people of its sides: the apply waits.

## Upgrades

Upgrade by changing `version` and running `init -upgrade`. Before the first major release the schema may change; the release notes name every change.
