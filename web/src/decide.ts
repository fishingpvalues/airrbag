// Which dialog, if any, a delete needs. Pure, so it can be tested without a
// DOM: guard.ts renders whatever this returns.

import type { CheckResponse, FileVerdict } from "./types";

export type DialogKind = "none" | "unverified" | "keep" | "unknown" | "frees-nothing";

export interface Decision {
  kind: DialogKind;
  files: FileVerdict[];
}

export function decide(res: CheckResponse): Decision {
  if (!res.deletesFiles) return { kind: "none", files: [] };
  if (res.error) return { kind: res.failClosed ? "unverified" : "none", files: [] };
  const files = res.files || [];
  const keep = files.filter((f) => f.verdict === "keep");
  if (keep.length > 0) return { kind: "keep", files: keep };
  // needsConfirm comes from the server: false only with guard.unknown: allow.
  // An older server without the field is treated as asking (fail safe).
  const unknown = files.filter((f) => f.verdict === "unknown" && f.needsConfirm !== false);
  if (unknown.length > 0) return { kind: "unknown", files: unknown };
  const frees = files.filter((f) => f.verdict === "frees-nothing");
  if (frees.length > 0) return { kind: "frees-nothing", files: frees };
  return { kind: "none", files: [] };
}
