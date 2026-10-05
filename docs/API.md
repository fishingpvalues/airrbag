# API

Airrbag's own endpoints live under each listener, next to the proxied *Arr,
at `/__airrbag/`. They require the same credentials as the *Arr (session
cookie, `X-Api-Key` header or `apikey` query parameter, or a trusted SSO
header) and never reveal a verdict to anyone else. Unauthenticated calls get
`401`; 30 failures a minute from one address get `429`.

Every `POST` must carry `X-Airrbag-Request: 1` and, when the browser sends
them, a same-origin `Origin` and `Sec-Fetch-Site`; otherwise `403` (CSRF).

| Route | Purpose |
|-------|---------|
| `GET /__airrbag/api/files?parentId=` | Verdicts for one movie, series, artist or author |
| `GET /__airrbag/api/resolve?path=` | UI route to parent id |
| `POST /__airrbag/api/check` | Verdicts for the files a given DELETE would remove |
| `POST /__airrbag/api/grant` | Single-use confirmation for one DELETE |
| `GET /__airrbag/api/lists` | Whole-library grouping |
| `GET /__airrbag/api/info` | App, guard state, version |
| `GET /__airrbag/health` | `{"status":"ok"}` for anyone; details (version, index, client reachability) when signed in |
| `GET /__airrbag/metrics` | Prometheus text format; signed-in only unless `metrics.public` |

## File verdict

Every endpoint that returns files returns this object per file.

```json
{
  "fileId": 652,
  "parentId": 1,
  "parentTitle": "The Dark Knight",
  "path": "/movies/The Dark Knight (2008)/The.Dark.Knight.2008.1080p.BluRay.x264-GRP.mkv",
  "relativePath": "The.Dark.Knight.2008.1080p.BluRay.x264-GRP.mkv",
  "size": 9123456789,
  "verdict": "frees-nothing",
  "protocol": "torrent",
  "private": true,
  "relation": "hardlink",
  "reasons": [
    "private tracker seeding obligation not met (or unknown)",
    "hardlink of the seeding file: the seed survives, but no space is freed until the torrent goes"
  ],
  "reason": "hardlink of the seeding file: the seed survives, but no space is freed until the torrent goes",
  "evidence": ["history: torrent via PrivateHD into qBittorrent"],
  "severity": "info",
  "needsConfirm": false,
  "source": "qBittorrent",
  "indexer": "PrivateHD",
  "client": "qBittorrent",
  "tracker": "tracker.privatehd.example",
  "ratio": 0.42,
  "seedingTimeSeconds": 259200,
  "torrentName": "The.Dark.Knight.2008.1080p.BluRay.x264-GRP",
  "torrentState": "stalledUP"
}
```

| Field | Values | Meaning |
|-------|--------|---------|
| `verdict` | `keep`, `frees-nothing`, `safe`, `unknown` | What deleting the file does; see the README |
| `protocol` | `torrent`, `usenet`, `direct`, `unknown` | How the file was downloaded. `direct` is a downloader with no swarm (Xunlei) |
| `private` | bool | The torrent, its tracker or its indexer is private |
| `relation` | `same-path`, `hardlink`, `copy`, `none`, `unknown` | The library file compared with the torrent's files |
| `reasons` | string list | Every rule that fired, in order |
| `reason` | string | One-line summary for a tooltip or dialog. For `unknown`: "airrbag can't prove where this file came from" |
| `evidence` | string list | How the protocol was proven: history, inode match, import folder, client history, indexer proxy, name match. Never empty |
| `severity` | `danger`, `warning`, `info`, `ok` | `keep`, `unknown`, `frees-nothing`, `safe`. For badge and dialog styling |
| `needsConfirm` | bool | Ask before deleting: always for `keep`; for `unknown` unless `guard.unknown` is `allow` |
| `source` | string | The client or folder the evidence came from |

`indexer`, `client`, `tracker`, `ratio`, `seedingTimeSeconds`, `torrentName`
and `torrentState` are present only when known.

## What the UI does with it

| `verdict` / `needsConfirm` | Dialog |
|---------------------------|--------|
| `keep` | Danger dialog: the files, tracker and seeding time, "Delete anyway" registers a grant |
| `unknown`, `needsConfirm: true` | Warning dialog, asked once: "airrbag can't prove where this file came from", one click continues and registers a grant |
| `unknown`, `needsConfirm: false` | No dialog; the badge says unknown |
| `frees-nothing` | Informational note: no space is freed |
| `safe` | Nothing |

## Refused delete (409)

A DELETE the guard refuses answers `409 Conflict`:

```json
{
  "error": "airrbag: this delete would break a private-tracker seed that is still owed",
  "message": "airrbag: this delete would break a private-tracker seed that is still owed",
  "reason": "keep",
  "keep": [ { "...file verdict..." } ],
  "unknown": [],
  "override": "confirm in the Airrbag dialog"
}
```

`reason` is `keep` when any file is `keep`, else `unknown`: with
`guard.unknown: block` for every caller, and with the default `confirm` for a
browser request (`Sec-Fetch-*` or a cookie) that arrives without the dialog's
grant. The message then reads "provenance unknown: airrbag could not prove
this file is safe to delete". `message` is the field the
*Arr frontends print in their own error toast.

When the check itself cannot run and `guard.fail_closed` is on, the answer is
`503` with `error` and `override`.

## Overrides

- `POST /__airrbag/api/grant` with `{"method":"DELETE","url":"/api/v3/moviefile/12","reason":"..."}`
  registers a single-use grant for exactly that request (method, path, query,
  body hash) **and that signed-in caller**, valid for `guard.grant_ttl` (60 s).
  The DELETE must come with the same credentials (the *Arr UI's API key, or
  the same session).
- Only with `guard.allow_override_header: true`: `X-Airrbag-Override: <reason>`
  or `?airrbagOverride=<reason>` on the DELETE itself, from a signed-in caller.
  Otherwise both are ignored. Either way they are removed before the request
  reaches the *Arr.

## Verdict cause

Every file verdict carries `cause`, the rule that produced it, and `reason`,
the one-line sentence built from it. `unreachableClients` lists the torrent
clients that could not be asked, when that matters.

| cause | verdict | meaning |
|---|---|---|
| `seeds-from-file` | keep | a private torrent seeds from this exact file and is still owed |
| `private-uncompared` | keep | a private torrent is still owed and its files could not be compared |
| `client-unreachable` | keep | a torrent client is down, so a seed from this file cannot be ruled out |
| `torrent-evidence` | keep | the file came from a torrent whose obligation cannot be checked |
| `hardlink` | frees-nothing | a hardlink of the seeding file |
| `unknown-hardlink` | frees-nothing | origin unknown, another hardlink holds the bytes |
| `copy` | safe | an independent copy of the seed |
| `seed-ends` | safe | the torrent seeds from this file but nothing is owed |
| `torrent-gone` | safe | the torrent is no longer in the client |
| `usenet`, `direct` | safe or frees-nothing | no swarm, nothing owed |
| `no-history` | unknown | no evidence at all |
| `uncompared` | unknown | public torrent, files could not be compared |
| `file-missing` | safe | the library file no longer exists |
