// The browser half of the delete guard. The server refuses a kept delete
// on its own; this turns that refusal into a dialog before it happens, and
// registers a one-shot grant when the user confirms.

import { api } from "./config";
import type { CheckResponse } from "./types";
import { decide } from "./decide";
import { COLORS, modal } from "./ui";

function absolute(url: string): string {
  const u = new URL(url, location.href);
  return u.pathname + u.search;
}

async function grant(method: string, url: string, body: string, reason: string): Promise<void> {
  await api("/grant", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ method, url, body, reason }),
  });
}

/** Resolves true when the request may proceed. */
export async function confirmDelete(method: string, rawURL: string, body: string): Promise<boolean> {
  const url = absolute(rawURL);
  let res: CheckResponse;
  try {
    const r = await api("/check", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ method, url, body }),
    });
    if (!r.ok) return true; // the server-side guard still decides
    res = (await r.json()) as CheckResponse;
  } catch {
    return true;
  }
  const d = decide(res);
  switch (d.kind) {
    case "none":
      return true;
    case "unverified": {
      const go = await modal({
        title: "Airrbag could not check this delete",
        color: COLORS.unknown,
        intro: `Airrbag could not verify the files (${res.error}). If any of them seeds on a private tracker, deleting it now could be a hit-and-run.`,
        files: [],
        confirmLabel: "Delete anyway",
        danger: true,
        requireAck: "I checked the download client myself.",
      });
      if (go) await grant(method, url, body, "unverified, confirmed in dialog");
      return go;
    }
    case "keep": {
      const go = await modal({
        title: d.files.length === 1 ? "This file is still seeding on a private tracker" : `${d.files.length} files are still seeding on private trackers`,
        color: COLORS.keep,
        intro:
          "The torrent seeds from these exact files and its seeding obligation is not met yet. " +
          "Deleting them stops the seed early, which private trackers count as a hit-and-run and can cost you the account.",
        files: d.files,
        confirmLabel: "Delete anyway",
        danger: true,
        requireAck: "I understand this can count as a hit-and-run.",
      });
      if (go) await grant(method, url, body, "hit-and-run risk acknowledged in dialog");
      return go;
    }
    case "unknown": {
      const go = await modal({
        title: d.files.length === 1 ? "Airrbag can't prove where this file came from" : `Airrbag can't prove where ${d.files.length} files came from`,
        color: COLORS.unknown,
        intro:
          "No download history, torrent or Usenet job matches these files. If one of them is the data of a torrent " +
          "airrbag cannot see, deleting it could end a seed. If you know where they came from, go ahead.",
        files: d.files,
        confirmLabel: "Delete",
        danger: true,
      });
      if (go) await grant(method, url, body, "unknown origin, confirmed in dialog");
      return go;
    }
    case "frees-nothing":
      return modal({
        title: "Deleting frees no disk space",
        color: COLORS["frees-nothing"],
        intro:
          "These files are hardlinks of torrents that are still seeding. Deleting them is safe for the seeds, " +
          "but the bytes stay on disk until the torrents are removed as well.",
        files: d.files,
        confirmLabel: "Delete",
        danger: false,
      });
  }
  return true;
}
