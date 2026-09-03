# tescoctl

A Go CLI for Tesco groceries: search the catalogue, inspect products, manage
your trolley, and read your orders — from the terminal, with `--json` on every
command.

```
tescoctl search "oat milk" --limit 4
```

```
┌──────────────────────────────────────────────────────────────────────────────────────┐
│ TPNC       PRODUCT                                  PRICE  UNIT         NOTES        │
├──────────────────────────────────────────────────────────────────────────────────────┤
│ 323850450  Oatly Barista edition Coconut Oat 1L     £2.30  £2.30/litre  £1.70 Save…  │
│ 280832278  Alpro Oat Original Long Life 1L          £2.45  £2.45/litre  £1.50 Club…  │
│ 307745622  Tesco Long Life Oat Original 1L          £1.50  £1.50/litre               │
│ 314315257  Oatly The Original Whole Oat Drink 1L    £2.00  £2.00/litre  £1.50 Save…  │
└──────────────────────────────────────────────────────────────────────────────────────┘
```

## This is not an official API

Tesco has no public API. `tescoctl` talks to `xapi.tesco.com`, the internal GraphQL
gateway behind tesco.com and the Tesco app. That means:

- **It can break without warning.** Operations are reverse-engineered. If Tesco
  renames a field, a command stops working until the query is updated.
- **The API key rotates**, roughly monthly. It is public — baked into Tesco's own
  web bundles — but not stable. When you see `403 invalid client`, lift a fresh
  one and pass `--api-key` or set `TESCO_API_KEY`.
- **Automated access is against Tesco's terms of service.** This is built for
  personal use against your own account. It throttles to one request per second
  by default and never retries a 429 or 403.

Checkout is deliberately not implemented. Payment is a separate CSRF-protected
flow with 3-D Secure, browser-bound by design. Fill the basket here, then pay in
a browser — `tescoctl basket` prints the link.

## Install

```bash
go install github.com/endigma/tescoctl@latest
```

## Usage

Search, product lookup, and category browsing need no account:

```bash
tescoctl search "sourdough"              # search the catalogue
tescoctl product 254656543               # price, pack size, nutrition
tescoctl categories                      # the category tree and its facets
tescoctl browse "Fresh Food" --limit 20  # by name or facet id
```

Everything else needs a session:

```bash
tescoctl auth login                      # opens Chrome; you sign in by hand
tescoctl auth status                     # who you are, when it expires
tescoctl basket
tescoctl basket check                        # verify every line against the catalogue
tescoctl basket add 254656543 --qty 2
tescoctl basket add 259541778 --weight 1.8   # products sold by weight
tescoctl basket rm 254656543
tescoctl favourites
tescoctl orders                          # add --pending for upcoming ones
tescoctl order <id>
tescoctl slots --from 2026-09-10
tescoctl auth logout
```

`tescoctl basket` warns about a line tesco.com cannot render. `tescoctl basket check`
goes further and looks each line up, reporting items Tesco no longer sells — it
costs a request per line, which is why it is a command rather than something
every basket listing pays for. It exits non-zero when it finds something, so it
works as a pre-checkout gate:

```bash
tescoctl basket check && open "$(tescoctl basket --json | jq -r .checkoutUrl)"
```

### JSON

Every command takes `--json`. JSON goes to stdout and nothing else does, so it
pipes cleanly; diagnostics go to stderr, and errors are JSON too under `--json`.

```bash
tescoctl search "oat milk" --json | jq -r '.[] | "\(.price)\t\(.title)"'
tescoctl orders --json | jq '.[0].orderNo'
```

Output models are deliberately decoupled from Tesco's response shapes, so the
JSON contract does not shift underneath you every time an internal field moves.

Listings print how much of a result set is on screen — `showing 30 of 553` —
on stderr, so `--json` still emits a bare array and nothing else.

### Signing in

`tescoctl auth login` opens a real browser window at the Tesco sign-in page and
waits for you to log in. It never types your credentials — Tesco uses email OTP
and sits behind Akamai, and automating the form is both fragile and a good way to
get blocked. The browser profile persists under your config directory, so
subsequent logins usually skip the OTP.

Chrome, Chromium, Edge, Brave and Vivaldi are detected automatically in their
usual locations and on `$PATH`; override with `--chrome /path/to/binary`. Arc is
Chromium-based but refuses remote control, so it cannot be used here — install
one of the above, or use the cookie import below.

If that path is blocked, or you would rather not have tescoctl drive a browser,
export your tesco.com cookies and import them:

```bash
tescoctl auth import --file cookies.json
```

