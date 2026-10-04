# Security policy

## Reporting a vulnerability

Report privately through
[GitHub security advisories](https://github.com/fishingpvalues/airrbag/security/advisories/new),
not as a public issue. Expect a first answer within a week.

## Scope

Airrbag holds *Arr API keys and download-client credentials and proxies the
*Arr web UI. In scope, for example:

- reaching an *Arr or a download client without valid *Arr credentials
- leaking secrets in logs, errors or responses
- making Airrbag request a URL it was not configured for (SSRF)
- bypassing the delete guard without an override or a confirmed dialog

Out of scope: an authenticated *Arr user confirming the dialog or sending
`X-Airrbag-Override`. That override is intended.

## Network

airrbag contacts only the configured *Arr instances and the download clients
they list or the config names. Every outbound HTTP request passes a host
allowlist (`internal/egress`). A request it lets through to any other host is a
vulnerability.

## Supported versions

Only the latest release receives fixes.
