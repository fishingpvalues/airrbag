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

## Versioning

Semantic versioning, pre-1.0. At `0.x` a breaking change bumps the minor,
everything else the patch.

Commits follow [Conventional Commits](https://www.conventionalcommits.org/).
[release-please](https://github.com/googleapis/release-please) collects them
into a release PR, writes [`CHANGELOG.md`](CHANGELOG.md) and bumps
`version.txt`. Merging tags `vX.Y.Z` and builds the images and binaries.

Pin a version in production; `latest` moves.
