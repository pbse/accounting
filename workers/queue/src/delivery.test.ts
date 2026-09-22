import assert from "node:assert/strict";
import { test } from "node:test";
import { deliver } from "./delivery.ts";

test("acknowledgement requires the explicit durable handoff response", async () => {
  await deliver({ job_id: "synthetic-job" }, async (request) => {
    assert.deepEqual(await request.json(), { job_id: "synthetic-job" });
    assert.equal(new URL(request.url).pathname, "/internal/jobs");
    return new Response(null, { status: 204 });
  });
  await assert.rejects(
    deliver({}, async () => new Response(null, { status: 503 })),
  );
  await assert.rejects(
    deliver({}, async () => new Response("{}", { status: 200 })),
  );
  await assert.rejects(
    deliver({}, async () => {
      throw new Error("response lost");
    }),
  );
});
