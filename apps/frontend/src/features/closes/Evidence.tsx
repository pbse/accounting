import { DriveEvidence } from "./DriveEvidence";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { workspaceRequest } from "./workspace";
import { ForwardedEvidence } from "./ForwardedEvidence";
import { StatementRows } from "./StatementRows";

export type Source = {
  id: string;
  name: string;
  digest: string;
  role: string;
  location: string;
};
export type EvidenceFile = {
  id: string;
  name: string;
  digest: string;
  purpose?: string;
  bytes: number;
  extraction_state: string;
  extraction_error: string;
  superseded_by: string | null;
  replacement_reason: string | null;
  procedure_id?: string | null;
  procedure_kind?: string | null;
  checks?: { pages: number; warnings: string[]; questions: string[] };
  created_by?: string;
  agent?: string | null;
};

export function SourceReview({
  closeId,
  sources,
}: {
  closeId: string;
  sources: Source[];
}) {
  const [opened, setOpened] = useState<{ name: string; text: string }>();
  const [error, setError] = useState("");
  // An assistant link to one document (#source-ID) scrolls to it once the list has loaded.
  useEffect(() => {
    if (location.hash.startsWith("#source-")) document.getElementById(location.hash.slice(1))?.scrollIntoView({ block: "center" });
  }, [sources.length]);
  return (
    <section>
      <h4>Linked source evidence</h4>
      <p>
        Original text or page-labeled PDF extraction. OCR may misread values;
        compare with the original. Links and entered facts require human review;
        attaching a source does not verify its meaning.
      </p>
      <ul>
        {sources.map((source) => (
          <li key={`${source.id}-${source.role}`} id={source.role === "uploaded" ? `source-${source.id}` : undefined}>
            {source.role}: {source.name} · {source.location}{" "}
            <button
              onClick={async () => {
                setError("");
                setOpened(undefined);
                try {
                  const blob = await workspaceRequest(
                    `/${closeId}/evidence/${source.id}/text`,
                    undefined,
                    true,
                  );
                  setOpened({ name: source.name, text: await blob.text() });
                } catch (e) {
                  setError(
                    e instanceof Error ? e.message : "Could not open source.",
                  );
                }
              }}
            >
              Inspect source text
            </button>
            <button
              onClick={async () => {
                try {
                  const blob = await workspaceRequest(
                    `/${closeId}/evidence/${source.id}`,
                    undefined,
                    true,
                  );
                  const url = URL.createObjectURL(blob);
                  const link = document.createElement("a");
                  link.href = url;
                  link.download = source.name;
                  link.click();
                  setTimeout(() => URL.revokeObjectURL(url), 1000);
                } catch (e) {
                  setError(e instanceof Error ? e.message : "Download failed.");
                }
              }}
            >
              Download original
            </button>
            <details>
              <summary>Source revision</summary>
              <code>{source.digest}</code>
            </details>
          </li>
        ))}
      </ul>
      {error && <p role="alert">{error}</p>}
      {opened && (
        <div>
          <h4>{opened.name}</h4>
          <button onClick={() => setOpened(undefined)}>Close source</button>
          <pre className="scroll-text">
            {opened.text}
          </pre>
        </div>
      )}
    </section>
  );
}

