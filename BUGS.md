# BUGS

| # | bug | status |
|---|-----|--------|
| 1 | `basket add` silently no-ops on catchweight products | fixed |
| 2 | a fractional `--qty` writes a line that breaks tesco.com | fixed |
| 3 | `grosh slots` fails to decode `delivery.group` | fixed |

These are kept as the record of what went wrong. 1 and 2 are silent failures
that would be easy to reintroduce, because Tesco reports each of them as a
success; 3 is a drift the live suite is structurally unable to catch.

Fixed in `internal/tesco` (`SetQuantity`, `SetWeight`, `write`, `Catchweight`,
`DeliverySlot.Group`) and `internal/cmd/shop.go` (`--weight`), covered by
`internal/tesco/basket_test.go` and `internal/tesco/slots_test.go`.

## 1. `basket add` can silently no-op while reporting success

**Severity:** high — the write is lost, exit code is 0, and nothing is printed
to say so.

`grosh basket add <tpnc>` reports success whenever the `UpdateBasket` mutation
returns without a GraphQL error. It never checks that the requested line is
actually present in the basket that comes back. For at least one product the
gateway accepts the mutation, returns no error, and simply does not add the
item — so `grosh` prints the (unchanged) basket and exits 0.

### Reproduction

`259541778` (Tesco Large Pork Shoulder Joint) reproduces it every time:

```console
$ ./grosh basket --json | jq '.items|length'
32
$ ./grosh basket add 259541778 --qty 1     # also tried --qty 2, --qty 1.5
┌───────────────────────────────────────────────────────────────┐
│ ... basket printed, no mention of 259541778, no error ...      │
└───────────────────────────────────────────────────────────────┘
$ echo $?
0
$ ./grosh basket --json | jq '.items|length'
32                                          # nothing was added
```

### Root cause: catchweight items need a weight, not a piece count

This is not one broken product. It is a class — **catchweight goods**, which
Tesco sells in a set of discrete weights rather than by the piece. They are
identifiable by `price == unitPrice` with `unitOfMeasure: "kg"`:

| tpnc | product | price | unitPrice | adds? |
|------|---------|-------|-----------|-------|
| `259541778` | Tesco Large Pork Shoulder Joint | £5.00 | £5.00/kg | no |
| `251884298` | Tesco Large Pork Leg Joint | £7.00 | £7.00/kg | no |
| `281085590` | Tesco Pork Shoulder Steaks 700G | £6.15 | £8.79/kg | yes |

The first two silently no-op. The third — a fixed-weight 700g pack, where
`price != unitPrice` — adds normally.

The gateway exposes the selectable weights, on a field `grosh` does not
currently request. `ProductInterface` has **`catchWeightList: [CatchWeightInterface]`**,
whose members are `{ price, weight, default }`:

```console
$ # productQuery extended with: catchWeightList { price weight default }
$ grosh product 259541778   # raw response
"catchWeightList": [
  { "price": 9,     "weight": 1.8,  "default": true  },
  { "price": 9.75,  "weight": 1.95, "default": false },
  { "price": 10.5,  "weight": 2.1,  "default": false }
]
```

So the joint is not bought "by the kilo" continuously — tesco.com offers exactly
1.8kg, 1.95kg or 2.1kg, and the basket line must carry one of those weights.
`SetQuantity` sends `newUnitChoice: "pcs"` with a piece count, which is not a
representable state for such a product, so the gateway drops the write.

Also discovered while probing: `ProductInterface` exposes `bulkBuyLimit`
(25 for this product) and `restrictions: [ProductRestrictionType]`. Neither is
requested today; both are relevant to validating a basket write before sending it.

### How to add a catchweight item correctly

Send the **weight** as `newValue` with `newUnitChoice: "kg"`, and the weight
**must be one of the values in `catchWeightList`**. Passing an arbitrary weight
is what causes bug 2 — `1.5` is not an option for this product, and writing it
corrupted the basket.

Suggested shape:

- Request `catchWeightList { price weight default }` in `productQuery`.
- Expose it in `grosh product` output, so a user can see the choices.
- On `basket add`, look up the product; if `catchWeightList` is non-empty,
  require the requested value to match one of the listed weights (defaulting to
  the `default: true` entry when `--qty` is not given) and send it with
  `newUnitChoice: "kg"`. Otherwise send an integer piece count with `"pcs"`.
- Reject anything else with a message naming the valid weights, rather than
  writing it.

### `available` does not mean what the field name suggests

`internal/view/from.go:16` maps `Available` straight from Tesco's `isForSale`,
which is a catalogue-level flag, not store or slot stock. `259541778` reports
`available: true` while being unaddable, and this actively misled the
diagnosis. Either rename the field to `isForSale` or source real availability.

### Fix — done

Two parts.

1. **Stop losing the write silently.** `SetQuantity` already receives the
   updated basket in the mutation response and should assert against it: if the
   requested tpnc is not present at the requested quantity, return an error
   instead of reporting success. This is worth doing on its own, independent of
   any root cause.
2. **Support catchweight properly**, per "How to add a catchweight item
   correctly" above: request `catchWeightList`, pick or validate a weight from
   it, and send that weight with `newUnitChoice: "kg"`.

`internal/tesco/api_auth.go:48` (`SetQuantity`) and the `basket add` command
path in `internal/cmd/shop.go`.

---

## 2. A fractional `--qty` writes a basket line that breaks tesco.com

**Severity:** high — corrupts the whole basket, not just the offending line,
and the damage is invisible from `grosh` itself.

