<p align="center">
  <img src="assets/logo.svg" width="96" alt="airrbag logo">
</p>

<h1 align="center">airrbag</h1>

<p align="center">
  Know where every file in your *Arr library came from,<br>
  and stop the deletes that would break a private-tracker seed.
</p>

<p align="center">
  <a href="https://github.com/fishingpvalues/airrbag/releases"><img src="https://img.shields.io/github/v/release/fishingpvalues/airrbag?sort=semver" alt="Release"></a>
  <a href="https://github.com/fishingpvalues/airrbag/actions/workflows/ci.yml"><img src="https://github.com/fishingpvalues/airrbag/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License"></a>
</p>

<p align="center">
  <img src="docs/screenshots/radarr-delete-dialog.png" width="900" alt="airrbag in Radarr: source badges on the file, and the confirmation that opens before a delete would end a private seed">
</p>

airrbag is a reverse proxy in front of [Sonarr](https://sonarr.tv),
[Radarr](https://radarr.video), [Lidarr](https://lidarr.audio), Readarr and
Whisparr. It adds a source badge to every file (Usenet, public torrent,
private torrent) and checks every delete against the seed behind the file.

## Why

Deleting a library file is one of three things, and the *Arr cannot tell them
apart:

| The library file is | Deleting it |
|---------------------|-------------|
| a separate copy, or from Usenet | frees the space; nothing seeds from it |
| a hardlink of a file a torrent seeds | is safe for the seed, but frees no space until the torrent goes too |
| the file a private torrent seeds from | ends the seed early: a hit-and-run |

History says where a file came from. Only the filesystem says which of the
three it is, because two names for the same bytes share an inode. airrbag
compares inodes, asks the torrent client whether the seed is still owed, and
refuses the third kind unless you confirm.

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

Open `http://localhost:17878` instead of Radarr's own port. Radarr works as
before; the badges and the delete check sit on top. `/airrbag` opens the
dashboard.

A hardened compose file is in [`examples/docker-compose.yml`](examples/docker-compose.yml).
The image is a static binary on distroless, non-root, with a read-only root
filesystem, signed with cosign and shipped with an SBOM. Binaries for Linux,
macOS, FreeBSD and Windows are attached to each
[release](https://github.com/fishingpvalues/airrbag/releases).

## Configure

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

One entry under `instances` per *Arr. Download clients are discovered from
the *Arr; only their passwords go here. `path_mappings` translate the paths
the *Arr and the client see into airrbag's view, so the inode comparison can
find the files. `airrbag check-config` validates a file without starting.

Everything else, from secrets in Docker secrets or an age-encrypted file to
tracker rules and the guard's modes, is in
[docs/CONFIGURATION.md](docs/CONFIGURATION.md).

## Supported

- **\*Arrs**: Radarr, Sonarr, Lidarr, Readarr, Whisparr
- **Torrent clients**: qBittorrent 4.1 to 5.x, Transmission 3 and 4, Deluge 2,
  rTorrent 0.9+, and resume data read from disk for when a client's API is
  down or missing: qBittorrent's `BT_backup` or SQLite `torrents.db`
  (detected automatically), Deluge's `state`
- **Usenet and more**: SABnzbd 4, NZBHydra2's download history, Xunlei by
  its download folder

CI runs every change against pinned real instances of Radarr 6, Sonarr 4,
Lidarr 3 and qBittorrent 4.3, 4.6 and 5.x.

## Screenshots

<details>
<summary>Badges</summary>

Source badges on a Radarr movie page: the airrbag panel under the header and
a label next to each file.

![Badges on a Radarr movie page](docs/screenshots/radarr-badges.png)

</details>

<details>
<summary>Dialogs</summary>

When a delete reaches the server-side guard anyway, its refusal opens the
same dialog with the server's message.

![Refusal from the server-side guard](docs/screenshots/radarr-blocked-refusal.png)

</details>

<details>
<summary>Dashboard</summary>

Overview: every verdict across all instances, the space a delete would free,
and client health.

![Dashboard overview](docs/screenshots/dashboard-overview.png)

The same in the light theme, which follows the system setting.

![Dashboard overview, light theme](docs/screenshots/dashboard-overview-light.png)

Files: source, tracker, seeding time against the requirement, and the
verdict, sortable and filterable.

![Dashboard files](docs/screenshots/dashboard-files.png)

Guard: recent blocked, would-block and overridden deletes.

![Dashboard guard log](docs/screenshots/dashboard-guard.png)

</details>

The movie in the Radarr screenshots is a throwaway test fixture (a generated
video registered as The General, 1926, seeded with a private flag from a test
qBittorrent).

## Documentation

- [How it works](docs/HOW-IT-WORKS.md): the evidence chain, the four verdicts, the UI, the server-side guard, the dashboard
- [Configuration](docs/CONFIGURATION.md): every key, secrets, resume files
- [Security model](docs/SECURITY-MODEL.md): sign-in delegated to the *Arr, 2FA through SSO, overrides, network allowlist
- [API](docs/API.md): routes, the verdict JSON, the 409 refusal
- [Troubleshooting](docs/TROUBLESHOOTING.md)
- [Design notes](docs/DESIGN.md)

## Compared with

| Tool | Does | Relation |
|------|------|----------|
| qbitrr | Manages qBittorrent from the *Arrs, including seeding rules | Never looks at library inodes |
| Decluttarr, Cleanuparr | Clean stuck or unwanted downloads out of queues and clients | Act on the client; airrbag guards the library |
| Maintainerr | Deletes watched media by rule | Complementary: point it at the airrbag listener |

## Contributing

Small fixes: open a PR. Larger changes: open an issue first. Build, test and
release notes are in [CONTRIBUTING.md](CONTRIBUTING.md). Report security
issues through
[private advisories](https://github.com/fishingpvalues/airrbag/security/advisories/new);
[SECURITY.md](SECURITY.md) has the scope.

Written with AI assistance (Claude Code), by one maintainer. The decision
logic is a pure function with table tests; behavior that depends on the
*Arrs and download clients is tested against real instances in CI. Read
`git log` before trusting the code.

## License

[Apache-2.0](LICENSE)
