import { useState, type FormEvent } from "react";
import { workspaceRequest } from "./workspace";
import { ProcedureLearning } from "./ProcedureLearning";

type Version = {
  id: string;
  client_entity: string;
  kind: string;
  method: string;
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
type PublishImpact = { kind: string; client_entity: string; previous_id: string; impact_digest: string; total_closes: number; has_more: boolean; closes: { close_id: string; entity: string; period: string; pinned_source_id: string; draft_count: number }[] };
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
  const [publishImpact, setPublishImpact] = useState<PublishImpact>();
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
    if (!publishImpact || publishImpact.kind !== kind || publishImpact.previous_id !== (previous?.id ?? "") || publishImpact.client_entity.toLowerCase() !== (target === "client" ? entity : "").toLowerCase()) {
      setError("Review the affected closes before publishing this procedure.");
      return;
    }
    const data = new FormData(event.currentTarget);
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await workspaceRequest("/procedures", {
        previous_id: previous?.id ?? "",
        impact_digest: publishImpact.impact_digest,
        client_entity: target === "client" ? entity : "",
        kind,
        method: kind === "loan" ? data.get("loan_method") : kind === "basis" ? data.get("basis") : kind === "owner" && target === "client" ? data.get("entity_type") : "",
        instructions: data.get("instructions"),
        expense_account: data.get("expense_account") ?? "",
        prepaid_account: data.get("prepaid_account") ?? "",
        enabled: kind === "basis" || data.get("enabled") === "on",
        reason: data.get("reason"),
        confirmed: data.get("confirmed") === "on",
      });
      setEditing(false);
      setPublishImpact(undefined);
      setMessage(
        "Published a new immutable version. Existing close context stays pinned until explicitly updated.",
      );
      await load();
      if (kind === "basis") onApplied();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Procedure save failed.");
    } finally {
      setBusy(false);
    }
  }
  async function previewPublish() {
    setBusy(true);
    setError("");
    setPublishImpact(undefined);
    try {
      setPublishImpact(await workspaceRequest(`/procedures?impact_kind=${encodeURIComponent(kind)}&client_entity=${encodeURIComponent(target === "client" ? entity : "")}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Affected closes unavailable.");
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
          {["basis", "owner", "prepaid", "reconciliation", "accrual", "loan"].map((k) => {
            const v =
              versions.find((p) => matches(p, entity, k)) ??
              versions.find((p) => matches(p, "", k));
            return (
              <section key={k}>
                <h4>
                  {k === "basis" ? "Accounting basis" : k === "owner" ? "Owner treatments" : k === "prepaid" ? "Prepaid preparation" : k === "accrual" ? "Accrual preparation" : k === "loan" ? "Loan schedules" : "Statement reconciliation"}
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
                    {v.method && <p>Approved method boundary: {v.method}</p>}
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
                    {v.enabled && k !== "basis" && k !== "owner" && <>
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
                  <option value="loan">Loan schedules (lender statement method)</option>
                  <option value="basis">Accounting basis</option>
                  <option value="owner">Owner treatments (entity type)</option>
                </select>
              </label>
              {target === "client" && kind !== "basis" && kind !== "owner" && <button onClick={() => void suggest()} disabled={busy}>Draft review points from prior edits</button>}
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
                {kind === "owner" && target === "client" && (
                  <label>
                    Entity type
                    <select name="entity_type" required defaultValue={previous?.method ?? ""}>
                      <option value="">Choose the client's entity type</option>
                      <option value="sole_proprietor">Sole proprietor / single-member LLC</option>
                      <option value="partnership">Partnership / multi-member LLC</option>
                      <option value="s_corp">S corporation</option>
                      <option value="c_corp">C corporation</option>
                    </select>
                  </label>
                )}
                {kind === "loan" && (
                  <label>
                    Loan method
                    <select name="loan_method" required defaultValue={previous?.method ?? "lender_statement_v1"}>
                      <option value="lender_statement_v1">Lender statement, fixed rate</option>
                      <option value="lender_statement_variable_v1">Lender statement with rate changes and fees added to the balance</option>
                      <option value="lender_statement_costs_v1">Lender statement with rate changes, fees and debt issuance costs</option>
                    </select>
                  </label>
                )}
                {kind === "basis" ? (
                  <label>
                    Accounting basis
                    <select name="basis" required defaultValue={previous?.method ?? ""}>
                      <option value="">Choose the basis the accountant approved</option>
                      <option value="accrual">Accrual</option>
                      <option value="modified_cash">Modified cash (no accruals or prepaid schedules)</option>
                      <option value="cash">Cash (no accruals or prepaid schedules)</option>
                    </select>
                  </label>
                ) : (
                <label>
                  <input
                    type="checkbox"
                    name="enabled"
                    defaultChecked={previous?.enabled ?? true}
                  />
                  Enable for new context (a disabled client override suppresses
                  the firm default)
                </label>
                )}
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
                <button type="button" onClick={() => void previewPublish()}>Review affected closes</button>
                {publishImpact?.kind === kind && publishImpact.client_entity.toLowerCase() === (target === "client" ? entity : "").toLowerCase() && <section role="status">
                  <p>{publishImpact.total_closes} existing close{publishImpact.total_closes === 1 ? "" : "s"} in this procedure scope. Publishing leaves their pinned context unchanged; each close needs a separate reviewed apply action.</p>
                  {publishImpact.has_more && <p>Showing the latest 100 closes. Open older closes individually before a firm-wide change.</p>}
                  <ul>{publishImpact.closes.map(item => <li key={item.close_id}>{item.entity} · {item.period} · {item.draft_count} draft{item.draft_count === 1 ? "" : "s"} · {item.pinned_source_id ? `pinned source #${item.pinned_source_id}` : "no pinned procedure"}</li>)}</ul>
                </section>}
                <button disabled={!publishImpact || publishImpact.kind !== kind || publishImpact.previous_id !== (previous?.id ?? "") || publishImpact.client_entity.toLowerCase() !== (target === "client" ? entity : "").toLowerCase()}>Publish approved version</button>
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
          <ProcedureLearning closeId={closeId} entity={entity} onActivated={() => { void load(); onApplied(); }} />
        </>
      )}
    </details>
  );
}
