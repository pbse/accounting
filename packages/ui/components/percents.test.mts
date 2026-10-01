import assert from "node:assert/strict";
import test from "node:test";
import { percents } from "./percents.ts";

test("shares always add up to 100, the largest remainders rounded up", () => {
  assert.deepEqual(percents([1, 1, 1]), [34, 33, 33]);
  assert.deepEqual(percents([2, 1]), [67, 33]);
  assert.deepEqual(percents([5, 0]), [100, 0]);
  assert.deepEqual(percents([0, 0]), [0, 0]);
  for (const values of [[7, 13, 29, 51], [1, 998, 1], [3, 3, 3, 1]]) assert.equal(percents(values).reduce((a, b) => a + b, 0), 100);
});