It accepts several formats: a JSON array of `{"name", "value"}` objects (what
cookie-export extensions emit), an object mapping names to values, or a raw
`name=value; …` Cookie header. The header form is usually easiest — see below.
Only the cookies that matter are kept (`OAuth.AccessToken`, `UUID`, and the
Akamai set).

The auth cookies are `HttpOnly`, so `document.cookie` in the console will not
see them. Two routes that do work, with Tesco open and signed in:

**DevTools network panel** (no extension needed):

1. F12 → Network, then click around tesco.com so a request fires.
2. Select any request to `xapi.tesco.com`.
3. Under Request Headers, right-click the `Cookie` header → Copy value.
4. `pbpaste | tescoctl auth import` (macOS), or `tescoctl auth import --file cookies.txt`.

**Cookie-export extension** (Cookie-Editor and similar): open it on tesco.com,
Export → JSON, then `tescoctl auth import --file cookies.json`. Extensions can read
every cookie on the page, so prefer one you already trust.

Either way, check it worked with `tescoctl auth status`.

The session lands in `session.json` under your config directory, written `0600`
inside a `0700` directory. It holds a bearer token — treat it like a password.

### Staying signed in

The access token lasts an hour, but the same jar carries a refresh token that
Tesco issues for thirty days. `tescoctl` redeems it over plain HTTP, with no
browser: expired sessions are renewed in place, both before a command that needs
auth and after a 401 mid-command, so you sign in about once a month rather than
once an hour.

```bash
tescoctl auth status     # includes "Renews to", the refresh-token window
tescoctl auth refresh    # force a renewal early, or check renewal still works
```

Two things worth knowing:

- **Renewal rotates the refresh token**, so every renewal is written back to
  `session.json` immediately. A renewal that is not persisted burns the
  credential the next one needs.
- **A spent refresh token is not announced.** Tesco answers `200` on the
  groceries landing page exactly as it does on success, and signals the failure
  by clearing the OAuth cookies rather than rotating them. `tescoctl` detects
  that and tells you to sign in; it will not save the resulting dead session.

Renewal replays the `_abck` and `bm_*` cookies the original sign-in earned,
which is why Akamai accepts it without a browser. Sign-in itself still needs
one, because a cookie-less client gets challenged.

## Development

```bash
go test ./...              # unit tests, no network
go test -tags live ./...   # hits the real gateway
```

The live tests are the substitute for a schema. Tesco disables GraphQL
introspection, so there is nothing to generate from and nothing to validate
against — but the gateway runs validation *before* authentication, which means a
stale field answers `Cannot query field X on type Y` even with no session. The
live suite exploits that to check every operation, including the authenticated
ones, without needing an account. Run it when something breaks; it will tell you
whether the cause is drift.

### Notes on the reference implementations

Two JS projects map the same gateway and were useful references, with caveats
worth recording:

- `open-supermarkets` fetches search results in two steps via
  `search.api.tesco.com`, on the grounds that GraphQL search does not work. It
  does — verified. The single `search` operation is enough.
- `basketeer`'s `categoryFacet()` base64-encodes the raw department name. Real
  facet ids base64-encode the *URL-encoded* name (`b;` + `base64("Fresh%20Food")`).
  `tescoctl` takes facet ids from the taxonomy, which is authoritative either way.
- `basketeer` reopens a headed Chrome for every hourly token refresh, on the
  grounds that Akamai rejects a pure-HTTP refresh. It does not: its HTTP attempt
  never replayed the `_abck`/`bm_*` cookies Akamai actually checks. Seeding
  those, the refresh endpoint rotates the token over plain `fetch` — verified,
  and the basis for `internal/auth/refresh.go`. See
  `internal/auth/refresh_live_test.go` to re-check it against the live site.

Three behaviours of the gateway that are easy to get wrong:

- **It returns more results than you ask for** — consistently about six more than
  `count`, at every page size tested. `tescoctl` trims to the requested limit.
- **Some products are sold by weight, not by the piece.** A roasting joint is
  offered in a few fixed weights, listed by the product query as
  `catchWeightList`. Writing such a line in pieces is dropped silently; writing
  a weight Tesco does not offer is *accepted* and produces a basket tesco.com
  cannot render at all. `tescoctl product` prints the weights on offer, and
  `basket add --weight` is the only way to add one:

  ```console
  $ tescoctl product 259541778
  Weights    1.8kg — £9.00 (default)
             1.95kg — £9.75
             2.1kg — £10.50
  ```

- **Auth failures are reported inconsistently.** Basket and favourites answer
  `Unauthorized` with `extensions.http.status: 401`; orders and slots answer
  `A token was expected, but not defined` with no status and a misleading
  `INTERNAL_SERVER_ERROR` code. Both mean "log in".
