import { test } from "node:test";
import assert from "node:assert/strict";
import { WORKPAPERS } from "./workpapers.ts";

// "(1,180.00)" is -1180, "48,212.60" is 48212.6; in cents, so the arithmetic is exact.
const cents = (amount: string) => {
  const negative = amount.startsWith("(");
  const value = Math.round(Number(amount.replace(/[(),]/g, "")) * 100);
  return negative ? -value : value;
};

test("every illustrative reconciliation ties to nil, one listed item at a time", () => {
  for (const paper of WORKPAPERS) {
    const [bank, ledger, ...items] = paper.rows.map((row) => cents(row[4]));
    let difference = bank - ledger;
    assert.equal(difference, Math.round(paper.diff[1] * 100), `${paper.head}: statement less ledger`);
    items.forEach((item, i) => {
      assert.ok(item < 0, `${paper.head}: each reconciling item reduces the difference`);
      difference += item;
      assert.equal(difference, Math.round(paper.diff[i + 2]! * 100), `${paper.head}: difference after item ${i + 1}`);
    });
    assert.equal(difference, 0, `${paper.head}: ties`);
    // The proposed entry records the one item not yet in the ledger, for the same amount.
    assert.ok(paper.aje.includes(paper.rows[4][4].replace(/[()]/g, "")), `${paper.head}: entry amount`);
  }
});

test("the reviewer's signature and sign-off line name the same person", () => {
  for (const paper of WORKPAPERS) assert.ok(paper.meta.startsWith(paper.signer), paper.head);
});
