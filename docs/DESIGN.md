# Design

## Goal

Answer one question per library file, everywhere a user can delete it: what
happens to the bytes a tracker is still counting on?

## Architecture

```mermaid
flowchart LR
  B[Browser] -->|UI + API| P[Airrbag proxy]
  T[Other tools] -->|API DELETE| P
  P -->|everything else, untouched| A[*Arr]
  P --> E[Engine]
  E -->|history, files, clients| A
  E -->|torrents, files, private flag| Q[qBittorrent]
  E -->|stat: dev, inode, nlink| F[(Media, read-only)]
  E --> R[Tracker rules]
```

One listener per *Arr instance. The proxy:

1. Adds one `<script>` tag to HTML responses (gzip is decoded; anything else
   passes untouched; API, static and websocket traffic is never modified).
2. Serves its own endpoints under `/__airrbag/`.
3. Evaluates DELETE requests that remove files and answers `409` for `keep`.

## Data flow for one file

```mermaid
sequenceDiagram
  participant E as Engine
  participant A as *Arr
  participant Q as Torrent client
  participant F as Filesystem
  E->>A: GET /history (paged, cached)
  Note over E: import event: fileId -> downloadId<br/>grab event: downloadId -> protocol, indexer, client, hash
  E->>A: GET /moviefile?movieId=
  E->>Q: GET torrents/info (one call, cached 60 s)
  E->>Q: torrents/files, properties, trackers (per hash, cached)
  E->>F: stat(library file), stat(torrent files)
  Note over E: verdict.Decide (pure function)
```

## Decisions

**Reverse proxy, not a browser extension.** It works on every device and
browser with nothing to install, and the same process can enforce the rule
server-side. An extension can only warn the browser it runs in.

**Provenance from the API, not from the UI.** The badges' DOM layer is the
only fragile part. Everything that decides a verdict uses documented REST
endpoints, which change far less often than the web UI.

**Inode comparison.** History says a file came from a torrent; only the
filesystem says whether deleting it removes the torrent's data. A hardlink
(same device and inode, different name) survives deletion of the library name.

**Delete interception below the UI.** The script patches `XMLHttpRequest` and
`fetch` for `DELETE /api/vN/...` instead of hooking buttons, so it is
independent of how each app draws its delete dialogs. The *Arr UI's own request
cannot carry an extra header, so a confirmed dialog registers a one-shot grant
keyed by method, path, query and body hash; the guard consumes it.

**Evidence chain when the history is silent.** Many libraries hold files the
*Arr history never recorded: imported by hand, by another tool, or before the
history kept them. Airrbag then gathers evidence in order of strength and
stops at the first hit:

