// Safety net: when a DELETE reaches the server-side guard anyway (the
// pre-check could not run, a race, another tab) the *Arr UI receives a 409
// it would show as nothing or as a generic error. Any refusal carrying the
// airrbag marker is turned into the Airrbag dialog, with the same text the
// server sent and the option to confirm and repeat the request.

import { api } from "./config";
import type { FileVerdict } from "./types";
import { COLORS, modal } from "./ui";

export interface Refusal {
  message: string;
  description?: string;
  airrbag: true;
  keep?: FileVerdict[];
}

export interface SentRequest {
  method: string;
  url: string;
  body: string;
  headers: Record<string, string>;
}

// The browser's own fetch, captured before main.ts patches it, so the retry
// is not intercepted again.
const nativeFetch: typeof fetch = window.fetch.bind(window);

export function parseRefusal(status: number, text: string): Refusal | null {
  if ((status !== 409 && status !== 503) || text.indexOf('"airrbag":true') < 0) return null;
  try {
    const r = JSON.parse(text) as Refusal;
    return r && r.airrbag === true && typeof r.message === "string" ? r : null;
  } catch {
    return null;
  }
}

let showing = false;

export async function handleRefusal(req: SentRequest, refusal: Refusal): Promise<void> {
  if (showing) return;
  showing = true;
  try {
    const go = await modal({
      title: "Airrbag blocked this delete",
      color: COLORS.keep,
      intro: refusal.message + (refusal.description ? ` ${refusal.description}` : ""),
      files: refusal.keep || [],
      confirmLabel: "Delete anyway",
      danger: true,
      requireAck: "I understand this can count as a hit-and-run.",
    });
    if (!go) return;
    const url = new URL(req.url, location.href);
    await api("/grant", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ method: req.method, url: url.pathname + url.search, body: req.body, reason: "confirmed after a guard refusal" }),
    });
    const r = await nativeFetch(url.toString(), {
      method: req.method,
      headers: req.headers,
      body: req.body || undefined,
      credentials: "same-origin",
    });
    if (r.ok) {
      // The *Arr's own state still believes the delete failed; reload so the
      // page shows what is really on disk.
      location.reload();
    } else {
      await modal({
        title: "The delete still failed",
        color: COLORS.unknown,
        intro: `The *Arr answered ${r.status}. Nothing was changed by Airrbag.`,
        files: [],
        confirmLabel: "Close",
        danger: false,
      });
    }
  } finally {
    showing = false;
  }
}
