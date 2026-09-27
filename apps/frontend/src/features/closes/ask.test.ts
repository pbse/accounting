import assert from "node:assert/strict";
import { test } from "node:test";
import { clientAsk } from "./ask.ts";

test("only open accounts of the compared capture that no live request covers are asked about", () => {
  const now = Date.parse("2026-10-02T12:00:00Z");
  const flux = {
    this: { source: "quickbooks", snapshot_id: "7", stale: false },
    last: { snapshot_id: "6", stale: false },
    changes: [
      { account_id: "21", questions: ["Software: CloudSuite?", "Software: Figma?"] },
      { account_id: "22", questions: ["Rent: Landlord?"] },
      { account_id: "25", questions: ["Utilities: PSEG?"] },
      { account_id: "26", questions: ["Water: City?"] },
      { account_id: "27", questions: ["Phone: Verizon?"] },
      { account_id: "28", questions: ["Travel: Delta?"] },
      { account_id: "30", questions: [] },
      { account_id: "31", questions: ["Meals: Cafe?"] },
      { account_id: "29", questions: ["Fuel: Shell?"] },
    ],
  };
  const open = (id: string) => ({ account_id: id, state: "missing_evidence" });
  const request = (id: string, account: string, link: { used_at?: string; revoked_at?: string; expires_at: string }, snapshot = "7") => ({ id, recipient: "ap@acme.test", scope: { snapshot_id: snapshot, bindings: [{ account_id: account }] }, link });
  const status = {
    coverage: { snapshot_id: "7", stale: false, digest: "d", items: [open("21"), { account_id: "22", state: "excluded" }, open("25"), open("26"), open("27"), open("28"), open("30"), { account_id: "31", state: "resolved" }, open("29")] },
    requests: [
      request("9", "25", { expires_at: "2026-10-05T00:00:00Z" }),
      request("8", "26", { expires_at: "2026-10-05T00:00:00Z", revoked_at: "2026-10-01T00:00:00Z" }),
      request("7", "27", { expires_at: "2026-10-01T00:00:00Z" }),
      request("6", "28", { expires_at: "2026-10-01T00:00:00Z", used_at: "2026-09-30T00:00:00Z" }, "5"),
      request("5", "29", { expires_at: "2026-10-05T00:00:00Z" }, "6"),
    ],
  };
  assert.deepEqual(clientAsk(flux, status, now), { accounts: ["21", "26", "27"], questions: ["Software: CloudSuite?", "Software: Figma?", "Water: City?", "Phone: Verizon?"], asked: { "25": "9", "28": "6" }, withdraw: { "29": "5" } });
  assert.deepEqual(clientAsk({ ...flux, this: { ...flux.this, source: "xero" } }, status, now).accounts, []);
  assert.deepEqual(clientAsk({ ...flux, last: { snapshot_id: "6", stale: true } }, status, now).accounts, []);
  assert.deepEqual(clientAsk({ ...flux, this: { ...flux.this, snapshot_id: "8" } }, status, now).accounts, []);
  assert.deepEqual(clientAsk({ ...flux, this: { ...flux.this, stale: true } }, status, now).accounts, []);
  assert.deepEqual(clientAsk(flux, { ...status, coverage: { ...status.coverage, stale: true } }, now).accounts, []);
});