export function Evidence({
  closeId,
  onReplace,
}: {
  closeId: string;
  onReplace: () => void;
}) {
  const [oldId, setOldId] = useState("");
  const [newId, setNewId] = useState("");
  const [reason, setReason] = useState("");
  const [comparison, setComparison] = useState<{
    before: string;
    after: string;
  }>();
  const [files, setFiles] = useState<EvidenceFile[]>([]);
  const checking = useRef(false);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function load(includeChecks = true) {
    try {
      const fresh: EvidenceFile[] = await workspaceRequest(`/${closeId}/evidence${includeChecks ? "?checks=1" : ""}`);
      setFiles((previous) => fresh.map((file) => ({ ...file, checks: file.checks ?? previous.find((old) => old.id === file.id)?.checks })));
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Could not load evidence.");
    }
  }
  useEffect(() => {
    void load();
  }, [closeId]);
  const extracting = files.some((f) =>
    ["queued", "processing"].includes(f.extraction_state),
  );
  useEffect(() => {
    if (!extracting) return;
    const timer = setInterval(() => void load(false), 3000);
    return () => clearInterval(timer);
  }, [closeId, extracting]);
  useEffect(() => {
    if (checking.current || !files.some((file) => file.extraction_state === "ready" && !file.superseded_by && !file.checks)) return;
    checking.current = true;
    void load(true).finally(() => { checking.current = false; });
  }, [closeId, files]);
  async function upload(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    const data = new FormData(form);
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest(`/${closeId}/evidence`, data);
      form.reset();
      await load();
      setMessage(
        "Evidence saved. Identical content in this close reuses its existing source ID.",
      );
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Upload failed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h2>Source evidence</h2>
      <p>
        Upload text/CSV up to 512 KiB, PDF up to 5 MiB and 20 pages, or an
        Excel workbook (.xlsx) up to 5 MiB, whose first sheet is kept as CSV.
        PDF extraction runs in the background, with English OCR for pages
        without text. Files belong to this client and month.
      </p>
      <form onSubmit={upload}>
        <label>
          Evidence file
          <input name="file" type="file" accept=".txt,.csv,.pdf,.xlsx" required />
        </label>
        <button disabled={busy}>
          {busy ? "Uploading…" : "Upload evidence"}
        </button>
      </form>
      <ForwardedEvidence closeId={closeId} onReleased={() => { void load(); onReplace(); }} />
      <DriveEvidence closeId={closeId} onAdded={() => void load()} />
      {files.filter((file) => !file.superseded_by && files.some((other) => other.id !== file.id && other.name.toLowerCase() === file.name.toLowerCase() && other.digest !== file.digest && !other.superseded_by && BigInt(other.id) < BigInt(file.id))).map((file) => {
        const previous = files.find((other) => other.name.toLowerCase() === file.name.toLowerCase() && other.digest !== file.digest && !other.superseded_by && BigInt(other.id) < BigInt(file.id));
        return previous && <p key={file.id} role="status">{file.name} has different bytes in source #{previous.id} and #{file.id}. The agent keeps both as separate evidence until you compare them. <button onClick={() => { setOldId(previous.id); setNewId(file.id); setComparison(undefined); }}>Select for comparison</button></p>;
      })}
      {files.filter((file) => !file.superseded_by && file.checks && (file.checks.warnings.length || file.checks.questions.length)).map((file) => <div key={`check-${file.id}`} role="status">
        <strong>{file.name} · source #{file.id}</strong>{file.checks?.pages ? ` · ${file.checks.pages} extracted pages` : ""}
        <ul>{[...(file.checks?.questions ?? []), ...(file.checks?.warnings ?? [])].map((issue, index) => <li key={`${file.id}-${index}`}>{issue}</li>)}</ul>
        <p>Compare the original and linked facts before confirming work. If this is a revised or incomplete file, upload the complete copy and use Replace source.</p>
      </div>)}
      <details>
        <summary>Replace or compare source evidence</summary>
        <p>
          Upload the new file first. Explicit replacement keeps both originals
          and invalidates only work linked to the old source. It does not
          reverse posted entries. Use Revise and recalculate on an affected
          draft to confirm revised facts and automatically queue its
          calculation.
        </p>
        <label>
          Original source
          <select
            value={oldId}
            onChange={(e) => {
              setOldId(e.target.value);
              setNewId(
                files.find((f) => f.id === e.target.value)?.superseded_by ?? "",
              );
              setComparison(undefined);
            }}
          >
            <option value="">Choose original</option>
            {files.map((f) => (
              <option key={f.id} value={f.id}>
                {f.name} · #{f.id}
                {f.superseded_by ? " (replaced)" : ""}
              </option>
            ))}
          </select>
        </label>
        <label>
          Replacement source
          <select
            value={newId}
            onChange={(e) => {
              setNewId(e.target.value);
              setComparison(undefined);
            }}
          >
            <option value="">Choose replacement</option>
            {files
              .filter((f) => BigInt(f.id) > BigInt(oldId || "0"))
              .map((f) => (
                <option key={f.id} value={f.id}>
                  {f.name} · #{f.id}
                  {f.superseded_by ? " (replaced)" : ""}
                </option>
              ))}
          </select>
        </label>
        <button
          disabled={busy || !oldId || !newId}
          onClick={async () => {
            setBusy(true);
            setMessage("");
            try {
              const [before, after] = await Promise.all([
                workspaceRequest(
                  `/${closeId}/evidence/${oldId}/text`,
                  undefined,
                  true,
                ),
                workspaceRequest(
                  `/${closeId}/evidence/${newId}/text`,
                  undefined,
                  true,
                ),
              ]);
              setComparison({
                before: await before.text(),
                after: await after.text(),
              });
            } catch (e) {
              setMessage(
                e instanceof Error ? e.message : "Comparison unavailable.",
              );
            } finally {
              setBusy(false);
            }
          }}
        >
          Compare source text
        </button>
        {comparison && (
          <div className="form-grid">
            <div>
              <h4>Before · #{oldId}</h4>
              <pre className="scroll-text">
                {comparison.before}
              </pre>
            </div>
            <div>
              <h4>After · #{newId}</h4>
              <pre className="scroll-text">
                {comparison.after}
              </pre>
            </div>
          </div>
        )}
        <label>
          What changed?
          <textarea
            value={reason}
            maxLength={2000}
            onChange={(e) => setReason(e.target.value)}
          />
        </label>
        <button
          disabled={
            busy ||
            !oldId ||
            !newId ||
            !reason.trim() ||
            !!files.find((f) => f.id === oldId)?.superseded_by ||
            !!files.find((f) => f.id === newId)?.superseded_by
          }
          onClick={async () => {
            setBusy(true);
            setMessage("");
            try {
              await workspaceRequest(`/${closeId}/evidence/${oldId}/replace`, {
                replacement_id: newId,
                reason,
              });
              onReplace();
            } catch (e) {
              setMessage(
                e instanceof Error ? e.message : "Could not replace source.",
              );
            } finally {
              setBusy(false);
            }
          }}
        >
          Replace source and invalidate affected review
        </button>
      </details>
      <p role="status">{message}</p>
      {files
        .filter((f) => f.extraction_state !== "ready")
        .map((f) => (
          <p key={f.id} role="status">
            {f.name}: {f.extraction_state}. {f.extraction_error}
            {f.extraction_state === "failed" && (
              <button
                onClick={async () => {
                  try {
                    await workspaceRequest(
                      `/${closeId}/evidence/${f.id}/retry`,
                      {},
                    );
                    await load();
                  } catch (e) {
                    setMessage(
                      e instanceof Error ? e.message : "Retry failed.",
                    );
                  }
                }}
              >
                Retry extraction
              </button>
            )}
          </p>
        ))}
      {files.length === 0 ? (
        <p>No evidence uploaded yet.</p>
      ) : (
        <SourceReview
          closeId={closeId}
          sources={files.map((f) => ({
            ...f,
            role: "uploaded",
            location: `${f.bytes} bytes${f.agent ? ` · added by ${f.created_by} via ${f.agent}` : ""}${f.superseded_by ? ` · replaced by source #${f.superseded_by}: ${f.replacement_reason}` : " · current"}`,
          }))}
        />
      )}
      {files
        .filter((f) => f.name.toLowerCase().endsWith(".pdf") && f.extraction_state === "ready" && !f.superseded_by)
        .map((f) => (
          <div key={`statement-${f.id}`}>
            <strong>{f.name}</strong>
            <StatementRows closeId={closeId} evidenceId={f.id} onConfirmed={() => void load()} />
          </div>
        ))}
    </section>
  );
}

