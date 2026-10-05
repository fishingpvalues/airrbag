// Airrbag browser script, injected into the *Arr UI by the Airrbag proxy.
//
// Two jobs:
//   1. Badges: on a movie/series/artist/author page, show where every file
//      came from (Usenet, public torrent, private torrent) and what deleting
//      it would do.
//   2. Delete guard: before the UI sends a DELETE that removes files, ask
//      Airrbag; if a private seed is still owed, show a confirmation dialog.
//
// Rules for this file: never throw into the host app, never block the UI if
// Airrbag is unreachable (the server-side guard still protects), and keep
// every app-specific selector in APPS below.

import { confirmDelete } from "./guard";
import { invalidateBadges, startBadges } from "./badges";
import { cfg } from "./config";
import { handleRefusal, parseRefusal } from "./refusal";

function patchXHR(): void {
  const proto = XMLHttpRequest.prototype;
  const open = proto.open;
  const send = proto.send;
  const setHeader = proto.setRequestHeader;
  type Tagged = XMLHttpRequest & { __airrbag?: { method: string; url: string; headers: Record<string, string> } };

  proto.open = function (this: Tagged, method: string, url: string | URL, ...rest: unknown[]) {
    this.__airrbag = { method: String(method), url: String(url), headers: {} };
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return (open as any).call(this, method, url, ...rest);
  } as typeof proto.open;

  proto.setRequestHeader = function (this: Tagged, name: string, value: string) {
    if (this.__airrbag) this.__airrbag.headers[name] = value;
    return setHeader.call(this, name, value);
  };

  proto.send = function (this: Tagged, body?: Document | XMLHttpRequestBodyInit | null) {
    const tag = this.__airrbag;
    if (!tag || !isGuarded(tag.method, tag.url)) {
      return send.call(this, body);
    }
    const bodyText = typeof body === "string" ? body : "";
    // Safety net: a refusal from the server-side guard becomes the dialog.
    this.addEventListener("load", () => {
      if (this.status >= 200 && this.status < 300) invalidateBadges();
      const refusal = parseRefusal(this.status, typeof this.responseText === "string" ? this.responseText : "");
      if (refusal) {
        handleRefusal({ method: tag.method, url: tag.url, body: bodyText, headers: tag.headers }, refusal).catch(() => undefined);
      }
    });
    confirmDelete(tag.method, tag.url, bodyText).then(
      (ok) => (ok ? send.call(this, body) : this.abort()),
      () => send.call(this, body),
    );
  };
}

function headersOf(input: RequestInfo | URL, init?: RequestInit): Record<string, string> {
  const out: Record<string, string> = {};
  const h = new Headers((init && init.headers) || (input instanceof Request ? input.headers : undefined));
  h.forEach((v, k) => (out[k] = v));
  return out;
}

function patchFetch(): void {
  const orig = window.fetch;
  window.fetch = function (input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const method = (init && init.method) || (input instanceof Request ? input.method : "GET");
    const url = input instanceof Request ? input.url : String(input);
    if (!isGuarded(method, url)) {
      return orig.call(this, input, init);
    }
    const body = init && typeof init.body === "string" ? init.body : "";
    const watch = (p: Promise<Response>) =>
      p.then((res) => {
        if (res.ok) invalidateBadges();
        if (res.status === 409 || res.status === 503) {
          res
            .clone()
            .text()
            .then((text) => {
              const refusal = parseRefusal(res.status, text);
              if (refusal) handleRefusal({ method, url, body, headers: headersOf(input, init) }, refusal).catch(() => undefined);
            })
            .catch(() => undefined);
        }
        return res;
      });
    return confirmDelete(method, url, body).then(
      (ok) => (ok ? watch(orig.call(this, input, init)) : Promise.reject(new DOMException("Cancelled by Airrbag", "AbortError"))),
      () => watch(orig.call(this, input, init)),
    );
  };
}

function isGuarded(method: string, url: string): boolean {
  return method.toUpperCase() === "DELETE" && /\/api\/v\d+\//.test(url) && url.indexOf("/__airrbag/") < 0;
}

function main(): void {
  if (!cfg.base) return;
  try {
    patchXHR();
    patchFetch();
  } catch (e) {
    console.warn("airrbag: delete guard disabled", e);
  }
  try {
    startBadges();
  } catch (e) {
    console.warn("airrbag: badges disabled", e);
  }
}

main();
