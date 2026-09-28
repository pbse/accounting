import { useEffect, useState } from "react";
import { useResource } from "../api";
import { usePermissions } from "../App";
import { ActionForm } from "./ActionForm";

// What the server sends in place of client text until it is revealed.
export const masked = "••••";
export const shown = (value: string) => (value === masked ? <span className="muted">hidden</span> : value);

// useRevealable loads path, masked until someone reveals it with a reason. A reveal the server
// refuses is dropped, with its error, so the masked view and the form come back.
export function useRevealable<T extends { revealed?: boolean }>(path: string) {
  const [reason, setReason] = useState<string>();
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
    reveal: { pending: reason !== undefined && !resource.data?.revealed, shown: !!resource.data?.revealed, failure, show: (r: string) => { setFailure(""); setReason(r); }, hide: () => setReason(undefined) },
  };
}

// Reveal asks why before showing client content; each reveal is recorded in the audit log.
export function Reveal({ reveal }: { reveal: ReturnType<typeof useRevealable>["reveal"] }) {
  const allowed = usePermissions().includes("platform:content");
  if (reveal.shown) return <p className="note">Showing client content. This view was recorded in the audit log. <button onClick={reveal.hide}>Hide it</button></p>;
  if (reveal.pending) return <p role="status">Revealing…</p>;
  return (
    <>
      {reveal.failure && <p role="alert">The content was not revealed: {reveal.failure}</p>}
      <details>
        <summary>Reveal client content</summary>
        <ActionForm label="Reveal client content" allowed={allowed} onSubmit={async (reason) => reveal.show(reason)} />
      </details>
    </>
  );
}
