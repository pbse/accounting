import { useEffect, useState, type FormEvent } from "react";
import { apiBaseUrl } from "../../lib/api";
import { PageHeader } from "@close/ui/components/PageHeader";
import { useShellThemeColor } from "@close/ui/theme/useShellThemeColor";

type Question = { position: number; text: string; state: string; answer: string };
type Request = {
  status: string;
  entity?: string;
  period?: string;
  body?: string;
  expires_at?: string;
  removed?: string[];
  questions?: Question[];
  open?: boolean;
  rescan?: boolean;
  // A request for the month's books takes its books files: a CSV or Excel workbook, scanned whole.
  books?: boolean;
  // Where each books file sent stands.
  files?: { name: string; state: keyof typeof fileStates }[];
  // The ledger a request to connect the client's books asks it to sign in to, and whether this link connected it.
  connect?: "quickbooks" | "xero";
  connected?: boolean;
};
const ledgerNames = { quickbooks: "QuickBooks", xero: "Xero" };
const fileStates = {
  received: "Received, not yet cleared by the malware scan",
  scanned: "Scanned, waiting for the firm to review it",
  released: "With the firm, being read",
  accepted: "Accepted as your books for the month",
  refused: "Not accepted: the firm will ask for another file",
  removed: "Removed by the firm",
};
const stateLabels: Record<string, string> = { answered: "Answered", awaiting_document: "Waiting for your document", "": "Not answered yet" };
// Green only for what is done; a document still to come asks for attention, an open question stays neutral.
const statePills: Record<string, string> = { answered: "pill", awaiting_document: "pill muted", "": "pill neutral" };

