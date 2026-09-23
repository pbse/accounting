import { useState, type FormEvent } from "react";
import { workspaceRequest } from "./workspace";

type Version = {
  id: string;
  client_entity: string;
  kind: string;
  instructions: string;
  expense_account: string;
  prepaid_account: string;
  enabled: boolean;
  reason: string;
  actor: string;
  created_at: string;
};
type Impact = {
  version_id: string;
  source_id: string;
  impact_digest: string;
  affected_drafts: { id: string; kind: string; review_state: string }[];
};
type Suggestion = { draft_instructions: string; changed_fields: string[]; source_draft_ids: string[] };
export function Procedures({
  closeId,
  entity,
  onApplied,
}: {
  closeId: string;
  entity: string;
  onApplied: () => void;
}) {
  const [versions, setVersions] = useState<Version[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [target, setTarget] = useState("client");
  const [kind, setKind] = useState("prepaid");
  const [editing, setEditing] = useState(false);
  const [applyConfirmed, setApplyConfirmed] = useState(false);
  const [message, setMessage] = useState("");
  const [impact, setImpact] = useState<Impact>();
  const [suggestion, setSuggestion] = useState<Suggestion>();
  async function load() {
    setBusy(true);
    setError("");
    try {
      const data = await workspaceRequest("/procedures");
      setVersions(data.versions);
      setImpact(undefined);
      setLoaded(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Procedures unavailable.");
    } finally {
      setBusy(false);
    }
  }
  const matches = (v: Version, client: string, k: string) =>
    v.kind === k && v.client_entity.toLowerCase() === client.toLowerCase();
  const previous = versions.find((v) =>
    matches(v, target === "client" ? entity : "", kind),
  );
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await workspaceRequest("/procedures", {
        previous_id: previous?.id ?? "",
        client_entity: target === "client" ? entity : "",
        kind,
        instructions: data.get("instructions"),
        expense_account: data.get("expense_account") ?? "",
        prepaid_account: data.get("prepaid_account") ?? "",
        enabled: data.get("enabled") === "on",
        reason: data.get("reason"),
        confirmed: data.get("confirmed") === "on",
      });
      setEditing(false);
      setMessage(
        "Published a new immutable version. Existing close context stays pinned until explicitly updated.",
      );
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Procedure save failed.");
    } finally {
      setBusy(false);
    }
  }
  async function apply(v: Version) {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/procedures/apply`, {
        kind: v.kind,
        version_id: v.id,
        impact_digest: impact?.impact_digest,
        confirmed: applyConfirmed,
      });
      setImpact(undefined);
      onApplied();
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "Procedure could not be applied.",
      );
    } finally {
      setBusy(false);
    }
  }
  async function preview(v: Version) {
    setBusy(true);
    setError("");
    setImpact(undefined);
    setApplyConfirmed(false);
    try {
      setImpact(await workspaceRequest(`/${closeId}/procedures/impact?kind=${v.kind}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Impact preview unavailable.");
    } finally {
      setBusy(false);
    }
  }
  async function suggest() {
    setBusy(true); setError("");setSuggestion(undefined);
    try { const next = await workspaceRequest(`/${closeId}/procedures/suggestion?kind=${kind}`) as Suggestion; setSuggestion(next);setEditing(true); }
    catch (e) { setError(e instanceof Error ? e.message : "Prior edits unavailable."); }
    finally { setBusy(false); }
  }
  return (
    <details
      onToggle={(event) => {
        if (event.currentTarget.open && !loaded && !busy) void load();
      }}
    >
      <summary>Approved procedures and client overrides</summary>
      <p>
        Use instructions approved by the client’s accounting professional.
        Procedures supply cited context and optional account names; they cannot
        authorize unsupported treatment, confirm invoice booking or approve
        posting.
      </p>
      {error && <p role="alert">{error}</p>}
      {message && <p role="status">{message}</p>}
      <button
        type="button"
        disabled={busy || editing}
        onClick={() => void load()}
      >
        Refresh procedures
      </button>
      {loaded && (
        <>
          <h3>Effective versions for {entity}</h3>
          {["prepaid", "reconciliation", "accrual"].map((k) => {
            const v =
              versions.find((p) => matches(p, entity, k)) ??
              versions.find((p) => matches(p, "", k));
            return (
              <section key={k}>
                <h4>
                  {k === "prepaid" ? "Prepaid preparation" : k === "accrual" ? "Accrual preparation" : "Statement reconciliation"}
                </h4>
                {!v ? (
                  <p>
                    No approved procedure. Required facts and assumptions must
                    be supplied and reviewed explicitly.
                  </p>
                ) : (
                  <>
                    <p>
                      Version #{v.id} ·{" "}
                      {v.client_entity ? "Client override" : "Firm default"} ·{" "}
                      {v.enabled
                        ? "Enabled for new context"
                        : "Disabled for new context"}
                    </p>
                    <p>{v.instructions}</p>
                    {v.expense_account && (
                      <p>Expense account: {v.expense_account}</p>
                    )}
                    {v.prepaid_account && (
                      <p>Prepaid account: {v.prepaid_account}</p>
                    )}
                    <p>
                      Approval record: {v.reason} · {v.actor} ·{" "}
                      {new Date(v.created_at).toLocaleString()}
                    </p>
                    {v.enabled && <>
                      <button disabled={busy} onClick={() => void preview(v)}>Preview affected work</button>
                      {impact?.version_id === v.id && <>
                        <p>Current pinned procedure source: {impact.source_id ? `#${impact.source_id}` : "none"}. Applying this version changes the cited context for {impact.affected_drafts.length} workpaper(s); their review may become stale.</p>
                        {impact.affected_drafts.length > 0 && <ul>{impact.affected_drafts.map((draft) => <li key={draft.id}>Draft #{draft.id} · {draft.kind} · {draft.review_state}</li>)}</ul>}
                        <button disabled={busy || !applyConfirmed} onClick={() => void apply(v)}>Apply version #{v.id} to this close</button>
                      </>}
                    </>}
                  </>
                )}
              </section>
            );
          })}
          <label>
            <input
              type="checkbox"
              checked={applyConfirmed}
              onChange={(e) => setApplyConfirmed(e.target.checked)}
            />
            I reviewed this procedure for this client. Applying a newer version
            changes evidence context and can invalidate dependent reviews and
            sign-off.
          </label>
          <p>
            Initial uploads automatically attach effective procedures when this
            close has no pinned version. Existing pinned copies stay visible
            under Source evidence. To update existing work, apply the new
            version here and recheck or revise affected preparation.
          </p>
          {!editing ? (
            <>
              <label>
                Procedure scope
                <select
                  value={target}
                  onChange={(e) => setTarget(e.target.value)}
                >
                  <option value="client">This client override</option>
                  <option value="firm">Firm default</option>
                </select>
              </label>
              <label>
                Supported work
                <select value={kind} onChange={(e) => setKind(e.target.value)}>
                  <option value="prepaid">Prepaid preparation</option>
                  <option value="accrual">Fixed incurred accrual</option>
                <option value="reconciliation">
                    Statement reconciliation
                  </option>
                </select>
              </label>
              {target === "client" && <button onClick={() => void suggest()} disabled={busy}>Draft review points from prior edits</button>}
              <button onClick={() => { setSuggestion(undefined);setEditing(true); }} disabled={busy}>
                Propose and approve a new version
              </button>
            </>
          ) : (
            <form onSubmit={(event) => void save(event)}>
              <fieldset disabled={busy}>
                <legend>
                  {target === "client" ? `${entity} override` : "Firm default"}{" "}
                  · {kind}
                </legend>
                <label>
                  Approved instructions
                  <textarea
                    name="instructions"
                    required
                    maxLength={4000}
                    defaultValue={suggestion?.draft_instructions || previous?.instructions || ""}
                  />
                </label>
                {suggestion && <p>Suggested from up to 20 recent revision drafts {suggestion.source_draft_ids.join(", ") || "none"}. Check every field and rewrite this draft before approval; prior edits do not establish policy or materiality.</p>}
                {kind === "prepaid" && (
                  <>
                    <label>
                      Expense account name (optional)
                      <input
                        name="expense_account"
                        maxLength={200}
                        defaultValue={previous?.expense_account ?? ""}
                      />
                    </label>
                    <label>
                      Prepaid account name (optional)
                      <input
                        name="prepaid_account"
                        maxLength={200}
                        defaultValue={previous?.prepaid_account ?? ""}
                      />
                    </label>
                  </>
                )}
                <label>
                  <input
                    type="checkbox"
                    name="enabled"
                    defaultChecked={previous?.enabled ?? true}
                  />
                  Enable for new context (a disabled client override suppresses
                  the firm default)
                </label>
                <label>
                  Approval reference, accounting professional and reason for
                  this version
                  <textarea name="reason" required maxLength={2000} />
                </label>
                <label>
                  <input type="checkbox" name="confirmed" required />I am
                  authorized to record this approved procedure and have checked
                  its applicability. Unsupported accounting methods remain
                  blocked.
                </label>
                <button>Publish approved version</button>
                <button type="button" onClick={() => setEditing(false)}>
                  Cancel
                </button>
              </fieldset>
            </form>
          )}
          <details>
            <summary>Version history ({versions.length})</summary>
            <ol>
              {versions.map((v) => (
                <li key={v.id}>
                  #{v.id} · {v.client_entity || "Firm default"} · {v.kind} ·{" "}
                  {v.enabled ? "enabled" : "disabled"}
                  <p>{v.instructions}</p>
                  <p>
                    {v.reason} · {v.actor}
                  </p>
                </li>
              ))}
            </ol>
          </details>
        </>
      )}
    </details>
  );
}
