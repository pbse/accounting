import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Reply = { id: string; request_id: string; envelope_from: string; header_from: string; body: string; addresses_match: boolean; state: string; received_at: string; decision_note?: string };

export function EmailReplies({ closeId, onReleased }: { closeId: string; onReleased: () => void }) {
  const [items, setItems] = useState<Reply[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [releasing, setReleasing] = useState<Record<string, boolean>>({});
  async function load() {
    setBusy(true); setError("");
    try { setItems(await workspaceRequest(`/${closeId}/email-replies`)); setLoaded(true); }
    catch (e) { setError(e instanceof Error ? e.message : "Email replies unavailable."); }
    finally { setBusy(false); }
  }
  useEffect(() => { void load(); }, [closeId]);
  async function decide(id: string, action: "release" | "reject", reason: string, verificationReference: string) {
    setBusy(true); setError("");
    try {
      await workspaceRequest(`/${closeId}/email-replies/${id}`, { action, reason, verification_reference: verificationReference, confirmed: true });
      await load();
      if (action === "release") onReleased();
    } catch (e) { setError(e instanceof Error ? e.message : "Reply decision failed."); }
    finally { setBusy(false); }
  }
  return <details><summary>Review email replies{loaded ? ` · ${items.filter(item => item.state === "held").length} held` : ""}</summary>
    <p>Email replies are held until a firm reviewer confirms the original request and independently verifies the sender through a known contact channel. Matching addresses and mail headers alone do not prove identity. Replies from other senders cannot enter preparation; ask the client to use the secure link.</p>
    <button disabled={busy} onClick={() => void load()}>Load replies</button>
    {error && <p role="alert">{error}</p>}
    {loaded && items.length === 0 && <p>No email replies received for this close.</p>}
    {items.map(item => <article key={item.id}>
      <h3>Request #{item.request_id} · {item.state}</h3>
      <p>Received {new Date(item.received_at).toLocaleString()} from {item.envelope_from}. Message From: {item.header_from}. {item.addresses_match ? "Addresses match the approved recipient; identity still needs review." : "Sender differs from the approved recipient. Keep held and use the secure link."}</p>
      <pre>{item.body}</pre>
      {item.state === "held" && <form onSubmit={e => { e.preventDefault(); const data = new FormData(e.currentTarget); void decide(item.id, String(data.get("action")) as "release" | "reject", String(data.get("reason")), String(data.get("verification_reference") ?? "")); }}>
        <label>Decision reason<input name="reason" required minLength={5} maxLength={2000} /></label>
        {item.addresses_match && <label><input type="radio" name="action" value="release" required onChange={() => setReleasing(current => ({...current, [item.id]: true}))} /> Release into the original preparation job</label>}
        <label><input type="radio" name="action" value="reject" required onChange={() => setReleasing(current => ({...current, [item.id]: false}))} /> Reject</label>
        {releasing[item.id] && <label>Independent sender verification reference<input name="verification_reference" required minLength={10} maxLength={500} placeholder="Known client contact and confirmation date" /></label>}
        <button disabled={busy}>Save decision</button>
      </form>}
    </article>)}
  </details>;
}
