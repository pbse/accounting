import { useEffect, useRef, useState } from "react";
import { workspaceRequest } from "./workspace";

type Draft = {
  id: string;
  body: string;
  workpaper_ids: string[];
  context_digest: string;
  created_by: string;
  created_at: string;
  stale: boolean;
};
export function Requests({
  closeId,
  papers,
}: {
  closeId: string;
  papers: { id: string; kind: string; review_state: string }[];
}) {
  const [history, setHistory] = useState<Draft[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const editor = useRef<HTMLTextAreaElement>(null);
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [message, setMessage] = useState("");
  const latest = history[0];
  async function load() {
    const drafts: Draft[] = await workspaceRequest(`/${closeId}/requests`);
    setHistory(drafts);
    if (editor.current) editor.current.value = drafts[0]?.body ?? "";
    setSelected(drafts[0]?.workpaper_ids ?? []);
    setLoaded(true);
  }
  useEffect(() => {
    void load().catch((e) => setMessage(e.message));
  }, [closeId]);
  async function save(generate: boolean, body?: string) {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest(`/${closeId}/requests`, {
        expected_id: latest?.id ?? "",
        workpaper_ids: selected,
        ...(generate
          ? {}
          : { body, context_digest: latest?.context_digest ?? "" }),
      });
      await load();
      setMessage("Draft saved. Nothing was sent to a client.");
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Could not save request.");
    } finally {
      setBusy(false);
    }
  }
  const changedSelection =
    JSON.stringify([...selected].sort()) !==
    JSON.stringify([...(latest?.workpaper_ids ?? [])].sort());
  return (
    <section id="client-request">
      <h2>Consolidated client request</h2>
      <p>
        Draft one request for this client and month. Start from selected
        workpapers, then check the facts, remove internal notes and edit the
        wording. To approve a recipient and connect replies, load the saved
        draft from its Work inbox item. This editor never sends messages.
      </p>
      {!loaded && !message && <p role="status">Loading saved request…</p>}
      <fieldset disabled={busy || !loaded}>
        <legend>Work needing information · choose up to 20</legend>
        {papers.length === 0 && (
          <p>
            No saved workpapers yet. Generate an empty draft to write a specific
            evidence request.
          </p>
        )}
        {papers.map((p) => (
          <label key={p.id} className="checkbox-label">
            <input
              type="checkbox"
              checked={selected.includes(p.id)}
              onChange={(e) =>
                setSelected(
                  e.target.checked
                    ? [...selected, p.id]
                    : selected.filter((id) => id !== p.id),
                )
              }
            />
            <span>
              {p.kind} · draft #{p.id} · {p.review_state.replaceAll("_", " ")}
            </span>
          </label>
        ))}
        {selected.some((id) => !papers.some((p) => p.id === id)) && (
          <p>
            The saved request references earlier workpapers outside the current
            list. Reloading preserves those references.
          </p>
        )}
        <button disabled={selected.length > 20} onClick={() => void save(true)}>
          {latest
            ? "Generate new draft from current context"
            : "Generate request draft"}
        </button>
        {latest && (
          <p>
            Generating saves a new revision and replaces the editor text.
            Previous wording remains in history.
          </p>
        )}
      </fieldset>
      {latest && (
        <>
          <p>
            Draft #{latest.id} · {new Date(latest.created_at).toLocaleString()}{" "}
            · {latest.created_by} · draft revision; delivery history is in Work
          </p>
          {latest.stale && (
            <p role="alert">
              Evidence or review changed since this draft. Inspect the new
              context and generate a fresh draft before editing. Earlier wording
              is preserved below.
            </p>
          )}
          {changedSelection && (
            <p>
              Selection changed. Generate a new draft before saving edited
              wording.
            </p>
          )}
          <form
            onSubmit={(event) => {
              event.preventDefault();
              void save(
                false,
                String(new FormData(event.currentTarget).get("body") ?? ""),
              );
            }}
          >
            <label>
              Client request wording
              <textarea
                rows={14}
                maxLength={20000}
                name="body"
                key={latest.id}
                ref={editor}
                defaultValue={latest.body}
                required
                disabled={busy}
              />
            </label>
            <button
              disabled={busy || latest.stale || changedSelection}
              type="submit"
            >
              Save edited draft
            </button>
          </form>
          <details>
            <summary>Saved request history · latest 20 revisions</summary>
            {history.map((d) => (
              <article key={d.id}>
                <h3>
                  Draft #{d.id} · {new Date(d.created_at).toLocaleString()}
                </h3>
                <p>
                  {d.created_by} · workpapers{" "}
                  {d.workpaper_ids.join(", ") || "none selected"}
                </p>
                <pre
                  style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
                >
                  {d.body}
                </pre>
              </article>
            ))}
          </details>
        </>
      )}
      <button
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          try {
            await load();
            setMessage("Loaded the latest saved draft.");
          } catch (e) {
            setMessage(e instanceof Error ? e.message : "Reload failed.");
          } finally {
            setBusy(false);
          }
        }}
      >
        Reload saved request (discard unsaved edits)
      </button>
      <p role="status">{message}</p>
    </section>
  );
}
