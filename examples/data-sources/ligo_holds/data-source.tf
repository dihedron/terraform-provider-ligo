# The holds that keep a pipeline from creating a binding again.
data "ligo_holds" "open" {}

data "ligo_holds" "history" {
  all = true
}
