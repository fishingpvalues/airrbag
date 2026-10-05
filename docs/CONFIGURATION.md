# Configuration

Commented example: [`examples/airrbag.yml`](../examples/airrbag.yml). Secrets
stay out of the file:

- `${NAME}` is the environment variable `NAME`, or, when `NAME` is unset and
  `NAME_FILE` is set, the contents of that file (Docker/Kubernetes secrets).
- `${file:/run/secrets/x}` is the contents of a file.
- References are resolved per value after the file is parsed (in string
  values and durations), so a variable's content is never read as YAML.
- The whole file may be [age](https://age-encryption.org)-encrypted
  (`airrbag.yml.age`); set `AIRRBAG_AGE_IDENTITY_FILE` to the identity.

airrbag refuses to start when the config file holds a secret written inline
and is readable by group or others (`chmod 600` it, or keep every secret a
`${...}` reference, which can then live in git; `AIRRBAG_ALLOW_INSECURE_CONFIG=1`
downgrades this to a warning for filesystems without POSIX permissions), and
when a key or password is still a template value such as `changeme` or
`<your api key>`.

| Key | Default | Meaning |
|-----|---------|---------|
| `instances[].name` | required | Label in logs and metrics |
| `instances[].listen` | required | Address of this instance's listener |
| `instances[].upstream` | required | The *Arr's URL |
| `instances[].api_key` | required | The *Arr's API key |
| `instances[].app` | `auto` | `sonarr`, `radarr`, `lidarr`, `readarr`, `whisparr` |
| `clients[].name` | | The client's name in the *Arr |
| `clients[].type` | required | `qbittorrent`, `transmission`, `deluge`, `rtorrent`, `qbittorrent-resume`, `qbittorrent-sqlite`, `libtorrent-resume`, `sabnzbd`, `nzbhydra2`, `xunlei` |
| `clients[].url` | discovered | Overrides the address read from the *Arr. Required for `nzbhydra2` |
| `clients[].username`, `.password` | | qBittorrent, Transmission, rTorrent login; Deluge Web UI password; NZBHydra2 basic auth |
| `clients[].api_key` | | SABnzbd API key; NZBHydra2 main API key (optional) |
| `clients[].path` | | `xunlei`: its download folder; `libtorrent-resume`: the resume folder (e.g. qBittorrent's `BT_backup`), as airrbag sees it |
| `clients[].layout` | `auto` | `libtorrent-resume`: `qbittorrent`, `deluge` or `torrents` (a folder of `.torrent` files) |
| `clients[].save_path` | | `libtorrent-resume` with `layout: torrents`: where their data lies, client view |
| `clients[].path` (resume) | | `qbittorrent-resume`: qBittorrent's config folder (holding `qBittorrent.conf`, or its parent); `qbittorrent-sqlite`: the `torrents.db` file |
| `clients[].live` | the only qBittorrent | `qbittorrent-resume`: the live qBittorrent client asked for its storage type and torrent count |
| `clients[].stale_after` | `24h` | `qbittorrent-resume`: with no live client to compare with, a store unchanged this long counts as out of date |
| `clients[].tmp_dir` | system temp | `qbittorrent-resume`, `qbittorrent-sqlite`: where the database copy is made; mount a tmpfs there when the root filesystem is read-only |
| `path_mappings[]` | | `from`, `to`, `source`: `arr`, `client` or a client name |
| `trackers.file` | | Roster JSON: `trackers[].domains`, `announce_domains`, `fragments` |
| `trackers.private[]` | | Extra private domains or indexer-name fragments |
| `trackers.rules[]` | | `domains`, `min_seed_time` (`72h`, `14d`), `min_ratio`, `require_both` |
| `trackers.default` | none | Rule for private torrents no rule matches |
| `guard.enabled` | `true` | Refuse deletes of `keep` files |
| `guard.dry_run` | `false` | Log instead of refusing |
| `guard.fail_closed` | `true` | A check that cannot run (an *Arr or client error) answers 503 |
| `guard.unknown` | `confirm` | Delete of a file with no provenance evidence: `confirm` asks once in the UI (a browser delete without the dialog's grant is refused) and lets API callers through with a WARN and `airrbag_unknown_deletes_total`; `block` refuses (409) without a grant; `allow` only shows the badge |
| `guard.grant_ttl` | `60s` | Lifetime of a confirmation given in the dialog (max `10m`) |
| `guard.allow_override_header` | `false` | Honor `X-Airrbag-Override` / `?airrbagOverride=` from signed-in API callers |
| `auth.trusted_proxies[]` | none | CIDRs/IPs of reverse proxies in front; only from these is `X-Forwarded-For` believed |
| `auth.forward_auth_header` | none | SSO identity header (e.g. `Remote-User`) a trusted proxy sets after its own login |
| `auth.grant_secret` | random per start | HMAC key binding confirmations to a caller (32+ characters) |
| `dashboard.cross_instance` | `false` | Show every instance on any instance's dashboard |
| `metrics.public` | `false` | Serve `/__airrbag/metrics` without authentication |
| `cache_ttl` | `5m` | History index and verdict cache lifetime |
| `history_limit` | `100000` | History records indexed per instance |
| `log_level` | `info` | `debug`, `info`, `warn`, `error` |

## Reading resume files (`libtorrent-resume`)

A libtorrent-based client keeps one resume file per torrent next to its
`.torrent` files. airrbag can read those from a read-only mount, with no API
and no credentials:

```yaml
clients:
  - name: qBittorrent resume
    type: libtorrent-resume
    path: /resume/BT_backup          # qBittorrent's BT_backup, mounted :ro
path_mappings:
  - { from: /downloads, to: /media/downloads, source: qBittorrent resume }
```

It reports the private flag, save path, files, seeding time, ratio and
trackers, like a live client. Use it next to the live qBittorrent: when the
WebUI is down, the resume files still prove whether a library file is a
seed, so fewer files fall back to "client unreachable". It also covers
libtorrent clients without an API. Layouts: qBittorrent `BT_backup`
(`*.fastresume` and `*.torrent`), Deluge `state` (`torrents.fastresume`), or
any folder of `.torrent` files plus `save_path` (no seeding time there, so a
private obligation reads as not met). For qBittorrent, prefer
`qbittorrent-resume` below: it also reads the SQLite store.

Paths in resume files are the client's view, so map them with a
`path_mappings` entry whose `source` is this client's name (or `client`).

## qBittorrent's resume store, detected (`qbittorrent-resume`)

qBittorrent keeps resume data either in `BT_backup` (Legacy, the default) or
in one SQLite file, `torrents.db` (opt-in since 4.4.0). Switching leaves the
old store behind, so a config folder often holds both and one of them is
stale. `qbittorrent-resume` finds out which one is in use and reads that one:

```yaml
clients:
  - name: qBittorrent resume
    type: qbittorrent-resume
    path: /resume                     # qBittorrent's config folder, mounted :ro
    tmp_dir: /tmp                     # a tmpfs: the SQLite store is read from a copy
path_mappings:
  - { from: /downloads, to: /media/downloads, source: qBittorrent resume }
```

Detection, first answer wins: the live client's `resume_data_storage_type`
preference (qBittorrent 4.5.1+), then `Session\ResumeDataStorageType` in
`qBittorrent.conf` (absent means Legacy), then whichever store changed most
recently. The result, with the reason, is on the dashboard's System page and
in `/__airrbag/health`. A store that lags the live client (more than 5% or 10
torrents missing), or that has not changed for `stale_after` while no live
client answers, is marked out of date: its matches still count, but a file it
does not know about stays `keep`, never `safe`.

The SQLite store is never opened in place. qBittorrent keeps it in WAL mode,
and a reader of a WAL database has to write the `-shm` index beside it, which
a read-only mount refuses; SQLite's `immutable` mode would skip the `-wal`
file, which holds every change since the last checkpoint. So airrbag copies
`torrents.db` and `torrents.db-wal` into `tmp_dir`, checks that neither
changed during the copy, reads the copy and deletes it, at most once a minute
while the database keeps changing. Only the needed columns are read
(`torrent_id`, `name`, `category`, `target_save_path`,
`libtorrent_resume_data`, `metadata`); never `ssl_private_key`. Use
`qbittorrent-sqlite` with `path: .../torrents.db` to read the database
without detection.
