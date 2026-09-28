import { useEffect, useState } from "react";
import { useResource } from "../api";
import { usePermissions } from "../App";
import { ActionForm } from "./ActionForm";
import { OpenGrant, useGrantFor } from "./Grants";

// What the server sends in place of client text until it is revealed.
export const masked = "••••";
export const shown = (value: string) => (value === masked ? <span className="muted">hidden</span> : value);

// useRevealable loads path, masked until someone reveals it with a reason. A reveal belongs to its
// scope (by default the path; a list passes its filters, so paging keeps it): a new scope is masked
// again until revealed anew. A reveal the server refuses is dropped, with its error, so the masked
// view and the form come back.
export function useRevealable<T extends { revealed?: boolean }>(path: string | null, scope: string | null = path) {
  const [revealed, setRevealed] = useState<{ scope: string | null; reason: string }>();
  const reason = revealed?.scope === scope ? revealed.reason : undefined;
  const setReason = (r?: string) => setRevealed(r === undefined ? undefined : { scope, reason: r });
  // Leaving the scope ends the reveal, so coming back to it asks again.
  useEffect(() => {
    if (revealed && revealed.scope !== scope) setRevealed(undefined);
  }, [scope, revealed]);
  const [failure, setFailure] = useState("");
  const resource = useResource<T>(path, reason);
  useEffect(() => {
    if (reason !== undefined && resource.error) {
      setFailure(resource.error);
      setReason(undefined);
    }
  }, [reason, resource.error]);
  return {
    ...resource,
    reveal: { pending: reason !== undefined && !resource.data?.revealed, shown: !!resource.data?.revealed, reason, failure, show: (r: string) => { setFailure(""); setReason(r); }, hide: () => setReason(undefined) },
  };
}

// Reveal asks why before showing client content; each reveal is recorded in the audit log. On one
// firm's screen (firm), a content grant the staff member holds for it reveals without asking (an
// empty reason: the server records the grant's), until they hide it.
export function Reveal({ reveal, firm }: { reveal: ReturnType<typeof useRevealable>["reveal"]; firm?: string | null }) {
  const allowed = usePermissions().includes("platform:content");
  const grant = useGrantFor(firm);
  const [hidden, setHidden] = useState(false);
  // The grant this screen already revealed under, or tried to: one attempt per grant, so a refused one is not repeated
  // and a new grant tries again.
  const [triedWith, setTriedWith] = useState<number>();
  useEffect(() => {
    if (grant && grant.id !== triedWith && !hidden && !reveal.shown && !reveal.pending) {
      setTriedWith(grant.id);
      reveal.show("");
    }
  }, [grant, triedWith, hidden, reveal.shown, reveal.pending]); // reveal.show is new each render; these decide
  // A grant that ended or expired takes back what it showed. Only with the firm known: a screen whose
  // firm comes from its data has none while the revealed version loads.
  useEffect(() => {
    if (firm && !grant && reveal.reason === "") reveal.hide();
  }, [firm, grant, reveal.reason]); // as above
  if (reveal.shown) {
    return (
      <p className="note">
        {reveal.reason === "" && grant ? `Showing client content under your grant, until ${new Date(grant.expires_at).toLocaleTimeString()}.` : "Showing client content."} This view was recorded in the audit log.{" "}
        <button onClick={() => { setHidden(true); reveal.hide(); }}>Hide it</button>
      </p>
    );
  }
  if (reveal.pending) return <p role="status">Revealing…</p>;
  return (
    <>
      {reveal.failure && <p role="alert">The content was not revealed: {reveal.failure}</p>}
      {grant && hidden && <p className="muted">Hidden while you hold a grant for this firm. <button className="link" onClick={() => { setHidden(false); setTriedWith(undefined); }}>Show it again</button></p>}
      <details>
        <summary>Reveal client content</summary>
        <ActionForm label="Reveal client content" allowed={allowed} onSubmit={async (reason) => reveal.show(reason)} />
      </details>
      {allowed && firm && !grant && <OpenGrant firm={firm} />}
    </>
  );
}
