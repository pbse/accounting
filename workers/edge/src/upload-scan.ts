// WAF rules and its managed header are required on this configured hostname.
export function uploadScanRequest(request: Request, host = "", key = ""): Request | Response {
  const headers = new Headers(request.headers);
  headers.delete("X-Close-Upload-Scan-Key");
  if (new URL(request.url).pathname === "/v1/client-request/scan") {
    if (request.method !== "POST" || !host || new URL(request.url).hostname !== host ||
        !request.cf || key.length < 32 || headers.get("Malicious-Uploads-Detection") !== "3") {
      return Response.json({ message: "A complete Cloudflare upload scan is required. The file remains quarantined." }, { status: 503 });
    }
    headers.set("X-Close-Upload-Scan-Key", key);
  }
  return new Request(request, { headers });
}