1. Inode: the same bytes as a file of any torrent in any client (one walk of
   every torrent's content, cached 15 minutes), or of a file in a
   direct-download folder.
2. Import folder: the import event's source path lies inside a torrent's
   content or a Usenet client's finished folder.
3. Release name, normalized (case, separators, media extension): a finished
   SABnzbd job, an NZBHydra2 grab (NZB or torrent, with the indexer), a file
   in a direct-download folder, a torrent's name.

Names are weaker than inodes, so short or generic names (under 12
characters, one word) never match.

**Unknown is not safe, and torrent evidence is never unknown.** "We don't
know where it came from" used to mean "allow". For a torrent download whose
seed cannot be seen (client down, files unreadable) that is exactly the
hit-and-run airrbag exists to prevent, so any torrent evidence turns an
unknown verdict into `keep`. What remains unknown has no evidence at all, and
`guard.unknown` decides:

- `confirm` (default) asks once in the UI with a warning and lets an API
  caller without a grant through, logged at WARN and counted in
  `airrbag_unknown_deletes_total`. A *browser* delete without a grant is
  refused (409, `reason: "unknown"`): the UI always goes check, dialog,
  grant, so a browser request that arrives without a grant skipped the
  dialog, and the injected safety net turns the 409 into it. "Browser" means
  the request carries `Sec-Fetch-Mode`/`Sec-Fetch-Site` (set by every modern
  browser, not removable by page scripts) or a cookie; scripts and server-side
  tools send neither. Blocking these by default would interrupt
  every cleanup of an old library while protecting nothing the evidence chain
  could identify. A visible warning plus an audit trail is the balance.
- `block` refuses them everywhere without a grant, for maximum safety.
- `allow` keeps only the badge.

**Fail closed for private trackers.** If the torrent client cannot be asked and
the indexer is private, the verdict is `keep`. If several clients exist and one
is down, "not found" is never concluded from a partial view. More generally,
while any configured torrent client is unreachable, a file that no evidence
places in Usenet or a direct-download folder is `keep`, whatever
`guard.unknown` says: the client that cannot be asked might hold a torrent
seeding from exactly that file.

**No own authentication.** Airrbag replays the caller's *Arr credentials
against `/system/status`. Whoever may use the *Arr may use Airrbag; verdicts
are never revealed to anyone else, not even in a 409 body.

**No egress beyond the stack.** One allowlist, built from the configured *Arr
upstreams and the download clients they list, wraps every outbound transport,
redirects included. A source-scan test fails the build on code that would
bypass it (default client, package-level `http.Get`).

**Dashboard: Preact, bundled and embedded.** The dashboard needs sortable,
filterable, paged tables and a few stateful views; hand-written DOM code (what
the injected script uses, to stay tiny and dependency-free) would grow into an
ad-hoc framework, and server-rendered templates plus htmx would put view logic
in two languages. Preact gives components and hooks in about 4 KB, esbuild
bundles it with the TypeScript into one JS and one CSS file, and `go:embed`
ships them in the binary. No CDN, no runtime fetch of anything foreign; the
shell is served with a CSP of `default-src 'none'; script-src 'self';
style-src 'self'` plus what the app needs from its own origin. Filtering,
sorting and paging run server-side, so a 14,000-file Lidarr library costs one
small JSON page per view.

**One look for badges and dashboard.** The label styles live once in
`web/src/shared/labels.css`: the dashboard bundles it, the injected script
imports it as text into its shadow root. Colours follow the Servarr label
palette, so a badge in Radarr and a row on the dashboard read the same.

**Refusals the *Arr UI can show.** The 409 uses the Servarr error shape
(`message`, `description`), which every generic error renderer in the *Arr
frontends reads. Their delete handlers store a failed request and show
nothing, so the injected script also recognises its own refusal (the
`airrbag: true` marker) on `XMLHttpRequest` and `fetch` and opens the dialog,
with the override.

**Pure core.** `internal/verdict` takes plain values and returns a verdict
with reasons. It has no I/O and carries most of the test cases.

### Every verdict names its cause

One sentence used to describe every kept file, so a file kept only because
qBittorrent was down read as "the seeding data of a private torrent". The
decision now returns a `Cause` with the verdict (`internal/verdict`), and
every user-facing text (dialog reason, 409 message and description) is built
from it in one place (`engine.Summary`). A table test per cause pins the
sentence, and asserts that only the seed-in-place cause says "seeding data".

### Old qBittorrent releases: read the field where the release has it

qBittorrent moved fields between endpoints across 4.x and 5.x. The client
asks for the newest source and falls back per field rather than per version:
the private flag from `torrents/info` (5.0), `torrents/properties`
`is_private` (4.6), or the disabled DHT/PeX/LSD rows of `torrents/trackers`;
the seeding time from `torrents/info` (4.4) or, per torrent and only when a
verdict needs it, `torrents/properties`; `content_path` (4.3.2) or
`save_path` + name. Field presence decides, not the version number, so a
patched or forked build behaves. `app/webapiVersion` below 2.0 (qBittorrent
4.0) is refused outright: that is the legacy API. CI runs real 4.3, 4.6 and
5.x containers.

### Resume files as a second witness

A down WebUI used to leave airrbag with "client unreachable", which keeps the
file but cannot say why. `libtorrent-resume` reads the client's own resume
files from a read-only mount (qBittorrent `BT_backup`, Deluge `state`, or a
folder of `.torrent` files) and answers the same questions offline. It is
an ordinary torrent client to the engine, so it joins the info-hash and path
search like any other; when the live client is down and the resume files
still show a seed, the verdict is the precise "seeds from this file". The
bencode decoder bounds input size, depth and element count and is fuzzed,
and metainfo paths cannot leave the torrent root, because these files are
read without trusting whoever wrote them.

## Package map

| Package | Responsibility |
|---|---|
| `internal/config` | YAML, `${ENV}` expansion, validation, defaults |
| `internal/egress` | Host allowlist every outbound HTTP client goes through |
| `internal/arr` | Servarr client, per-app shapes, history index |
| `internal/clients` | Download-client interfaces; `qbittorrent`, `sabnzbd` |
| `internal/paths` | Path translation between container views |
| `internal/fsx` | Inode identity |
| `internal/trackers` | Private detection and seeding obligations |
| `internal/verdict` | The decision |
| `internal/engine` | Caches and orchestration per instance |
| `internal/proxy` | Reverse proxy, injection, guard, API, dashboard endpoints |
| `internal/hub` | What every instance of one process shares: instance list, guard log, redacted config |
| `internal/webassets` | Embedded bundles (`make web`) and icons (`make icons`) |
| `web/src` | Injected script (TypeScript, esbuild) |
| `web/src/dashboard` | Dashboard (Preact + TypeScript, esbuild) |
| `web/src/shared` | Labels, colours and formatting used by both |
| `assets` | Logo sources; `scripts/icons.sh` renders the PNG set |

## Security design

airrbag holds every key of the stack it fronts, so its rule is: no new way in,
and no way for a secret out. The details and the threat model are in
[SECURITY.md](../SECURITY.md); the design choices behind them:

- **No own login.** Authentication is delegated to the *Arr (API key in
  constant time, otherwise the caller's cookie/basic auth replayed against the
  *Arr's `/system/status` with the real client address). A second password
  store would be a second thing to get wrong and to keep in sync; delegating
  inherits the *Arr's own fixes. 2FA comes from an SSO proxy in front
  (`auth.forward_auth_header`, trusted proxies only), not from a homemade TOTP.
- **The client address is a security input.** "Authentication disabled for
  local addresses" makes it one, so forwarding headers are accepted only from
  `auth.trusted_proxies` and taken right-to-left until the first untrusted hop.
- **Grants instead of a bypass header.** The dialog registers a server-side,
  single-use grant keyed by HMAC(identity, method, URL, body hash) with a short
  TTL. Nothing secret travels to the browser, a grant cannot be replayed or
  moved to another URL or user, and the raw override header is opt-in.
- **Scrub at the edges.** Secrets are removed from every log record (a slog
  handler), every JSON body (`writeJSON`) and the metrics output, with tests
  that push the key through every endpoint.
- **Config hygiene at load time.** A group/world-readable config that holds
  an inline secret, and placeholder secrets, are refused before anything
  starts. A config made only of `${...}` references has nothing to hide.
