// Run with: npm test (node --test, type stripping; no DOM needed).
import { test } from "node:test";
import assert from "node:assert/strict";
import { SOURCE_LABEL, sourceOf } from "../src/shared/tokens.ts";

test("every protocol the server sends has its own label", () => {
  assert.equal(sourceOf({ protocol: "usenet", private: false }), "usenet");
  assert.equal(sourceOf({ protocol: "torrent", private: false }), "torrent");
  assert.equal(sourceOf({ protocol: "torrent", private: true }), "private");
  // Both used to fall through to "Unknown" in the badge.
  assert.equal(sourceOf({ protocol: "direct", private: false }), "direct");
  assert.equal(sourceOf({ protocol: "library", private: false }), "library");
  assert.equal(sourceOf({ protocol: "unknown", private: false }), "unknown");
  assert.equal(SOURCE_LABEL.library, "Library");
});
