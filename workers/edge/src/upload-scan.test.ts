import assert from "node:assert/strict";
import { test } from "node:test";
import { uploadScanRequest } from "./upload-scan.ts";

test("scan attestations require configured Cloudflare ingress; caller keys are stripped", () => {
  const key = "synthetic-test-key-0000000000000000";
  const request = (verdict: string, cf = true, host = "uploads.example.test") => {
    const r = new Request(`https://${host}/v1/client-request/scan`, {
      method: "POST", headers: { "Malicious-Uploads-Detection": verdict, "X-Close-Upload-Scan-Key": "forged" },
    });
    if (cf) Object.defineProperty(r, "cf", { value: { colo: "TEST" } });
    return r;
  };
  for (const verdict of ["", "1", "2", "3, 1", "unknown"]) {
    assert.equal((uploadScanRequest(request(verdict), "uploads.example.test", key) as Response).status, 503);
  }
  assert.ok(uploadScanRequest(request("3", false), "uploads.example.test", key) instanceof Response);
  assert.ok(uploadScanRequest(request("3", true, "bypass.workers.dev"), "uploads.example.test", key) instanceof Response);
  assert.ok(uploadScanRequest(request("3"), "uploads.example.test", "") instanceof Response);
  const accepted = uploadScanRequest(request("3"), "uploads.example.test", key) as Request;
  assert.equal(accepted.headers.get("X-Close-Upload-Scan-Key"), key);
  const normal = uploadScanRequest(new Request("https://uploads.example.test/v1/client-request", {
    headers: { "X-Close-Upload-Scan-Key": key },
  }), "uploads.example.test", key) as Request;
  assert.equal(normal.headers.get("X-Close-Upload-Scan-Key"), null);
});
