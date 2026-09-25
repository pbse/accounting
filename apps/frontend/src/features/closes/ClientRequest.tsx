import { useEffect, useState, type FormEvent } from "react";
import { apiBaseUrl } from "../../lib/api";

type Request = {
  status: string;
  entity?: string;
  period?: string;
  body?: string;
  expires_at?: string;
  removed?: string[];
};

export default function ClientRequest() {
  const [token] = useState(() => window.location.hash.slice(1));
  const [request, setRequest] = useState<Request>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [scanStatus, setScanStatus] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [passcode, setPasscode] = useState("");
  const [needsPasscode, setNeedsPasscode] = useState(false);
  async function call(body?: object | FormData): Promise<Request> {
    const response = await fetch(`${apiBaseUrl}/v1/client-request`, {
      method: body ? "POST" : "GET",
      credentials: "omit",
      cache: "no-store",
      referrerPolicy: "no-referrer",
      headers: {
        Authorization: `Bearer ${token}`,
        ...(passcode ? { "X-Request-Passcode": passcode } : {}),
        ...(body && !(body instanceof FormData) ? { "Content-Type": "application/json" } : {}),
      },
      ...(body ? { body: body instanceof FormData ? body : JSON.stringify(body) } : {}),
    });
    const result = await response.json().catch(() => ({ message: "The request was blocked or is unavailable. Contact the firm; retry unchanged if receipt is uncertain." }));
    setNeedsPasscode(result.code === "PASSCODE_REQUIRED" || response.status === 403);
    if (!response.ok)
      throw new Error(
        result.message || "Request unavailable. Contact the firm.",
      );
    return result;
  }
  useEffect(() => {
    window.history.replaceState(null, "", "/request");
    if (!/^[A-Z2-7]{26,128}$/.test(token)) {
      setError(
        "Open the complete private link from your accounting firm. It may have expired or been replaced.",
      );
      return;
    }
    let active = true;
    void call()
      .then((value) => {
        if (active) {
          setRequest(value);
          setError("");
        }
      })
      .catch((e) => {
        if (active)
          setError(
            e instanceof Error ? e.message : "Could not open this request.",
          );
      });
    return () => {
      active = false;
    };
  }, [token, refresh]);
  async function scanFiles(files: File[]) {
    for (const file of files) {
      const textFile = /\.(txt|csv)$/i.test(file.name);
      setScanStatus(`Checking ${file.name} with ${textFile ? "the on-demand text scanner (startup may take a few minutes)" : "Cloudflare"}…`);
      const response = await fetch(`${apiBaseUrl}/v1/client-request/${textFile ? "scan-text" : "scan"}`, {
        method: "POST", credentials: "omit", cache: "no-store", referrerPolicy: "no-referrer",
        headers: { Authorization: `Bearer ${token}`, ...(passcode ? { "X-Request-Passcode": passcode } : {}), "Content-Type": textFile ? "application/octet-stream" : "application/pdf" },
        body: file,
      });
      if (!response.ok) throw new Error("Your reply is saved, but an attachment could not pass malware scanning. It remains quarantined. Retry with the same original files or contact the firm; do not change the file extension.");
    }
    setScanStatus(files.length ? "malware scanning completed. The firm must review and release your documents." : "");
  }
  async function retryScans(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const files = new FormData(event.currentTarget).getAll("files").filter((file): file is File => file instanceof File && file.size > 0);
    setBusy(true); setError("");
    try { await scanFiles(files); }
    catch (e) { setScanStatus(""); setError(e instanceof Error ? e.message : "Scan unavailable; documents remain quarantined."); }
    finally { setBusy(false); }
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    setBusy(true);
    setError("");
    try {
      const submission = new FormData();
      submission.set("body", String(data.get("reply") ?? ""));
      submission.set("confirmed", String(data.has("confirmed")));
      const files = data.getAll("files").filter((file): file is File => file instanceof File && file.size > 0);
      for (const file of files) submission.append("files", file);
      setRequest(await call(submission));
      await scanFiles(files);
    } catch (e) {
      setScanStatus("");
      setError(
        e instanceof Error
          ? e.message
          : "Could not confirm receipt. Retry the same reply.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <main id="main" className="public-page">
      <p className="eyebrow">PRIVATE CLIENT REQUEST</p>
      <h1>
        {request?.status === "received"
          ? "Your reply has been received."
          : "Help complete your close"}
      </h1>
      {error && <p role="alert">{error}</p>}
      {scanStatus && <p role="status">{scanStatus}</p>}
      {!request && !error && <p role="status">Opening your request…</p>}
      {!request && needsPasscode && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setPasscode(String(new FormData(e.currentTarget).get("passcode") ?? "").replace(/\s/g, ""));
            setRefresh((n) => n + 1);
          }}
        >
          <label>
            Passcode from your accounting team
            <input name="passcode" inputMode="numeric" autoComplete="one-time-code" maxLength={9} required />
          </label>
          <button>Open request</button>
          <p className="note">Your accounting team gives you this passcode by phone or in person; it is never in the email. Five wrong tries close the link.</p>
        </form>
      )}
      {!request && error && !needsPasscode && (
        <button onClick={() => setRefresh((n) => n + 1)}>
          Retry opening request
        </button>
      )}
      {request?.status === "received" && (
        <>
        {(request.removed?.length ?? 0) > 0 && (
          <p role="status">
            For your security we removed the {request.removed!.join(", ")} from your reply before saving it. Please never send card or bank
            account numbers, identity numbers or passwords; the firm will ask another way if it needs them.
          </p>
        )}
        <p>
          Your reply is saved. Attachments remain quarantined until automated scanning and firm release. Preparation can
          resume; this does not approve accounting decisions or posting. Contact
          the firm directly if you need to correct your reply.
        </p>
        <details><summary>Retry attachment scanning</summary><p>Select the same original files if scanning was interrupted or the firm needs a fresh scan. This cannot add new documents or change your reply.</p>
          <form onSubmit={retryScans}><fieldset disabled={busy}>
            <label>Original documents<input type="file" name="files" accept=".pdf,.txt,.csv" multiple required /></label>
            <button>Retry malware scanning</button>
          </fieldset></form>
        </details>
        </>
      )}
      {request?.status === "open" && (
        <>
          <h2>
            {request.entity} · {request.period}
          </h2>
          <p>
            Please respond only for this client and period. Do not include
            passwords or information about other clients.
          </p>
          <pre style={{ whiteSpace: "pre-wrap" }}>{request.body}</pre>
          <p>
            This link expires {new Date(request.expires_at!).toLocaleString()}.
            Keep it private.
          </p>
          <form onSubmit={submit}>
            <fieldset disabled={busy}>
              <label>
                Your reply
                <textarea name="reply" rows={8} maxLength={20000} required />
              </label>
              <label>
                <input type="checkbox" name="confirmed" required /> This reply
                concerns {request.entity} for {request.period}. I understand it
                will be reviewed by the firm.
              </label>
              <label>Supporting documents<input name="files" type="file" accept=".pdf,.txt,.csv" multiple /></label>
              <p>Up to five distinct files: PDF up to 5 MiB, TXT/CSV up to 512 KiB; 20 MiB total. PDFs use Cloudflare scanning; TXT/CSV use the separate on-demand antivirus service. Files stay quarantined until automated malware scanning succeeds and the firm releases them. One submission per link; retry with the same reply and files if receipt is uncertain.</p>
              <button>
                {busy ? "Saving your reply…" : "Send reply to the firm"}
              </button>
            </fieldset>
          </form>
        </>
      )}
    </main>
  );
}
