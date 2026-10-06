import assert from "node:assert/strict";
import { test } from "node:test";
import { RequestError, forbidden } from "./requestError.ts";

test("only a refusal for want of permission makes a section inapplicable", () => {
  assert.equal(forbidden(new RequestError("The client roster requires clients:all.", 403)), true);
  assert.equal(forbidden(new RequestError("The client roster is unavailable.", 503)), false);
  assert.equal(forbidden(new Error("Failed to fetch")), false);
  assert.equal(new RequestError("Refused", 403).message, "Refused");
});
