# Security policy

## Reporting a vulnerability

Report privately through
[GitHub security advisories](https://github.com/fishingpvalues/airrbag/security/advisories/new),
not as a public issue. Expect a first answer within a week, and a fix or a
documented mitigation for anything confirmed before it is disclosed.

## Supported versions

Only the latest release receives fixes. Releases are signed (cosign keyless)
and carry SBOM and SLSA provenance attestations:

```sh
cosign verify ghcr.io/fishingpvalues/airrbag:<version> \
  --certificate-identity-regexp 'https://github.com/fishingpvalues/airrbag/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
gh attestation verify oci://ghcr.io/fishingpvalues/airrbag:<version> --owner fishingpvalues
```

## Threat model

### What airrbag holds

- The API key of every *Arr instance it fronts. With it, anyone can do
  everything the *Arr can: delete libraries, change settings, read indexer
  credentials.
- Download-client credentials (qBittorrent, Transmission, Deluge, rTorrent
  passwords, SABnzbd API key, NZBHydra2 basic auth).
- Read-only access to the media and seed directories, to compare inodes.

airrbag never stores these anywhere but in memory, never writes to the media,
and never sends a credential to any host but the one it belongs to.

### Who can reach it

airrbag is a reverse proxy in front of the *Arr web UI. It is meant for a LAN,
a VPN or a tailnet, like the *Arr behind it, and not for the public internet.

### Trust boundaries and controls

| Asset / action | Who may | Control |
|----------------|---------|---------|
| airrbag's pages and API | whoever the *Arr signs in | API key (constant time), or credentials replayed against the *Arr's `/system/status`; optional trusted SSO header |
| Being "local" to the *Arr | the real client | `X-Forwarded-For`/`Forwarded`/`X-Real-IP` only from `auth.trusted_proxies` |
| Overriding the delete guard | the signed-in user who confirmed the dialog | grant bound to identity + method + URL + body, single use, `guard.grant_ttl` (60 s); CSRF: same origin and `X-Airrbag-Request: 1`; raw override header off by default |
| Seeing verdicts and file lists | signed-in users of *that* instance | `dashboard.cross_instance` off by default |
| Secrets | nobody | never in responses; scrubbed from logs, errors and metrics; config must be `chmod 600`; Docker secrets, `${file:}` and age encryption supported; placeholders refused |
| Outbound requests | only to configured/discovered *Arrs and clients | host allowlist on every HTTP client, redirects included; build fails if code bypasses it |
| Brute force | - | 30 failed sign-ins per minute per address, then `429` |

### What an attacker can do with...

- **Network access only (no *Arr credentials):** load the static dashboard
  shell and the injected script (no data), and `/__airrbag/health`, which says
  `ok`. Everything else answers `401`. Spoofed forwarding headers are dropped.
- **An *Arr session or API key:** everything the *Arr allows anyway, plus
  read access to airrbag's verdicts for that instance and the ability to
  confirm a guarded delete. The guard protects against mistakes and against
  other tools, not against a signed-in user who deliberately confirms.
- **A malicious web page in the same browser:** nothing. Airrbag's
  state-changing calls need a custom header that a cross-site page cannot send
  without a CORS preflight airrbag never answers, and the dashboard cannot be
  framed.

### Known limits

- **airrbag only guards traffic that passes through it.** A client that talks
  to the *Arr's own port bypasses the badges and the guard. Firewall the
  *Arr's port to airrbag (and the tools that must reach it directly).
- **"Authentication disabled for local addresses" on the *Arr** trusts
  whatever address airrbag reports. airrbag reports the real client, or, behind
  a trusted proxy, the address that proxy reports. Prefer real authentication
  on the *Arr.
- **The browser authenticates with the *Arr's API key**, read from the *Arr's
  `/initialize.json` exactly as the *Arr UI does. It is visible to anyone who
  can sign in to the *Arr UI, which is the *Arr's own design.
- **Go cannot reliably wipe secrets from memory.** They live in ordinary
  strings for the life of the process. Protect the host, and do not run
  airrbag where untrusted code shares the memory or the swap.
- **No built-in 2FA.** Use an SSO proxy with 2FA and `auth.forward_auth_header`.

## Hardening checklist

- `chmod 600` the config, owned by the uid the container runs as.
- Pass secrets as Docker secrets (`NAME_FILE`) or `${file:...}`, or encrypt
  the config with age.
- `read_only: true`, `cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]`.
- Bind to `127.0.0.1` or a VPN/tailnet address; never `0.0.0.0` on a host with
  a public interface.
- List your reverse proxy in `auth.trusted_proxies`; add an SSO proxy with
  2FA if more than one person signs in.
- Firewall the *Arr's own ports.

## Scope

In scope, for example: reaching an *Arr, a download client or airrbag's data
without valid *Arr credentials; leaking a secret in a response, log or metric;
making airrbag request a host it was not configured for (SSRF); spending
another user's grant or bypassing the guard without a grant; spoofing the
client address the *Arr sees.

Out of scope: a signed-in *Arr user confirming the dialog, or sending
`X-Airrbag-Override` while `guard.allow_override_header` is enabled. Both are
intended.

## Continuous checks

Every change runs golangci-lint (gosec), govulncheck, gitleaks over the full
history, a Trivy scan of the image (HIGH/CRITICAL with a fix fail the build),
CodeQL, and OpenSSF Scorecard. Actions are pinned to commit SHAs and updated
by Renovate.
