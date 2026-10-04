# Notes for AI agents and contributors

- Read docs/DESIGN.md first. The verdict is a pure function in
  internal/verdict; keep I/O out of it.
- Safety rules that must not regress (each has a test):
  - a private torrent that seeds from the library path and is still owed is `keep`
  - a hardlink of a seed is `frees-nothing`, never `keep`
  - an unreachable client plus a private indexer is `keep` when fail_closed
  - a partial client view never concludes "torrent gone"
  - verdicts are never shown to callers the *Arr does not authenticate
  - the override grant is single use
- Never log secrets. Errors from clients that put keys in URLs must not
  include the URL (see internal/clients/sabnzbd).
- The proxy must never change API, asset or websocket bytes; only HTML gets
  the script tag.
- Fixtures in testdata/ are recorded from real instances and scrubbed. Scrub
  API keys, passkeys, download URLs and guids before committing new ones.
- Conventional Commits; release-please owns versions and tags.
