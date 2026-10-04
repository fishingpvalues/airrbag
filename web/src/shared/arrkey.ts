// The *Arr web UI authenticates its own API calls with the key it reads from
// /initialize.json, which the *Arr serves to whoever may see the UI: a signed-
// in session (Forms), anyone behind an external auth proxy (External), local
// addresses when auth is disabled for them. Airrbag's endpoints accept the
// same credentials, so the browser code does exactly what the *Arr UI does.
// Without this, a cookie-less setup (External auth) would never get badges.

let cached: Promise<string> | null = null;

export function arrApiKey(urlBase: string, f: typeof fetch): Promise<string> {
  if (!cached) {
    cached = f(`${urlBase.replace(/\/+$/, "")}/initialize.json`, {
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    })
      .then((r) => (r.ok ? r.json() : {}))
      .then((j: { apiKey?: unknown }) => (typeof j.apiKey === "string" ? j.apiKey : ""))
      .catch(() => "");
  }
  return cached;
}
