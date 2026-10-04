// Colours and labels shared by the injected badges and the dashboard. They
// follow the Servarr label palette (Sonarr/Radarr dark theme) so Airrbag reads
// as part of the *Arr UI rather than a widget pasted on top of it.

import type { FileVerdict, Verdict } from "../types";

export type Source = "usenet" | "torrent" | "private" | "unknown";

export const COLORS: Record<Verdict | Source, string> = {
  usenet: "#5d9cec",
  torrent: "#ff902b",
  private: "#f05050",
  unknown: "#7a7a7a",
  keep: "#f05050",
  "frees-nothing": "#a46bd6",
  safe: "#27c24c",
};

export const VERDICT_LABEL: Record<Verdict, string> = {
  keep: "Keep",
  "frees-nothing": "Frees nothing",
  safe: "Safe to delete",
  unknown: "Unknown",
};

export const VERDICT_HELP: Record<Verdict, string> = {
  keep: "Private seed still owed: deleting is a hit-and-run",
  "frees-nothing": "Hardlink of a running seed: safe, but no space is freed",
  safe: "No seed is owed: deleting frees the space",
  unknown: "No download history, or the client could not be asked",
};

export const SOURCE_LABEL: Record<Source, string> = {
  usenet: "Usenet",
  torrent: "Torrent",
  private: "Private",
  unknown: "Unknown",
};

export function sourceOf(f: Pick<FileVerdict, "protocol" | "private">): Source {
  if (f.protocol === "usenet") return "usenet";
  if (f.protocol === "torrent") return f.private ? "private" : "torrent";
  return "unknown";
}

// Lock glyph shown on private-tracker labels (24x24 viewBox).
export const LOCK_PATH = "M12 1a5 5 0 0 0-5 5v4H5v13h14V10h-2V6a5 5 0 0 0-5-5zm-3 9V6a3 3 0 0 1 6 0v4H9z";

// The Airrbag mark (512x512 viewBox): a shield with an inflated cushion.
export const LOGO_SHIELD =
  "M256 26c-6 0-12 1.4-17.4 4.2C190 54.8 136 70 78 74.6 61.4 76 48 89.8 48 106.5V234c0 118.6 76.6 207.7 192.3 248.9 10.2 3.6 21.2 3.6 31.4 0C387.4 441.7 464 352.6 464 234V106.5c0-16.7-13.4-30.5-30-31.9C376 70 322 54.8 273.4 30.2 268 27.4 262 26 256 26z";
export const LOGO_BAG =
  "M256 92c-66.3 0-120 51.6-120 118.5 0 57.4 45.6 105.8 101.3 115.6l-11.8 25.4c-2.3 4.9 1.3 10.5 6.7 10.5h47.6c5.4 0 9-5.6 6.7-10.5l-11.8-25.4C330.4 316.3 376 267.9 376 210.5 376 143.6 322.3 92 256 92z";
export const ACCENT = "#1db79e";
