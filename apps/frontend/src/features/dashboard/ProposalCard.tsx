import { useDates } from "../../data/time";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { money, workspaceRequest } from "../closes/workspace";

type Outcome = { at: string } & Record<string, unknown>;

// ProposalCard is the agent's card for an action its own route has no page card for yet: asking a person to review a
// draft, recording a bill-pay handoff, running a portfolio goal on the closes a preview listed, or opening a client's month. It shows exactly what was proposed and runs only
// when the author confirms; the server previews the goal again and refuses a changed selection.
export function ProposalCard({ kind, proposal, closeId = "", outcome, live, onConfirm }: { kind: "approval" | "portfolio" | "open_close" | "bill_pay"; proposal: Record<string, unknown>; closeId?: string; outcome?: Outcome | null; live: boolean; onConfirm: (body: { reason: string; handed_off?: boolean }) => Promise<void> }) {
  const dates = useDates();
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  // A confirmed goal with closes still pending keeps running, a batch at a time, while this is the newest card.
  const [progress, setProgress] = useState<Record<string, number>>();
  const [stalled, setStalled] = useState("");
  const operation = kind === "portfolio" && outcome ? String(outcome.operation_id ?? "") : "";
  const pendingAtStart = kind === "portfolio" && outcome ? Number(outcome.pending ?? 0) : 0;
  useEffect(() => {
    if (!live || !operation || pendingAtStart === 0) return;
    let on = true;
    let timer: ReturnType<typeof setTimeout>;
    const run = async () => {
      try {
        const op = await workspaceRequest("/portfolio/run", { id: operation, confirmed: true, reason: String((proposal.args as Record<string, unknown> | undefined)?.reason ?? "") });
        if (!on) return;
        const counts: Record<string, number> = {};
        for (const item of (op.items ?? []) as { state: string }[]) counts[item.state] = (counts[item.state] ?? 0) + 1;
        setProgress(counts);
        if ((counts.pending ?? 0) > 0) timer = setTimeout(run, 3000);
      } catch (e) {
        if (on) setStalled(e instanceof Error ? e.message : "The goal could not continue; open Work to run the rest.");
      }
    };
    void run();
    return () => {
      on = false;
      clearTimeout(timer);
    };
  }, [live, operation, pendingAtStart]);
  const args = (proposal.args ?? {}) as Record<string, unknown>;
  const title = kind === "bill_pay" ? "Record a bill-pay handoff" : kind === "approval" ? "Ask a person to review a draft" : kind === "open_close" ? "Open a client's month" : `Run a goal across closes`;
  const month = `${String(args.entity ?? "")} · ${String(args.period ?? "")}`;
  const card = (body: React.ReactNode) => (
    <div className="assistant-card">
      <strong>{title}</strong>
      {body}
      {failure && <p role="alert">{failure}</p>}
    </div>
  );
  if (outcome) {
    if (kind === "bill_pay") {
      return card(<p>Recorded your handoff of {String(args.label ?? "")} for {String(args.expected_on ?? "")} · confirmed {dates.dateTime(outcome.at)}. Payment execution is confirmed only by your bill-pay tool and later ledger evidence. <Link to={`/dashboard/closes/${closeId}?tab=cash`}>Open the bill and forecast →</Link></p>);
    }
    if (kind === "portfolio") {
      const n = (key: string) => Number(progress ? (progress[key] ?? 0) : (outcome[key] ?? 0));
      const parts = [
        n("done") ? `${n("done")} started` : "",
        n("skipped") ? `${n("skipped")} skipped (nothing to do, or already handed over)` : "",
        n("failed") ? `${n("failed")} failed` : "",
        n("pending") ? `${n("pending")} still pending, continued from Work` : "",
      ].filter(Boolean);
      return card(
        <p>
          {String(outcome.action ?? "")} on {String(outcome.closes ?? "")} close{outcome.closes === 1 ? "" : "s"}: {parts.join(", ") || "no outcome recorded"} · confirmed {dates.dateTime(outcome.at)}.{" "}
          {stalled && <span role="alert">{stalled} </span>}
          <Link to="/dashboard">Work →</Link>
        </p>,
      );
    }
    if (kind === "open_close") {
      return card(
        <p>
          {outcome.created ? `Opened ${month}` : `${month} was already open`} · confirmed {dates.dateTime(outcome.at)}.{" "}
          <Link to={`/dashboard/closes/${String(outcome.close_id ?? "")}`}>Open the close →</Link>
        </p>,
      );
    }
    return card(
      <p>
        Review requested for draft #{String(args.workpaper_id ?? "")} · confirmed {dates.dateTime(outcome.at)}.{" "}
        <Link to={`/dashboard/closes/${closeId}/workpapers/${String(args.workpaper_id ?? "")}`}>Open the draft →</Link>
      </p>,
    );
  }
  if (!live) return card(<p>A newer card replaced this one.</p>);
  async function submit() {
    setBusy(true);
    setFailure("");
    try {
      await onConfirm({ reason: reason.trim(), ...(kind === "bill_pay" ? { handed_off: true } : {}) });
    } catch (e) {
      setFailure(e instanceof Error ? e.message : "The action could not be confirmed.");
      setConfirmed(false);
    } finally {
      setBusy(false);
    }
  }
  const closes = Array.isArray(args.closes) ? (args.closes as string[]) : [];
  const labels = (args.labels ?? {}) as Record<string, string>;
  return card(
    <>
      {kind === "bill_pay" ? (
        <>
          <p>{String(args.label ?? "")} · {money(String(args.balance ?? ""), String(args.currency ?? ""))} · due {String(args.due || "not recorded")} · current status: {String(args.current_status ?? "open").replaceAll("_", " ")}</p>
          <p>Proposed payment date: {String(args.expected_on ?? "")}. {String(args.note ?? "")}</p>
          <p>This records your external handoff and updates the base forecast timing. <Link to={`/dashboard/closes/${closeId}?tab=cash`}>Inspect the bill and cash plan →</Link></p>
        </>
      ) : kind === "approval" ? (
        <p>
          Draft #{String(args.workpaper_id ?? "")}: “{String(args.note ?? "")}”
        </p>
      ) : kind === "open_close" ? (
        <p>{month}</p>
      ) : (
        <>
          <p>“{String(args.text ?? "")}” on {closes.length} close{closes.length === 1 ? "" : "s"}, as previewed:</p>
          <ul>
            {closes.map((id) => (
              <li key={id}>
                <Link to={`/dashboard/closes/${id}`}>{labels[id] || `Close #${id}`}</Link>
              </li>
            ))}
          </ul>
          {/^\s*stop\b/i.test(String(args.text ?? "")) && (
            <label>
              Reason for stopping
              <textarea value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
            </label>
          )}
        </>
      )}
      <label>
        <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> {kind === "bill_pay" ? `I handed this bill to the client’s external bill-pay tool for ${String(args.expected_on ?? "")}.` : kind === "approval" ? "Ask for this review." : kind === "open_close" ? `Open this month for ${String(args.entity ?? "")}.` : "Run this goal on exactly these closes; the server refuses a changed selection."}
      </label>
      <p className="assistant-note">{kind === "open_close" ? "Confirming opens the month as the Clients page would; you must be assigned to the client." : "Confirming needs the reviewer role and runs the same checks as the page."}</p>
      <button type="button" className="primary" disabled={busy || !confirmed} onClick={() => void submit()}>
        {busy ? "Confirming…" : title}
      </button>
    </>,
  );
}
