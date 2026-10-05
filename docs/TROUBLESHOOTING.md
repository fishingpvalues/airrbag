# Troubleshooting

**No badges.** Open the browser console and look for `airrbag:` messages.
`curl http://<listener>/__airrbag/health` shows the detected app and whether
each download client answers.

**Many files are `unknown`.** They were imported before the history the *Arr
kept, by hand, or by another tool, and nothing else ties them to a download.
`evidence` on each file says what was checked. Adding the Usenet client,
NZBHydra2 or the direct-download folder gives airrbag more to match;
`guard.unknown` decides how deletes of the rest are handled.

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
