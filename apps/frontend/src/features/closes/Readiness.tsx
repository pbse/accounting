import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Scope = {
  description: string;
  required_papers: string[];
  external_steps: { name: string; complete: boolean; reference: string }[];
};
type Status = {
  scope: Scope;
  suggested_papers: string[];
  suggestion_limited: boolean;
  digest: string;
  decision_id: string;
  state: "open" | "signed_off" | "reopen_required";
  blockers: string[];
  history: {
    id: string;
    decision: string;
    note: string;
    actor: string;
    created_at: string;
  }[];
};

export function Readiness({
  closeId,
  papers,
}: {
  closeId: string;
  papers: { id: string; kind: string; review_state: string }[];
}) {
  const [status, setStatus] = useState<Status>();
  const [scope, setScope] = useState<Scope>();
  const [editing, setEditing] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  useEffect(() => {
    let live = true;
    setStatus(undefined);
    setScope(undefined);
    setEditing(false);
    setError("");
    void workspaceRequest(`/${closeId}/readiness`)
      .then((data: Status) => {
        if (live) setStatus(data);
      })
      .catch((e: Error) => {
        if (live) setError(e.message);
      });
    return () => {
      live = false;
    };
  }, [closeId]);
  async function refresh() {
    setBusy(true);
    setError("");
    try {
      setStatus(await workspaceRequest(`/${closeId}/readiness`));
      setConfirmed(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Readiness unavailable.");
    } finally {
      setBusy(false);
    }
  }
  async function save(action: "scope" | "sign" | "reopen") {
    if (!status) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const saved = await workspaceRequest(
        `/${closeId}/${action === "scope" ? "scope" : "signoff"}`,
        {
          digest: status.digest,
          expected_decision_id: status.decision_id,
          ...(action === "scope" ? { scope } : { action, note, confirmed }),
        },
      );
      if (saved.carry_error) setNotice(`Signed off. ${saved.carry_error}`);
      else if (saved.carried) setNotice(`Next month is started: ${saved.carried} reviewed ${saved.carried === 1 ? "schedule is" : "schedules are"} queued for next-month preparation and review.`);
      setStatus(await workspaceRequest(`/${closeId}/readiness`));
      setEditing(false);
      setNote("");
      setConfirmed(false);
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : "Save could not be confirmed. Refresh before retrying.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-labelledby="readiness-heading" className="panel">
      <h2 id="readiness-heading">Close readiness</h2>
      <p>
        Sign-off covers the scope you define here. It does not certify unlisted
        work or verify production ledger balances. External steps are your
        recorded attestations.
      </p>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      <button
        type="button"
        disabled={busy || editing}
        onClick={() => void refresh()}
      >
        Refresh readiness
      </button>
      {status && (
        <>
          <p role="status">
            <strong>
              {status.state === "signed_off"
                ? "Supported scope signed off"
                : status.state === "reopen_required"
                  ? "Changed since sign-off — reopen required"
                  : status.blockers.length
                    ? "Not ready for sign-off"
                    : "Ready for an authorized decision"}
            </strong>
          </p>
          {status.blockers.length > 0 && (
            <ul>
              {status.blockers.map((b, i) => (
                <li key={i}>{b}</li>
              ))}
            </ul>
          )}
          {status.scope.description && (
            <p>
              <strong>Scope: </strong>
              {status.scope.description}
            </p>
          )}
          {status.scope.required_papers.length > 0 && (
            <p>
              Required drafts:{" "}
              {status.scope.required_papers.map((id) => `#${id}`).join(", ")}
            </p>
          )}
          {status.scope.external_steps.length > 0 && (
            <ul>
              {status.scope.external_steps.map((s, i) => (
                <li key={i}>
                  {s.complete ? "Done" : "Outstanding"}: {s.name}
                  {s.reference && ` — ${s.reference}`}
                </li>
              ))}
            </ul>
          )}
          {status.state === "open" && !editing && (
            <button
              disabled={busy}
              onClick={() => {
                setScope(structuredClone(status.scope));
                setEditing(true);
              }}
            >
              Define or update scope
            </button>
          )}
          {editing && scope && (
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void save("scope");
              }}
            >
              <fieldset disabled={busy}>
                <legend>Required work and external steps</legend>
                <p>
                  The current drafts can be proposed as required work. Check
                  their sources and add missing accounts or external steps;
                  this does not certify the whole ledger.
                </p>
                {status.suggested_papers.length > 0 && <button
                  type="button"
                  onClick={() => setScope({
                    ...scope,
                    required_papers: status.suggested_papers,
                  })}
                >
                  Replace selection with {status.suggested_papers.length} latest draft
                  {status.suggested_papers.length === 1 ? "" : "s"}
                </button>}
                {status.suggestion_limited &&
                  <p role="alert">More than 100 current drafts exist. Select a bounded supported scope manually.</p>}
                <label>
                  Supported scope and explicit exclusions
                  <textarea
                    required
                    maxLength={2000}
                    value={scope.description}
                    onChange={(e) =>
                      setScope({ ...scope, description: e.target.value })
                    }
                    placeholder="For example: September insurance prepaids only. Payroll, tax and full ledger close are excluded."
                  />
                </label>
                <p>
                  Select the exact drafts required for this scope. Earlier
                  versions are alternatives, not additional balances.
                </p>
                {papers.map((p) => (
                  <label key={p.id}>
                    <input
                      type="checkbox"
                      checked={scope.required_papers.includes(p.id)}
                      onChange={(e) =>
                        setScope({
                          ...scope,
                          required_papers: e.target.checked
                            ? [...scope.required_papers, p.id]
                            : scope.required_papers.filter((id) => id !== p.id),
                        })
                      }
                    />
                    Draft #{p.id} · {p.kind} ·{" "}
                    {p.review_state.replaceAll("_", " ")}
                  </label>
                ))}
                {scope.required_papers
                  .filter((id) => !papers.some((p) => p.id === id))
                  .map((id) => (
                    <label key={id}>
                      <input
                        type="checkbox"
                        checked
                        onChange={() =>
                          setScope({
                            ...scope,
                            required_papers: scope.required_papers.filter(
                              (p) => p !== id,
                            ),
                          })
                        }
                      />
                      Draft #{id} (outside the current page)
                    </label>
                  ))}
                {scope.external_steps.map((s, i) => (
                  <fieldset key={i}>
                    <legend>External step {i + 1}</legend>
                    <label>
                      Required action
                      <input
                        required
                        maxLength={200}
                        value={s.name}
                        onChange={(e) =>
                          setScope({
                            ...scope,
                            external_steps: scope.external_steps.map((v, n) =>
                              n === i ? { ...v, name: e.target.value } : v,
                            ),
                          })
                        }
                      />
                    </label>
                    <label>
                      <input
                        type="checkbox"
                        checked={s.complete}
                        onChange={(e) =>
                          setScope({
                            ...scope,
                            external_steps: scope.external_steps.map((v, n) =>
                              n === i
                                ? { ...v, complete: e.target.checked }
                                : v,
                            ),
                          })
                        }
                      />
                      I verified this step is complete
                    </label>
                    <label>
                      Evidence or completion reference
                      <textarea
                        required={s.complete}
                        maxLength={2000}
                        value={s.reference}
                        onChange={(e) =>
                          setScope({
                            ...scope,
                            external_steps: scope.external_steps.map((v, n) =>
                              n === i ? { ...v, reference: e.target.value } : v,
                            ),
                          })
                        }
                      />
                    </label>
                    <button
                      type="button"
                      onClick={() =>
                        setScope({
                          ...scope,
                          external_steps: scope.external_steps.filter(
                            (_, n) => n !== i,
                          ),
                        })
                      }
                    >
                      Remove step {i + 1}
                    </button>
                  </fieldset>
                ))}
                <button
                  type="button"
                  disabled={scope.external_steps.length >= 20}
                  onClick={() =>
                    setScope({
                      ...scope,
                      external_steps: [
                        ...scope.external_steps,
                        { name: "", complete: false, reference: "" },
                      ],
                    })
                  }
                >
                  Add external step
                </button>
                <button disabled={scope.required_papers.length === 0 && scope.external_steps.length === 0}>
                  Save scope revision
                </button>
                <button type="button" onClick={() => setEditing(false)}>
                  Cancel changes
                </button>
              </fieldset>
            </form>
          )}
          {!editing && (
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void save(status.state === "open" ? "sign" : "reopen");
              }}
            >
              <fieldset
                disabled={
                  busy ||
                  (status.state === "open" && status.blockers.length > 0)
                }
              >
                <legend>
                  {status.state === "open"
                    ? "Authorized sign-off"
                    : "Reopen this scope"}
                </legend>
                <label>
                  Decision and reason
                  <textarea
                    required
                    maxLength={2000}
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                  />
                </label>
                <label>
                  <input
                    type="checkbox"
                    required
                    checked={confirmed}
                    onChange={(e) => setConfirmed(e.target.checked)}
                  />
                  {status.state === "open"
                    ? "I reviewed this supported scope, its evidence and external steps, and am authorized to sign it off."
                    : "I intend to reopen this scope for further work and review."}
                </label>
                <button>
                  {status.state === "open"
                    ? "Sign off supported scope"
                    : "Reopen with reason"}
                </button>
              </fieldset>
            </form>
          )}
          <p>
            New evidence, work or review changes invalidate the current
            sign-off. Local token decisions are labeled development, with no
            verified individual identity.
          </p>
          {status.history.length > 0 && (
            <details>
              <summary>Decision history ({status.history.length})</summary>
              <ol>
                {status.history.map((h) => (
                  <li key={h.id}>
                    <strong>{h.decision.replaceAll("_", " ")}</strong> ·{" "}
                    {h.actor} · {new Date(h.created_at).toLocaleString()}
                    <p>{h.note}</p>
                  </li>
                ))}
              </ol>
            </details>
          )}
        </>
      )}
    </section>
  );
}