type Plan = {
  id: string;
  created_at: string;
  plan: { kind: string; input: unknown; sources: Source[] };
};
type Job = {
  id: string;
  state: string;
  error: string;
  created_at: string;
  workpaper_ids: string[];
};
export function PreparationJobs({
  closeId,
  onComplete,
}: {
  closeId: string;
  onComplete: () => void;
}) {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function refresh() {
    try {
      const [p, j] = await Promise.all([
        workspaceRequest(`/${closeId}/preparations`),
        workspaceRequest(`/${closeId}/jobs`),
      ]);
      setPlans(p);
      setJobs(j);
    } catch (e) {
      setMessage(
        e instanceof Error ? e.message : "Could not load preparation.",
      );
    }
  }
  useEffect(() => {
    void refresh();
  }, [closeId]);
  const pending = jobs.some((job) => job.state === "queued");
  useEffect(() => {
    if (!pending) return;
    const timer = setInterval(() => {
      workspaceRequest(`/${closeId}/jobs`)
        .then(setJobs)
        .catch((e) => setMessage(e.message));
    }, 3000);
    return () => clearInterval(timer);
  }, [closeId, pending]);
  return (
    <section>
      <h2>Run saved preparation instructions</h2>
      <p>
        After uploading evidence, use a preparation form to confirm the method
        and save source-linked instructions. Select the revisions to run
        together. Alternative versions are not additive.
      </p>
      <p>
        Showing the latest 100 instructions and 20 jobs. Each run supports up to
        20 instructions.
      </p>
      {plans.map((p) => (
        <div key={p.id}>
          <label>
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
            {p.plan.kind} · instructions #{p.id} ·{" "}
            {new Date(p.created_at).toLocaleString()}
          </label>
          <details>
            <summary>Inspect instructions and evidence</summary>
            <SourceReview closeId={closeId} sources={p.plan.sources} />
            <pre>{JSON.stringify(p.plan.input, null, 2)}</pre>
          </details>
        </div>
      ))}
      <button
        disabled={busy || selected.length === 0 || selected.length > 20}
        onClick={async () => {
          setBusy(true);
          setMessage("");
          try {
            const job = await workspaceRequest(`/${closeId}/jobs`, {
              plans: selected,
            });
            setMessage(
              `Preparation job #${job.id} saved. You can leave this page; the worker will prepare and save the drafts.`,
            );
            await refresh();
          } catch (e) {
            setMessage(
              e instanceof Error ? e.message : "Could not queue preparation.",
            );
          } finally {
            setBusy(false);
          }
        }}
      >
        Prepare selected work
      </button>
      <p role="status">{message}</p>
      <ul>
        {jobs.map((j) => (
          <li key={j.id}>
            Job #{j.id} ·{" "}
            {j.state === "queued"
              ? "Waiting for worker or processing"
              : j.state}{" "}
            · {new Date(j.created_at).toLocaleString()}
            {j.error && <p role="alert">{j.error}</p>}
            {j.state === "completed" && (
              <>
                <p>
                  Saved draft IDs: {j.workpaper_ids.join(", ")}. Preparation is
                  not approval or posting.
                </p>
                <button onClick={onComplete}>Show prepared drafts</button>
              </>
            )}
            {j.state === "queued" && (
              <button
                onClick={async () => {
                  try {
                    await workspaceRequest(
                      `/${closeId}/jobs/${j.id}/cancel`,
                      {},
                    );
                    await refresh();
                  } catch (e) {
                    setMessage(
                      e instanceof Error ? e.message : "Could not cancel.",
                    );
                  }
                }}
              >
                Cancel if not started
              </button>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
