import assert from "node:assert/strict";
import { test } from "node:test";
import { citedLines } from "./citation.ts";

test("a PDF citation counts its line from its page's marker", () => {
  const text = "--- Page 1 · PDF text ---\nA\nB\n--- Page 2 · PDF text ---\nC\nD";
  assert.deepEqual(citedLines("page 2, line 2", text), [6, 6]);
  assert.deepEqual(citedLines("lines 2-3", text), [2, 3]);
  assert.equal(citedLines("page 3, line 1", text), undefined);
});
