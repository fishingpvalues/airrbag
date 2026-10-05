// Run with: npm test (node --test, type stripping; no DOM needed).
import { test } from "node:test";
import assert from "node:assert/strict";
import { decide } from "../src/decide.ts";
import type { FileVerdict } from "../src/types.ts";

const f = (v: FileVerdict["verdict"], extra: Partial<FileVerdict> = {}): FileVerdict => ({
  fileId: 1, parentId: 1, path: "/x", size: 1, verdict: v, protocol: "unknown", private: false,
  relation: "unknown", reasons: [], ...extra,
});

test("unknown with needsConfirm asks (the 0.4.0 bug: it was skipped)", () => {
  const d = decide({ deletesFiles: true, files: [f("unknown", { needsConfirm: true, severity: "warning" })] });
  assert.equal(d.kind, "unknown");
  assert.equal(d.files.length, 1);
});

test("unknown without the field (older server) still asks", () => {
  assert.equal(decide({ deletesFiles: true, files: [f("unknown")] }).kind, "unknown");
});

test("unknown with guard.unknown: allow does not ask", () => {
  assert.equal(decide({ deletesFiles: true, files: [f("unknown", { needsConfirm: false })] }).kind, "none");
});

test("keep wins over unknown and frees-nothing", () => {
  const d = decide({ deletesFiles: true, files: [f("unknown", { needsConfirm: true }), f("frees-nothing"), f("keep")] });
  assert.equal(d.kind, "keep");
  assert.equal(d.files.length, 1);
});

test("frees-nothing informs, safe passes", () => {
  assert.equal(decide({ deletesFiles: true, files: [f("frees-nothing")] }).kind, "frees-nothing");
  assert.equal(decide({ deletesFiles: true, files: [f("safe")] }).kind, "none");
});

test("errors: fail-closed asks, fail-open passes, no files passes", () => {
  assert.equal(decide({ deletesFiles: true, error: "x", failClosed: true }).kind, "unverified");
  assert.equal(decide({ deletesFiles: true, error: "x" }).kind, "none");
  assert.equal(decide({ deletesFiles: false, files: [f("keep")] }).kind, "none");
});
