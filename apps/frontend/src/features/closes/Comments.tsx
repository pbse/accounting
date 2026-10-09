import { useDates } from "../../data/time";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "./workspace";

type Comment = { id: string; workpaper_id: string; anchor: string; anchor_label: string; body: string; author: string; created_at: string; resolved_by?: string; resolved_at?: string; resolution?: string };
export type PickedLine = { sourceId: string; name: string; line: number };

// Questions on a draft and its earlier versions; any left open keeps the draft from approval.
export function Comments({ closeId, paperId, fields, picked, canComment }: { closeId: string; paperId: string; fields: string[]; picked?: PickedLine; canComment: boolean }) {
  const dates = useDates();
  const path = `/${closeId}/workpapers/${paperId}/comments`;
  const [comments, setComments] = useState<Comment[]>([]);
  const [anchor, setAnchor] = useState("general");
  const [body, setBody] = useState("");
  const [resolutions, setResolutions] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    workspaceRequest(path).then((d) => setComments(d.comments)).catch((e) => setError(e instanceof Error ? e.message : "Comments are unavailable."));
  }, [path]);
  useEffect(() => {
    if (picked) setAnchor(`source:${picked.sourceId}:line:${picked.line}`);
  }, [picked]);
  async function send(payload: Record<string, string>) {
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(path, payload);
      setComments(result.comments);
      if (payload.action === "add") setBody("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "The comment could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  const open = comments.filter((c) => !c.resolved_at).length;
  return (
    <section aria-label="Comments">
      <h2>Comments{open > 0 && ` · ${open} open`}</h2>
      {open > 0 && <p role="status">Open comments keep this draft from approval until they are resolved.</p>}
      <ul>
        {comments.map((c) => (
          <li key={c.id}>
            <strong>{c.anchor_label}</strong>
            {c.workpaper_id !== paperId && (
              <> · on <Link to={`/dashboard/closes/${closeId}/workpapers/${c.workpaper_id}`}>draft #{c.workpaper_id}</Link></>
            )}{" "}
            · {c.author} · {dates.dateTime(c.created_at)}
            <p>{c.body}</p>
            {c.resolved_at ? (
              <p>Resolved by {c.resolved_by} on {dates.dateTime(c.resolved_at)}: {c.resolution}</p>
            ) : (
              <form onSubmit={(e) => { e.preventDefault(); void send({ action: "resolve", id: c.id, resolution: resolutions[c.id] ?? "" }); }}>
                <label>How it was resolved<input required value={resolutions[c.id] ?? ""} onChange={(e) => setResolutions({ ...resolutions, [c.id]: e.target.value })} /></label>
                <button disabled={busy}>Resolve</button>
              </form>
            )}
          </li>
        ))}
      </ul>
      {canComment ? (
        <form onSubmit={(e) => { e.preventDefault(); void send({ action: "add", anchor, body }); }}>
          <label>
            About
            <select value={anchor} onChange={(e) => setAnchor(e.target.value)}>
              <option value="general">The whole draft</option>
              {fields.map((f) => (
                <option key={f} value={`field:${f}`}>Input: {f.replaceAll("_", " ")}</option>
              ))}
              {picked && <option value={`source:${picked.sourceId}:line:${picked.line}`}>{picked.name}, line {picked.line}</option>}
            </select>
          </label>
          <small>Choose a line by clicking its number in the evidence.</small>
          <label>Comment or question<textarea required value={body} onChange={(e) => setBody(e.target.value)} /></label>
          <button disabled={busy || !body.trim()}>Add comment</button>
        </form>
      ) : (
        <p>Comments go on the current, unapproved draft. To question an approved draft, request changes first.</p>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
