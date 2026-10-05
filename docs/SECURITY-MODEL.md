# Security model

airrbag holds every *Arr API key and download-client password you give it, so
it is built to be no easier to get into than the *Arr itself, and harder to
get secrets out of. The threat model is in [SECURITY.md](../SECURITY.md).

**Sign-in is the *Arr's.** airrbag has no login and no user database. A call to
its pages or API is accepted when the caller is signed in to the *Arr:

1. through a trusted SSO proxy (`auth.forward_auth_header`, see below), or
2. with the *Arr's API key (compared in constant time), or
3. with credentials the *Arr itself accepts - session cookie, basic auth, or
   "disabled for local addresses" - checked by replaying them against the
   *Arr's `/system/status` with the real client address.

Accepted checks are cached for 30 s, refused ones for 5 s; 30 failures a minute
from one address get `429`. Verdicts are never shown to a caller the *Arr
rejects, including in a refused delete.

**2FA.** The *Arrs have none, and airrbag deliberately does not invent its own.
Put an SSO proxy with 2FA in front (Authelia, Authentik, oauth2-proxy, or
`tailscale serve` with Tailscale identity headers), list it in
`auth.trusted_proxies` and name its identity header in
`auth.forward_auth_header`. The header is ignored from anyone else.

**Client address.** `X-Forwarded-For`, `Forwarded` and `X-Real-IP` from a
client are dropped; only a peer in `auth.trusted_proxies` may supply them. So
nobody can make the *Arr believe a request is "local". Behind `tailscale serve`
or a reverse proxy on the same host, add that proxy's address (for Docker, the
bridge gateway) to `auth.trusted_proxies`, or every client looks like the proxy.

**Overrides.** "Delete anyway" in the dialog creates a grant that is bound to
the signed-in caller, the method, the exact URL and body, works once and
expires after `guard.grant_ttl`. State-changing calls must carry
`X-Airrbag-Request: 1` and come from the same origin (CSRF). The raw
`X-Airrbag-Override` header is ignored unless `guard.allow_override_header`.

**Secrets never leave.** Every configured secret is scrubbed from logs, error
responses and metrics; a test sends the API key through every endpoint and
fails if it appears anywhere. The dashboard shows secrets only as set/unset.

Open without credentials, and only these:

| Open | Why |
|------|-----|
| `/__airrbag/health` | container healthcheck; says `{"status":"ok"}` and nothing else |
| `/__airrbag/airrbag.js`, `/__airrbag/`, its assets | static code; every data call needs *Arr auth |

airrbag's own responses carry a strict CSP (no inline script or style),
`X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy`, `Permissions-Policy` and
COOP/CORP. The *Arr's own pages pass through with their own headers.

**Deployment.** Bind the listeners to localhost, a LAN you trust or a VPN or
tailnet address, never the internet. Firewall the *Arr's own port so browsers
reach it only through airrbag: airrbag can only guard traffic that passes
through it. The image is distroless and non-root; run it with
`read_only: true`, `cap_drop: [ALL]` and `no-new-privileges` as in
[`examples/docker-compose.yml`](../examples/docker-compose.yml).

## Network

airrbag contacts the *Arr instances in its config and the download clients
those instances list or the config names. Nothing else: no telemetry, no
update checks, no third-party hosts. This is enforced, not promised: every
outbound HTTP client goes through a host allowlist built from exactly those
addresses (`internal/egress`), redirects included, and a test fails the build
if code bypasses it.