`basket add --qty` is a `float`. Its value goes straight into the mutation as
`newValue`, alongside the hardcoded `newUnitChoice: "pcs"` at
`internal/tesco/api_auth.go:73`. Nothing validates the pair.

Tesco accepts the write. The GraphQL basket read returns the line happily:

```console
$ ./grosh basket --json | jq '.items[]|select(.tpnc=="259541778")'
{
  "tpnc": "259541778",
  "title": "Tesco Large Pork Shoulder Joint",
  "quantity": 1.5,
  "unit": "pcs",          <-- one and a half *pieces* of a single joint
  "cost": 5.62
}
```

But **tesco.com's basket page cannot render it and shows "Sorry, an error has
occurred."** The entire basket is unusable in the browser — every other line is
fine, but the page never loads, so the order cannot be reviewed or checked out.

### Why this is worse than it looks

- `grosh` gives no hint anything is wrong: `basket` prints the line, the guide
  total is correct, exit code is 0.
- The website gives no hint either — no indication of *which* line is at fault.
- The user cannot fix it in the browser, because the page that would let them
  remove the line is the page that will not load.
- The only route back is `grosh basket rm <tpnc>`, which requires already
  knowing which tpnc is to blame.

Verified: the error appeared after that line was written and cleared the moment
it was removed, with no other change.

### Fix — done

Reject a non-integer `--qty` before sending the mutation, in the `basket add`
command path (`internal/cmd/shop.go`) or in `SetQuantity` itself.

Note that fractional quantities are legitimate in Tesco's own model for genuinely
weighed goods — this account's order history contains `0.1x Tesco Root Ginger
Loose` — so do not treat "integer" as a universal invariant of the API. The
invariant that actually holds is narrower: **a line whose unit is `pcs` must
have an integer quantity.** Since `SetQuantity` currently hardcodes `pcs` for
every write, and nothing in the CLI can select another unit, the practical guard
today is to reject fractional `--qty` outright. If per-unit writes are added
later, gate the check on the unit being sent rather than removing it.

A defensive follow-up: have `grosh basket` warn when it *reads* a line with a
fractional quantity on a `pcs` unit, naming the tpnc. That turns an
unexplained website outage into a one-line diagnosis, and would have caught this
immediately.

### Do not "fix" this by changing `newUnitChoice`

Forcing `newUnitChoice: "kg"` instead of `"pcs"` **does** make `259541778`
appear in the basket, so it looks like the fix for bug 1. It is what produced
the corrupt line above.

`newUnitChoice: "kg"` is in fact correct for catchweight products — but only
when `newValue` is one of the weights in `catchWeightList`. Forcing `"kg"` with
an arbitrary quantity such as `1.5` is precisely what produced the corrupt line
above. The unit and the value have to be decided together, from the product.

Nor can `unitOfMeasure` alone be used to detect catchweight: it is only the
basis for the *unit price* and is present on essentially everything — Nutella
350g reports `price=2.99, unitPrice=8.54, uom=kg`, tortilla chips and cheese
likewise. The signal is a non-empty `catchWeightList`.

---

## 3. `grosh slots` is broken by schema drift on `delivery.group`

**Severity:** medium — the command is unusable, but nothing is corrupted and
the fix is one line.

Every invocation fails during decoding:

```console
$ grosh slots
error: decoding tesco DeliverySlots response: json: cannot unmarshal number into
Go struct field .delivery.0.group of type string
$ echo $?
1
```

`Slot.Group` is declared `string` at `internal/tesco/types.go:203`, and the
gateway now returns a number. Confirmed by retyping the field: `int` decodes the
live response cleanly and every slot comes back.

```console
$ # with Group retyped to int
$ grosh slots --json | jq '.[0]'
{
  "id": "REVMSVZFUllfVkFOIzI5OTQ...",
  "start": "2026-09-03T12:00:00Z",
  "end": "2026-09-03T13:00:00Z",
  "charge": 9,
  "status": "Available"
}
```

### Fix — done

`DeliverySlot.Group` is now `int`, and `TestSlotsDecodeNumericGroup` in
`internal/tesco/slots_test.go` decodes a real-shaped response to hold it there.

Two things worth noting beyond the type:

- **`Group` is never used.** It is not in the view model, the table, or the JSON
  output — nothing reads it. An unused field took out the whole command. It is
  typed correctly now rather than dropped, so the value stays available, but
  dropping it from the query and the struct would remove the failure mode
  outright and is still the safer option if nothing ever needs it.
- **The live suite cannot catch this class.** It validates that a field *exists*
  on a type, which `group` always did — the drift was in the Go type, not the
  query, and only decoding a real response reveals it. The new test decodes
  rather than validates, which is the shape this kind of guard needs.

This is the failure mode the README warns about ("If Tesco renames a field, a
command stops working"), with the twist that the field was neither renamed nor
needed.

---

## Notes from fixing these

Two things only real data showed, worth keeping:

- **A basket read cannot distinguish a good catchweight line from a corrupt one.**
  Tesco returns the 1.8kg joint as `quantity: 1.8, unit: "pcs"` — the exact shape
  of the line that broke the basket page. The read-side warning in `emitBasket`
  therefore false-positived on a perfectly good line and told the user to delete
  it. It now confirms a fractional line against the product's `catchWeightList`
  before calling it broken, which costs a lookup only for lines that are already
  anomalous.
- **`unit` in the basket JSON is not the unit the line was written in.** The
  1.8kg line reads `"pcs"`. Nothing should be inferred from it.