export default function ClientRequest() {
  useShellThemeColor();
  const [token] = useState(() => window.location.hash.slice(1));
  // The ledger's sign-in, started from a request to connect, returns here saying how it went.
  const [connected] = useState(() => new URLSearchParams(window.location.search).get("connected") ?? "");
  const [request, setRequest] = useState<Request>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [scanStatus, setScanStatus] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [passcode, setPasscode] = useState("");
  const [needsPasscode, setNeedsPasscode] = useState(false);
  const [drafts, setDrafts] = useState<Record<number, { answer: string; later: boolean; attached?: boolean }>>({});
  // The request read again after a books file's scan (its files' states and whether a scan still needs retrying), kept
  // apart from the request so unsent answers stay as typed.
  const [fresh, setFresh] = useState<Request>();
  useEffect(() => {
    // A document answer's recorded text names the files; it is not the client's note, so it is not offered back as one.
    setDrafts(Object.fromEntries((request?.questions ?? []).map((q) => [q.position, { answer: q.answer.startsWith("Document attached with this reply:") ? "" : q.answer, later: q.state === "awaiting_document" }])));
    setFresh(undefined);
  }, [request]);
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
    if (connected) return;
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
      const textFile = request?.books || /\.(txt|csv)$/i.test(file.name);
      setScanStatus(`Checking ${file.name} with ${textFile ? "the on-demand antivirus scanner (startup may take a few minutes)" : "Cloudflare"}…`);
      const response = await fetch(`${apiBaseUrl}/v1/client-request/${request?.books ? "scan-file" : textFile ? "scan-text" : "scan"}`, {
        method: "POST", credentials: "omit", cache: "no-store", referrerPolicy: "no-referrer",
        headers: { Authorization: `Bearer ${token}`, ...(passcode ? { "X-Request-Passcode": passcode } : {}), "Content-Type": textFile ? "application/octet-stream" : "application/pdf" },
        body: file,
      });
      if (!response.ok) throw new Error("Your reply is saved, but an attachment could not pass malware scanning. It remains quarantined. Retry with the same original files or contact the firm; do not change the file extension.");
    }
    setScanStatus(files.length ? "malware scanning completed. The firm must review and release your documents." : "");
    // A books request lists each file's state, which the scan just moved on: read it again.
    if (files.length && request?.books) {
      try {
        setFresh(await call());
      } catch {
        // The scan stands; the list catches up on the next read.
      }
    }
  }
  // A request to connect the client's books starts its ledger's sign-in, from which the ledger returns to this page.
  async function connect() {
    setBusy(true);
    setError("");
    try {
      const response = await fetch(`${apiBaseUrl}/v1/client-request/connect`, {
        method: "POST", credentials: "omit", cache: "no-store", referrerPolicy: "no-referrer",
        headers: { Authorization: `Bearer ${token}`, ...(passcode ? { "X-Request-Passcode": passcode } : {}) },
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.url) throw new Error(result.message || "The sign-in could not be started. Contact the firm.");
      window.location.assign(result.url);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The sign-in could not be started. Contact the firm.");
      setBusy(false);
    }
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
      // Each question sent is either answered or a document to follow; untouched ones stay as they were.
      const answers = (request?.questions ?? [])
        .map((q) => ({ q, d: drafts[q.position] ?? { answer: "", later: false } }))
        .filter(({ q, d }) => d.attached || ((d.later || d.answer.trim()) && ((d.later ? "awaiting_document" : "answered") !== q.state || d.answer.trim() !== q.answer)))
        .map(({ q, d }) => ({ position: q.position, state: d.attached ? "document" : d.later ? "awaiting_document" : "answered", answer: d.answer.trim() }));
      submission.set("body", JSON.stringify({ answers }));
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
    <div className="app-shell standalone">
      <main id="main" className="page request-page">
        <PageHeader title={request?.status === "received" ? "Your reply has been received." : "Help complete your close"}>
          Private client request
        </PageHeader>
        <section>
          {error && <p role="alert">{error}</p>}
          {scanStatus && <p role="status">{scanStatus}</p>}
          {connected && (
            <p role="status">
              {connected === "failed"
                ? "The sign-in did not finish. Open your private link again to retry, or contact your accounting firm."
                : `${ledgerNames[connected as keyof typeof ledgerNames] ?? "Your ledger"} is connected. Your accounting firm confirms it is yours before reading any of your books. You can close this page.`}
            </p>
          )}
          {!connected && !request && !error && <p role="status">Opening your request…</p>}
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
          {(request?.status === "open" || request?.status === "received") && (
            <>
              <h2>
                {request.entity} · {request.period}
              </h2>
              {(request.removed?.length ?? 0) > 0 && (
                <p role="status">
                  For your security we removed the {request.removed!.join(", ")} from your reply before saving it. Please never send card or bank
                  account numbers, identity numbers or passwords; the firm will ask another way if it needs them.
                </p>
              )}
              {request.status === "received" && (
                <p role="status">
                  {request.open
                    ? "Your reply is saved. You can answer more questions, send documents or correct an answer until this link expires. Work continues once every question is answered, or when the firm decides to go ahead."
                    : "Your reply is saved, and this request takes no more answers. Contact the firm if something needs to change."}{" "}
                  Attachments stay quarantined until automated scanning and the firm's release.
                </p>
              )}
              {request.books && ((fresh ?? request).files?.length ?? 0) > 0 && (
                <section aria-label="Your files">
                  <h3>Your files</h3>
                  <ul>
                    {(fresh ?? request).files!.map((f, i) => (
                      <li key={i}>
                        {f.name}: {fileStates[f.state]}
                      </li>
                    ))}
                  </ul>
                </section>
              )}
              {request.connect && !request.connected && request.open !== false && (
                <p>
                  <button type="button" className="primary" disabled={busy} onClick={() => void connect()}>
                    Connect {ledgerNames[request.connect]}
                  </button>{" "}
                  You sign in to {ledgerNames[request.connect]} yourself; your password is never shared with the firm.
                </p>
              )}
              <details>
                <summary>The request as sent</summary>
                <pre>{request.body}</pre>
              </details>
              {request.open !== false && (
                <p>
                  Please respond only for this client and period. Do not include passwords or information about other clients. This link expires{" "}
                  {new Date(request.expires_at!).toLocaleString()}. Keep it private.
                </p>
              )}
              <form onSubmit={submit}>
                <fieldset disabled={busy || request.open === false}>
                  <ol className="stack">
                    {(request.questions ?? []).map((q) => {
                      const d = drafts[q.position] ?? { answer: "", later: false };
                      return (
                        <li key={q.position}>
                          <p>
                            {q.text} <span className={statePills[q.state] ?? "pill neutral"}>{stateLabels[q.state] ?? q.state}</span>
                          </p>
                          <label>
                            {d.later || d.attached ? "Note about the document (optional)" : "Your answer"}
                            <textarea rows={3} value={d.answer} onChange={(e) => setDrafts({ ...drafts, [q.position]: { ...d, answer: e.target.value } })} />
                          </label>
                          <label>
                            <input type="checkbox" checked={!!d.attached} onChange={(e) => setDrafts({ ...drafts, [q.position]: { ...d, attached: e.target.checked, later: e.target.checked ? false : d.later } })} /> I'm attaching the document
                            with this reply
                          </label>
                          <label>
                            <input type="checkbox" checked={d.later} onChange={(e) => setDrafts({ ...drafts, [q.position]: { ...d, later: e.target.checked, attached: e.target.checked ? false : d.attached } })} /> I'll send the
                            document later
                          </label>
                        </li>
                      );
                    })}
                  </ol>
                  <label>Supporting documents<input name="files" type="file" accept={request?.books ? ".csv,.xlsx" : ".pdf,.txt,.csv"} multiple /></label>
                  {(request.questions ?? []).some((q) => q.state === "awaiting_document") && (
                    <p className="note">Sending a document you said would follow? Tick “I'm attaching the document with this reply” on its question and attach the file.</p>
                  )}
                  <p>{request?.books ? "Your books as a CSV or Excel workbook (.xlsx), up to 50 MiB each." : "Up to five distinct files: PDF up to 5 MiB, TXT/CSV up to 512 KiB; 20 MiB total."} Files stay quarantined until automated scanning and the firm's release.</p>
                  <label>
                    <input type="checkbox" name="confirmed" required /> This reply concerns {request.entity} for {request.period}. I understand it will be reviewed by the firm.
                  </label>
                  <button>{busy ? "Saving your reply…" : request.status === "received" ? "Send the update to the firm" : "Send to the firm"}</button>
                </fieldset>
              </form>
              {request.status === "received" && (fresh ?? request).rescan && (
                <details>
                  <summary>Retry attachment scanning</summary>
                  <p>Select the same original files if scanning was interrupted or the firm needs a fresh scan. This cannot add new documents or change your reply.</p>
                  <form onSubmit={retryScans}>
                    <fieldset disabled={busy}>
                      <label>Original documents<input type="file" name="files" accept={request?.books ? ".csv,.xlsx" : ".pdf,.txt,.csv"} multiple required /></label>
                      <button>Retry malware scanning</button>
                    </fieldset>
                  </form>
                </details>
              )}
            </>
          )}
        </section>
        <p className="request-legal">
          {/* A new tab keeps this request open: its link is no longer in the address bar. */}
          How Krith handles what you send:{" "}
          <a href="/privacy" target="_blank" rel="noreferrer">
            privacy notice
          </a>{" "}
          ·{" "}
          <a href="/security" target="_blank" rel="noreferrer">
            security
          </a>
        </p>
      </main>
    </div>
  );
}
