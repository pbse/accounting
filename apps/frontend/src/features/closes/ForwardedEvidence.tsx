import { useDates } from "../../data/time";
import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Link = { digest: string; issued_at: string; expires_at: string; revoked_at: string | null };
type File = { id: string; name: string; digest: string; state: string; error: string; from_address: string; subject: string; received_at: string; source_id: string | null; scanned_at: string | null };
type Status = { configured: boolean; links: Link[]; files: File[] };

export function ForwardedEvidence({ closeId, onReleased }: { closeId: string; onReleased: () => void }) {
  const dates = useDates();
  const [status, setStatus] = useState<Status>();
  const [address, setAddress] = useState("");
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function load() {
    try { setStatus(await workspaceRequest(`/${closeId}/forwarding`)); setError(""); }
    catch (e) { setError(e instanceof Error ? e.message : "Forwarded evidence unavailable."); }
  }
  useEffect(() => { setAddress(""); setReasons({}); setStatus(undefined); void load(); }, [closeId]);
  useEffect(() => {
    const timer = window.setInterval(() => void load(), 10000);
    return () => window.clearInterval(timer);
  }, [closeId]);
  async function issue() {
    setBusy(true); setError("");
    try {
      const result = await workspaceRequest(`/${closeId}/forwarding`, { action: "create", confirmed: true });
      setAddress(result.address);
      await load();
    } catch (e) { setError(e instanceof Error ? e.message : "Could not issue an address."); }
    finally { setBusy(false); }
  }
  async function revoke(digest: string) {
    setBusy(true); setError("");
    try { await workspaceRequest(`/${closeId}/forwarding`, { action: "revoke", digest, confirmed: true }); await load(); }
    catch (e) { setError(e instanceof Error ? e.message : "Could not revoke the address."); }
    finally { setBusy(false); }
  }
  async function decide(id: string, action: "release" | "reject" | "rescan") {
    setBusy(true); setError("");
    try {
      await workspaceRequest(`/${closeId}/forwarding/files/${id}`, { action, reason: reasons[id] ?? "", confirmed: true });
      setReasons((previous) => ({ ...previous, [id]: "" }));
      await load();
      if (action === "release") onReleased();
    } catch (e) { setError(e instanceof Error ? e.message : "Could not save the decision."); }
    finally { setBusy(false); }
  }
  async function download(file: File) {
    setError("");
    try {
      const blob = await workspaceRequest(`/${closeId}/forwarding/files/${file.id}`, undefined, true);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url; link.download = file.name; link.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (e) { setError(e instanceof Error ? e.message : "Download unavailable."); }
  }
  return <details>
    <summary>Forward documents by email</summary>
    <p>Issue a close-specific address and share it only with the intended sender. The address expires after seven days. Sender names are unverified; attachments are scanned and require your review before the agent can use them.</p>
    {error && <p role="alert">{error}</p>}
    {!status?.configured ? <p>Cloudflare email intake is not configured. Upload files directly for local work.</p> : <>
      <button disabled={busy} onClick={() => void issue()}>Issue a seven-day evidence address</button>
      {address && <p><strong>Copy and share once:</strong> <code>{address}</code> <button onClick={() => void navigator.clipboard.writeText(address)}>Copy address</button></p>}
      {status.links.filter((link) => !link.revoked_at && new Date(link.expires_at) > new Date()).map((link) =>
        <p key={link.digest}>Issued {dates.dateTime(link.issued_at)} · expires {dates.dateTime(link.expires_at)} <button disabled={busy} onClick={() => void revoke(link.digest)}>Revoke address</button></p>)}
    </>}
    <h3>Forwarded attachments</h3>
    {status?.files.length === 0 && <p>No forwarded attachments.</p>}
    {status?.files.map((file) => <article key={file.id}>
      <p><strong>{file.name}</strong> · {file.state} · received {dates.dateTime(file.received_at)} from unverified {file.from_address}{file.subject && ` · ${file.subject}`}</p>
      {file.error && <p role="alert">{file.error}</p>}
      {file.source_id && <p>Evidence #{file.source_id}. Identical bytes reuse an existing source; a changed document needs explicit comparison or replacement.</p>}
      {(file.state === "clean" || file.state === "released") && file.scanned_at && Date.now() - new Date(file.scanned_at).getTime() < 24 * 60 * 60 * 1000 && <button onClick={() => void download(file)}>Download scanned original</button>}
      {(file.state === "clean" || file.state === "failed") && <label>Decision reason <input value={reasons[file.id] ?? ""} onChange={(e) => setReasons({ ...reasons, [file.id]: e.target.value })} minLength={5} maxLength={2000} /></label>}
      {file.state === "clean" && <><button disabled={busy || (reasons[file.id] ?? "").trim().length < 5 || !file.scanned_at || Date.now() - new Date(file.scanned_at).getTime() >= 24 * 60 * 60 * 1000} onClick={() => void decide(file.id, "release")}>Release to agent evidence</button> <button disabled={busy || (reasons[file.id] ?? "").trim().length < 5} onClick={() => void decide(file.id, "reject")}>Reject file</button></>}
      {(file.state === "failed" || (file.state === "clean" && file.scanned_at && Date.now() - new Date(file.scanned_at).getTime() > 23 * 60 * 60 * 1000)) && <><button disabled={busy || (reasons[file.id] ?? "").trim().length < 5} onClick={() => void decide(file.id, "rescan")}>Request a fresh scan</button> {file.state === "failed" && <button disabled={busy || (reasons[file.id] ?? "").trim().length < 5} onClick={() => void decide(file.id, "reject")}>Reject file</button>}</>}
    </article>)}
  </details>;
}
