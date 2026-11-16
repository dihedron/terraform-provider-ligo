# Holds

A binding removed in an emergency (containment by an owner of a side, or a break-glass delete) leaves a **hold**. Nobody creates that binding again, the owner pipeline included, until a person lifts the hold.

## What the apply says

```text
is held: a person removed it in an emergency and a person lifts the hold
(ligo hold list, ligo hold lift) after reviewing it. The provider never lifts a
hold and does not file again until then
```

The problem is `resource-held` (409). The provider does not retry and never works around it: re-filing under another name would defeat the review.

## Finding and lifting

```bash
ligo hold list
ligo hold lift <hold> --comment "reviewed, cause removed"
```

An approver of one of the binding's sides lifts it, and a comment is required. Neither a pipeline nor an operator who is not an approver can. The `ligo_holds` data source shows what is holding a pipeline back, read-only.

After the lift, run the apply again: the binding is created.
