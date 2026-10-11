import { workspaceQuery } from "../../data/queries";
import { useQuery } from "@tanstack/react-query";
import { useDates } from "../../data/time";
import { useState } from "react";
import { workspaceRequest } from "./workspace";

type Reply = { id: string; request_id: string; envelope_from: string; header_from: string; body: string; addresses_match: boolean; state: string; received_at: string; decision_note?: string };

export function EmailReplies({ closeId, onReleased, open }: { closeId: string; onReleased: () => void; open?: boolean }) {
  const dates = useDates();
  const read = useQuery(workspaceQuery<Reply[]>(`/${closeId}/email-replies`));
  const items = read.data ?? [];
  const loaded = read.data !== undefined;
  const [actionError, setError] = useState("");
  const error = actionError || (read.error ? read.error.message || "Email replies unavailable." : "");
  const [acting, setBusy] = useState(false);
  const busy = acting || read.isFetching;
  const [releasing, setReleasing] = useState<Record<string, boolean>>({});
  const load = async () => { setError(""); await read.refetch(); };
  async function decide(id: string, action: "release" | "reject", reason: string, verificationReference: string, newSenderVerified: boolean) {
    setBusy(true); setError("");
    try {
      await workspaceRequest(`/${closeId}/email-replies/${id}`, { action, reason, verification_reference: verificationReference, new_sender_verified: newSenderVerified, confirmed: true });
      await load();
      if (action === "release") onReleased();
    } catch (e) { setError(e instanceof Error ? e.message : "Reply decision failed."); }
    finally { setBusy(false); }
  }
  return <details open={open}><summary>Review email replies{loaded ? ` · ${items.filter(item => item.state === "held").length} held` : ""}</summary>
    <p>Email replies are held until a firm reviewer confirms the original request and independently verifies the sender through a known contact channel. Matching addresses and mail headers alone do not prove identity. A reply from someone other than the approved recipient can be released only after you verify that new sender with the client through a known contact channel; never trust the name they give.</p>
    <button disabled={busy} onClick={() => void load()}>Load replies</button>
    {error && <p role="alert">{error}</p>}
    {loaded && items.length === 0 && <p>No email replies received for this close.</p>}
    {items.map(item => <article key={item.id}>
      <h3>Request #{item.request_id} · {item.state}</h3>
      <p>Received {dates.dateTime(item.received_at)} from {item.envelope_from}. Message From: {item.header_from}. {item.addresses_match ? "Addresses match the approved recipient; identity still needs review." : "A new sender, not the approved recipient. Verify them with the client through a known contact channel before releasing, or keep held and ask the client to use the secure link."}</p>
      <pre>{item.body}</pre>
      {item.state === "held" && <form onSubmit={e => { e.preventDefault(); const data = new FormData(e.currentTarget); void decide(item.id, String(data.get("action")) as "release" | "reject", String(data.get("reason")), String(data.get("verification_reference") ?? ""), data.get("new_sender_verified") === "on"); }}>
        <label>Decision reason<input name="reason" required minLength={5} maxLength={2000} /></label>
        <label><input type="radio" name="action" value="release" required onChange={() => setReleasing(current => ({...current, [item.id]: true}))} /> Release into the original preparation job (its work resumes, treating the request as answered)</label>
        <label><input type="radio" name="action" value="reject" required onChange={() => setReleasing(current => ({...current, [item.id]: false}))} /> Reject</label>
        {releasing[item.id] && !item.addresses_match && <label><input type="checkbox" name="new_sender_verified" required /> I verified this new sender with the client through a known contact channel</label>}
        {releasing[item.id] && <label>Independent sender verification reference<input name="verification_reference" required minLength={10} maxLength={500} placeholder="Known client contact and confirmation date" /></label>}
        <button disabled={busy}>Save decision</button>
      </form>}
    </article>)}
  </details>;
}
