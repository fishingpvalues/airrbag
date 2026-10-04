# airrbag

[![Release](https://img.shields.io/github/v/release/fishingpvalues/airrbag?sort=semver)](https://github.com/fishingpvalues/airrbag/releases)
[![CI](https://github.com/fishingpvalues/airrbag/actions/workflows/ci.yml/badge.svg)](https://github.com/fishingpvalues/airrbag/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

Know where every file in [Sonarr](https://sonarr.tv), [Radarr](https://radarr.video),
[Lidarr](https://lidarr.audio), Readarr and Whisparr came from, and stop
deletes that would break a private-tracker seed.

airrbag is a reverse proxy in front of the *Arr web UI and API. In the UI it
adds a badge to every file: Usenet, public torrent, or private torrent. On a
delete it checks what the delete would actually do to the seed behind the file,
and refuses the ones that would end a private seed that is still owed.

Deleting a library file is one of three things, and the *Arr cannot tell them
apart:

| The library file is | Deleting it |
|---------------------|-------------|
| a separate copy, or from Usenet | frees the space; nothing seeds from it |
| a hardlink of a file a torrent seeds | is safe for the seed, but frees no space until the torrent goes too |
| the file a private torrent seeds from | ends the seed early: a hit-and-run |

History says where a file came from. Only the filesystem says which of the
three it is, because two names for the same bytes share an inode. airrbag
compares inodes.

## Screenshots

Screenshots of the badges, the delete dialog and the library lists page are
added with the first tagged release.

## Install

```bash
docker run -d \
  -p 127.0.0.1:17878:17878 \
  -e RADARR_API_KEY \
  -e QBITTORRENT_PASSWORD \
  -v ./airrbag.yml:/config/airrbag.yml:ro \
  -v /srv/media:/media:ro \
  ghcr.io/fishingpvalues/airrbag:latest
```

Then open `http://localhost:17878` instead of Radarr's own port. Radarr works
exactly as before; the badges and the delete check sit on top.

The image is a single static binary on distroless, running as a non-root user
with a read-only root filesystem. Mount the media read-only at paths you then
map in the config. See [`examples/docker-compose.yml`](examples/docker-compose.yml)
and [`examples/airrbag.yml`](examples/airrbag.yml).

Binaries for Linux, macOS, FreeBSD and Windows on amd64 and arm64 are attached
to each [release](https://github.com/fishingpvalues/airrbag/releases) with
`checksums.txt`. Container images are signed with cosign (keyless) and carry
an SBOM and build provenance.

## Setup

A minimal `airrbag.yml`:

```yaml
instances:
  - name: radarr
    listen: ":17878"
    upstream: http://radarr:7878
    api_key: ${RADARR_API_KEY}
clients:
  - name: qBittorrent              # as named in Radarr > Settings > Download Clients
    type: qbittorrent
    username: admin
    password: ${QBITTORRENT_PASSWORD}
path_mappings:
  - { from: /movies, to: /media/movies, source: arr }
  - { from: /downloads, to: /media/downloads, source: client }
```

Add one entry under `instances` per *Arr; each gets its own listener.
`airrbag check-config -config airrbag.yml` validates a file without starting.

The download client's address is read from the *Arr's own settings. Its
password is not, because the *Arr API masks it, so it goes in `clients`.

`path_mappings` matter. Radarr may see a file as `/movies/X.mkv`, qBittorrent
the same bytes as `/downloads/X.mkv`, and airrbag as `/media/...`. Without the
mappings the inode comparison cannot find the files and verdicts fall back to
history alone.

Point anything that deletes through the *Arr API (Maintainerr, scripts) at the
airrbag listener too; the server-side check then covers it.

## How it works

For each file:

1. The *Arr history links the file to its download. The import event carries
   the file id; the grab event carries the protocol, indexer, client and
   download id, which for a torrent is its info hash.
2. qBittorrent is asked for that torrent: private flag, trackers, ratio,
   seeding time, files. A torrent seeding straight from the library is also
   found by path when the history no longer mentions it.
3. The library file and the torrent's files are stat'ed. Same device and inode
   means hardlink; the library path inside the torrent's content path means
   the same file.
4. The tracker's rule decides whether the seed is still owed.

| Verdict | Meaning | Delete |
|---------|---------|--------|
| `keep` | private seed still owed, and the delete removes its data | refused, unless confirmed |
| `frees-nothing` | hardlink of a running seed | allowed, the UI says no space is freed |
| `safe` | Usenet, separate copy, finished or removed torrent | allowed |
| `unknown` | no history, or a client could not be asked | allowed |

A private torrent from a private indexer whose client cannot be reached counts
as `keep` while `guard.fail_closed` is on (the default). A private torrent
that no rule covers and that has no `trackers.default` is never considered
done.

### In the UI

The browser script draws a small panel on every movie, series, artist and
author page, and a source badge next to file names in tables. Hovering shows
the indexer, client, tracker, ratio, seeding time and the reasoning.

Before the UI sends a delete that removes `keep` files, a dialog names the
files, the tracker and the seeding time, and asks for an explicit confirmation.
Confirming registers a single-use grant for exactly that request. A
`frees-nothing` delete gets a short note instead.

The script listens for the DELETE request itself rather than for particular
buttons, so the check does not depend on how each app draws its dialogs. The
panel falls back to a floating corner badge when a UI update moves the page
header.

### On the server

The same check runs on every DELETE that passes through the proxy, whatever
sent it. A refused request gets `409 Conflict` with the files and the reasons.
If the check itself cannot run (the *Arr or a client does not answer) and
`guard.fail_closed` is on, the answer is `503`. Nothing else is ever blocked.

API clients can override deliberately with `X-Airrbag-Override: <reason>`.
The reason is logged.

`/__airrbag/` lists the whole library as Safe to delete, Frees nothing, Keep
and Unknown, with sizes.

## Configuration

Commented example: [`examples/airrbag.yml`](examples/airrbag.yml). `${NAME}`
anywhere in the file is replaced with the environment variable `NAME`.

| Key | Default | Meaning |
|-----|---------|---------|
| `instances[].name` | required | Label in logs and metrics |
| `instances[].listen` | required | Address of this instance's listener |
| `instances[].upstream` | required | The *Arr's URL |
| `instances[].api_key` | required | The *Arr's API key |
| `instances[].app` | `auto` | `sonarr`, `radarr`, `lidarr`, `readarr`, `whisparr` |
| `clients[].name` | | The client's name in the *Arr |
| `clients[].type` | required | `qbittorrent` or `sabnzbd` |
| `clients[].url` | discovered | Overrides the address read from the *Arr |
| `clients[].username`, `.password` | | qBittorrent WebUI login |
| `clients[].api_key` | | SABnzbd API key |
| `path_mappings[]` | | `from`, `to`, `source`: `arr`, `client` or a client name |
| `trackers.file` | | Roster JSON: `trackers[].domains`, `announce_domains`, `fragments` |
| `trackers.private[]` | | Extra private domains or indexer-name fragments |
| `trackers.rules[]` | | `domains`, `min_seed_time` (`72h`, `14d`), `min_ratio`, `require_both` |
| `trackers.default` | none | Rule for private torrents no rule matches |
| `guard.enabled` | `true` | Refuse deletes of `keep` files |
| `guard.dry_run` | `false` | Log instead of refusing |
| `guard.fail_closed` | `true` | Private indexer with an unreachable client counts as `keep` |
| `guard.grant_ttl` | `2m` | Lifetime of a confirmation given in the dialog |
| `cache_ttl` | `5m` | History index and verdict cache lifetime |
| `history_limit` | `100000` | History records indexed per instance |
| `log_level` | `info` | `debug`, `info`, `warn`, `error` |

### Exposure

airrbag has no login of its own. For its API it replays the caller's *Arr
credentials (session cookie, `X-Api-Key`, basic auth) against the *Arr and
answers only if the *Arr accepts them. Verdicts are never shown to a caller
the *Arr rejects, including in a refused delete.

Open without credentials, and only these:

| Open | Why |
|------|-----|
| `/__airrbag/health` | container healthcheck |
| `/__airrbag/metrics` | Prometheus; counts only |
| `/__airrbag/airrbag.js`, `/__airrbag/` | static script and page; their data calls need *Arr auth |

airrbag holds *Arr API keys and download-client passwords. Mount the config
read-only, pass secrets through the environment, and bind the listeners to
localhost, a LAN you trust, or a VPN or tailnet address. It is not meant to
face the internet; neither is the *Arr behind it.

If the *Arr uses "authentication disabled for local addresses", it sees
airrbag's address. airrbag forwards `X-Forwarded-For`, but real authentication
on the *Arr is the better choice.

### Network

airrbag contacts the *Arr instances in its config and the download clients
those instances list or the config names. Nothing else: no telemetry, no
update checks, no third-party hosts. This is enforced, not promised: every
outbound HTTP client goes through a host allowlist built from exactly those
addresses (`internal/egress`), redirects included, and a test fails the build
if code bypasses it.

## Supported versions

Tested in CI against pinned images of Radarr 6, Sonarr 4 and Lidarr 3, one job
each. Readarr and Whisparr use the same code paths (Whisparr 2 as Sonarr,
Whisparr 3 as Radarr) without an integration job yet. qBittorrent 4.6 and 5.x;
SABnzbd 4.

## Versioning

Semantic versioning, pre-1.0. At `0.x` a breaking change bumps the minor,
everything else the patch.

Commits follow [Conventional Commits](https://www.conventionalcommits.org/).
[release-please](https://github.com/googleapis/release-please) collects them
into a release PR, writes [`CHANGELOG.md`](CHANGELOG.md) and bumps
`version.txt`. Merging tags `vX.Y.Z` and builds the images and binaries.

Pin a version in production; `latest` moves.

## Troubleshooting

**No badges.** Open the browser console and look for `airrbag:` messages.
`curl http://<listener>/__airrbag/health` shows the detected app and whether
each download client answers.

**Everything is `unknown`.** The files were imported before the history the
*Arr kept, or by hand. A torrent seeding from the library path is still found
by path.

**`frees-nothing` and `safe` look wrong.** The paths are not mapped. Compare
`path` in `/__airrbag/api/files?parentId=<id>` with where the file is inside
the airrbag container.

**A delete is refused with 503.** airrbag could not reach the *Arr or a
download client and `guard.fail_closed` is on. Fix the client, or confirm in
the dialog.

## API

Under each listener, next to the proxied *Arr. Requires *Arr credentials.

| Route | Purpose |
|-------|---------|
| `GET /__airrbag/api/files?parentId=` | Verdicts for one movie, series, artist or author |
| `GET /__airrbag/api/resolve?path=` | UI route to parent id |
| `POST /__airrbag/api/check` | What a given DELETE would remove |
| `POST /__airrbag/api/grant` | Single-use confirmation for one DELETE |
| `GET /__airrbag/api/lists` | Whole-library grouping |
| `GET /__airrbag/api/info` | App, guard state, version |

## Development

```bash
make web     # bundle the browser script (Node 24)
make build   # static binary
make test    # go test ./...
make lint    # golangci-lint and TypeScript typecheck
scripts/integration.sh radarr lscr.io/linuxserver/radarr:<tag> 7878
```

Design notes are in [`docs/DESIGN.md`](docs/DESIGN.md), contributor notes in
[`AGENTS.md`](AGENTS.md).

## Contributing

Small fixes: open a PR. Larger changes: open an issue first. See
[`CONTRIBUTING.md`](CONTRIBUTING.md).

Report security issues through
[private advisories](https://github.com/fishingpvalues/airrbag/security/advisories/new).
[`SECURITY.md`](SECURITY.md) describes the scope.

## Compared with

| Tool | Does | Relation |
|------|------|----------|
| qbitrr | Manages qBittorrent from the *Arrs, including seeding rules | Never looks at library inodes |
| Decluttarr, Cleanuparr | Clean stuck or unwanted downloads out of queues and clients | Act on the client; airrbag guards the library |
| Maintainerr | Deletes watched media by rule | Complementary: point it at the airrbag listener |

## Roadmap

- Transmission, Deluge and NZBGet clients
- Integration jobs for Readarr and Whisparr
- Proposing upstream that the *Arrs store protocol and indexer on each file

## Provenance

Written with AI assistance (Claude Code), by one maintainer. The decision logic
is a pure function with table tests; behavior that depends on the *Arrs is
tested against real instances in CI; recorded fixtures come from a live stack
with secrets removed. Read `git log` before trusting the code.

## License

[Apache-2.0](LICENSE)
