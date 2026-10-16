# Troubleshooting

**No badges.** Open the browser console and look for `airrbag:` messages.
`curl http://<listener>/__airrbag/health` shows the detected app and whether
each download client answers.

**Many files are `Library`.** They have no download on record: added by a
library scan, imported by hand or by another tool, or older than the
history the *Arr keeps. airrbag walked every torrent of every client and
none of them uses these bytes, so deleting them cannot break a seed
(`guard.standalone` decides whether that counts as `safe`). Adding the
Usenet client, NZBHydra2 or the direct-download folder can still name their
origin.

**Many files are `unknown`.** airrbag could not rule out a seed. `evidence`
on each file says why. The common causes: a torrent client is unreachable or
its resume store is stale; a torrent keeps its data under a path no
`path_mappings` rule covers (the evidence names one such torrent: add the
rule); the inode walk hit its limit. `guard.unknown` decides how deletes of
the rest are handled.

**No badges in Whisparr.** Whisparr 2 routes sites as `/site/<slug>`;
airrbag before 0.7 only knew `/series/<slug>`. Update.

**`torrents.db-wal: no space left on device`.** Before 0.7 every *Arr
instance copied qBittorrent's SQLite resume store at the same time. Update,
or give `/tmp` at least twice the size of `torrents.db` plus
`torrents.db-wal`.

**`frees-nothing` and `safe` look wrong.** The paths are not mapped. Compare
`path` in `/__airrbag/api/files?parentId=<id>` with where the file is inside
the airrbag container.

**Lidarr answers `400 Invalid Hostname`.** airrbag passes the browser's
`Host` header through unchanged, so Lidarr checks the name you typed against
its own `allowedHosts`. Add that name there (Settings > General), or open the
UI by a name Lidarr already allows. Calls made inside the docker network
with `Host: airrbag:<port>` hit the same check.

**A delete is refused with 503.** airrbag could not reach the *Arr or a
download client and `guard.fail_closed` is on. Fix the client, or confirm in
the dialog.
