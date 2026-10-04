# Contributing

1. `make web build test lint` must pass.
2. Commit subjects are Conventional Commits (`feat:`, `fix:`, `docs:` ...).
   They become the changelog, so write them as release notes.
3. A behavior change comes with a test that fails without it. Verdict logic
   goes into `internal/verdict` table tests.
4. Never bump `version.txt` or create tags. release-please owns both.
5. App-specific UI selectors live only in `web/src/config.ts`.

Install the git hooks with `lefthook install`.

Integration tests need Docker:
`scripts/integration.sh radarr lscr.io/linuxserver/radarr:<tag> 7878`.
