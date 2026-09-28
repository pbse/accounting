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
// view and the form come back. An empty reason reveals under the content grant numbered grant.
export function useRevealable<T extends { revealed?: boolean }>(path: string | null, scope: string | null = path) {
  const [revealed, setRevealed] = useState<{ scope: string | null; reason: string; grant?: number }>();
  const reason = revealed?.scope === scope ? revealed.reason : undefined;
  const setReason = (r?: string, grant?: number) => setRevealed(r === undefined ? undefined : { scope, reason: r, grant });
  // Reveal's automatic reveals under a grant, kept here so they outlive Reveal while the page reloads: the last
  // scope and grant tried, and the scope the staff member hid.
  const [auto, setAuto] = useState<{ tried?: string; hid?: string | null }>({});
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
    reveal: {
      scope, pending: reason !== undefined && !resource.data?.revealed, shown: !!resource.data?.revealed, reason, grant: reason === "" ? revealed?.grant : undefined, failure, auto, setAuto,
      show: (r: string, grant?: number) => { setFailure(""); setReason(r, grant); }, hide: () => setReason(undefined),
    },
  };
}

// Reveal asks why before showing client content; each reveal is recorded in the audit log. On one
// firm's screen (firm), a content grant the staff member holds for it reveals without asking (an
// empty reason: the server records the grant's), until they hide it.
export function Reveal({ reveal, firm }: { reveal: ReturnType<typeof useRevealable>["reveal"]; firm?: string | null }) {
  const allowed = usePermissions().includes("platform:content");
  const grant = useGrantFor(firm);
  const { auto, setAuto } = reveal;
  // One automatic attempt per scope and grant: a refused one is not repeated, while a new grant, or another filter or
  // firm, tries again. Hiding holds for the scope it was hidden in.
  const attempt = grant ? `${reveal.scope}|${grant.id}` : undefined;
  const hidden = auto.hid !== undefined && auto.hid === reveal.scope;
  useEffect(() => {
    if (grant && attempt !== auto.tried && !hidden && !reveal.shown && !reveal.pending) {
      setAuto({ ...auto, tried: attempt });
      reveal.show("", grant.id);
    }
  }, [grant, attempt, auto, hidden, reveal.shown, reveal.pending]); // reveal.show is new each render; these decide
  // A grant that ended, expired or was replaced takes back what it showed (a replacement then reveals anew). Only with
  // the firm known: a screen whose firm comes from its data has none while the revealed version loads.
  useEffect(() => {
    if (firm && reveal.reason === "" && reveal.grant !== grant?.id) reveal.hide();
  }, [firm, grant, reveal.reason, reveal.grant]); // as above
  if (reveal.shown) {
    return (
      <p className="note">
        {reveal.reason === "" && grant ? `Showing client content under your grant, until ${new Date(grant.expires_at).toLocaleTimeString()}.` : "Showing client content."} This view was recorded in the audit log.{" "}
        <button onClick={() => { setAuto({ ...auto, hid: reveal.scope }); reveal.hide(); }}>Hide it</button>
      </p>
    );
  }
  if (reveal.pending) return <p role="status">Revealing…</p>;
  return (
    <>
      {reveal.failure && <p role="alert">The content was not revealed: {reveal.failure}</p>}
      {grant && hidden && <p className="muted">Hidden while you hold a grant for this firm. <button className="link" onClick={() => setAuto({})}>Show it again</button></p>}
      <details>
        <summary>Reveal client content</summary>
        <ActionForm label="Reveal client content" allowed={allowed} onSubmit={async (reason) => reveal.show(reason)} />
      </details>
      {allowed && firm && !grant && <OpenGrant firm={firm} />}
    </>
  );
}
