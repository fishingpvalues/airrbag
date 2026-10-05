# How airrbag works

For each file:

1. The *Arr history links the file to its download. The import event carries
   the file id; the grab event carries the protocol, indexer, client and
   download id, which for a torrent is its info hash.
2. The torrent client is asked for that torrent: private flag, trackers,
   ratio, seeding time, files. A torrent seeding straight from the library is
   also found by path when the history no longer mentions it.
3. The library file and the torrent's files are stat'ed. Same device and inode
   means hardlink; the library path inside the torrent's content path means
   the same file.
4. The tracker's rule decides whether the seed is still owed.
5. When the history has no record of the file, airrbag looks for evidence
   elsewhere, strongest first: the same bytes (inode) as any torrent's file
   or a file in a direct-download folder; the folder the *Arr imported it
   from (a torrent's content or SABnzbd's finished folder); the release name
   in SABnzbd's history, NZBHydra2's download history, a direct-download
   folder or the torrent names. Each verdict lists the chain in `evidence`.

| Verdict | Meaning | Delete |
|---------|---------|--------|
| `keep` | private seed still owed, and the delete removes its data | refused, unless confirmed |
| `frees-nothing` | hardlink of a running seed | allowed, the UI says no space is freed |
| `safe` | Usenet, separate copy, finished or removed torrent | allowed |
| `unknown` | no evidence of where the file came from | per `guard.unknown`: ask (default), refuse or allow |

**Torrent evidence is never `unknown`.** A file that history, a private
indexer, a tracker, an inode or a name ties to a torrent, but whose seed
status cannot be checked (client down, files unreadable), is `keep`. Only a
file with no evidence at all is `unknown`.

A private torrent that no rule covers and that has no `trackers.default` is
never considered done.

## In the UI

The browser script draws a small panel on every movie, series, artist and
author page, and a source badge next to file names in tables. Hovering shows
the indexer, client, tracker, ratio, seeding time and the reasoning.

Before the UI sends a delete that removes `keep` files, a dialog names the
files, the tracker and the seeding time, and asks for an explicit confirmation.
Confirming registers a single-use grant for exactly that request. A
`frees-nothing` delete gets a short note instead.

If a delete reaches the server-side guard anyway (the check could not run,
another tab, a race), the refusal is not left to the *Arr UI, whose delete
handlers show nothing for a failed request: the script recognises the guard's
409 and opens the same dialog with the server's message and the option to
confirm and repeat the request.

The script listens for the DELETE request itself rather than for particular
buttons, so the check does not depend on how each app draws its dialogs. The
panel falls back to a floating corner badge when a UI update moves the page
header.

## On the server

The same check runs on every DELETE that passes through the proxy, whatever
sent it. A refused request gets `409 Conflict` in the Servarr error shape, so
any client that shows *Arr errors shows a useful line:

```json
{
  "message": "airrbag: kept, this file is the seeding data of a private torrent on tracker.example (seeded 3d 4h of 14d required). Delete the torrent first, or confirm in the airrbag dialog.",
  "description": "Deleting now ends a private-tracker seed whose obligation is not met: a hit-and-run.",
  "airrbag": true,
  "keep": [ ... ]
}
```

The message follows the verdict's cause: a file kept because a torrent client
did not answer reads "airrbag can't reach qBittorrent, so it can't rule out
that this file belongs to a seeding torrent", not "seeding data". Every cause
is listed in [API.md](API.md#verdict-cause).

If the check itself cannot run (the *Arr or a client does not answer) and
`guard.fail_closed` is on, the answer is `503`. With `guard.unknown: block`,
deletes of files with no provenance evidence are refused as well. Nothing else
is ever blocked.

API clients can override deliberately with `X-Airrbag-Override: <reason>`.
The reason is logged.

## Dashboard

`/airrbag` (a redirect) or `/__airrbag/` on any listener opens the dashboard, drawn like the *Arr UIs
(sidebar, page toolbar, dense tables, the same labels) in dark or light,
following the system setting:

- **Overview**: Keep, Frees nothing, Safe to delete and Unknown across every
  instance, with the space a delete would free, and client health.
- **Files**: every library file with its source, indexer, tracker, ratio,
  seeding time against the tracker's requirement and the verdict; sortable,
  filterable by instance, verdict and source, searchable.
- **Guard**: recent blocked, would-block (dry run) and overridden deletes.
- **Settings**: the effective configuration, secrets shown only as set or not
  set, and a client connectivity test.
- **System**: version, uptime, health and metrics endpoints.

The dashboard is read-only and uses the same *Arr sign-in as the API. Anyone
who can sign in to one proxied *Arr sees the file lists of all instances the
process fronts.
