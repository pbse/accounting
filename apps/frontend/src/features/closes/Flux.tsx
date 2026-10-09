import { useDates } from "../../data/time";
import { Link } from "react-router";
import { type FormEvent, useEffect, useState } from "react";
import { clientAsk, type CoverageStatus } from "./ask";
import { PasscodeNotice, SendRequestEmail } from "./Email";
import { workspaceRequest } from "./workspace";
import { RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Payee = { name: string; this_month: string; last_month: string };
type Change = {
  account_id: string;
  account: string;
  class: string;
  this_month: string;
  last_month: string;
  change: string;
  causes_unavailable?: string;
  new?: Payee[];
  missing?: Payee[];
  changed?: Payee[];
  unattributed?: { this_month: string; last_month: string };
  reconciled?: boolean;
  omitted?: number;
  questions?: string[];
};
type Month = { period: string; source: string; snapshot_id: string; captured_at: string; stale: boolean };
type Flux = { state: "ready" | "waiting"; reason?: string; currency?: string; this?: Month; last?: Month; changes?: Change[]; note?: string };

const kind = (c: Change) => (c.class === "Expense" ? "Expense" : c.class === "Revenue" ? "Income" : "Reported amount");

export function Flux({ closeId }: { closeId: string }) {
  const dates = useDates();
  const [flux, setFlux] = useState<Flux>();
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");
  const [coverage, setCoverage] = useState<CoverageStatus>();
  const [sent, setSent] = useState<{ id: string; recipient: string; link: string; passcode: string; queued?: boolean }>();
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/flux`)
      .then((data) => live && setFlux(data))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Changes are unavailable."));
    // Without coverage the questions can still be copied.
    workspaceRequest(`/${closeId}/coverage`).then((data) => live && setCoverage(data), () => {});
    return () => {
      live = false;
    };
  }, [closeId]);
  if (!flux) return error ? <p role="alert">{error}</p> : null;
  const questions = (flux.changes ?? []).flatMap((c) => c.questions ?? []);
  const ask: ReturnType<typeof clientAsk> = coverage ? clientAsk(flux, coverage) : { accounts: [], questions: [], asked: {}, withdraw: {} };
  const body = "Please help us complete this month's preparation:\n\n" + ask.questions.map((q, n) => `${n + 1}. ${q}`).join("\n\n");
  const reviewed = (f: Flux, a: typeof ask) => JSON.stringify([f.this?.source, f.this?.snapshot_id, f.last?.snapshot_id, a.accounts, a.questions]);
  async function approve(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    setBusy(true);
    setError("");
    try {
      // The draft was approved as shown, so both months and coverage are read again and must still give it.
      const [nowFlux, nowCoverage]: [Flux, CoverageStatus] = await Promise.all([workspaceRequest(`/${closeId}/flux`), workspaceRequest(`/${closeId}/coverage`)]);
      if (reviewed(nowFlux, clientAsk(nowFlux, nowCoverage)) !== reviewed(flux!, ask)) {
        setFlux(nowFlux);
        setCoverage(nowCoverage);
        throw new Error("The comparison or its coverage changed while you were reviewing. Check the updated draft.");
      }
      const recipient = String(form.get("recipient") ?? "").trim();
      const result = await workspaceRequest(`/${closeId}/coverage`, { action: "request", digest: nowCoverage.coverage.digest, accounts: ask.accounts, jobs: [], body: form.get("body"), recipient, confirmed: form.has("confirmed") });
      setSent({ id: result.id, recipient, link: `${window.location.origin}/request#${result.link_token}`, passcode: result.passcode ?? "" });
      workspaceRequest(`/${closeId}/coverage`).then(setCoverage, () => {});
    } catch (e) {
      setError(e instanceof Error ? e.message : "The request could not be approved.");
    } finally {
      setBusy(false);
    }
  }
  async function copy() {
    try {
      await navigator.clipboard.writeText(questions.map((q) => `- ${q}`).join("\n"));
      setCopied(`Copied ${questions.length} question${questions.length === 1 ? "" : "s"}.`);
    } catch {
      setCopied("The browser blocked the clipboard. Select the questions below and copy them.");
    }
  }
  return (
    <section aria-label="Changes from last month">
      <SectionHeader title="Changes from last month">
        {questions.length > 0 && (
          <button type="button" onClick={() => void copy()}>
            Copy client questions
          </button>
        )}
      </SectionHeader>
      {copied && <p role="status">{copied}</p>}
      {flux.state === "waiting" ? (
        <p className="empty">{flux.reason}</p>
      ) : (
        <>
          <p>
            Profit and Loss {flux.this!.period} (snapshot #{flux.this!.snapshot_id}, captured {dates.dateTime(flux.this!.captured_at)}) against{" "}
            {flux.last!.period} (snapshot #{flux.last!.snapshot_id}, captured {dates.dateTime(flux.last!.captured_at)}), largest movements first.{" "}
            {flux.note}
          </p>
          {(flux.this!.stale || flux.last!.stale) && <p role="status">A snapshot's source was replaced or its connection is inactive; capture again before relying on these figures.</p>}
          {flux.changes!.length === 0 && <p className="empty">No account moved between the two months.</p>}
          <RowList>
            {flux.changes!.map((c) => (
              <div className="row" key={c.account_id}>
                <div>
                  <strong>{c.account}</strong>{" "}
                  <span>
                    {kind(c)} {c.change.startsWith("-") ? "fell" : "rose"} {c.change.replace("-", "")} {flux.currency}, from {c.last_month} to {c.this_month}.
                  </span>
                  {c.causes_unavailable ? (
                    <p>
                      <small>{c.causes_unavailable}</small>
                    </p>
                  ) : (
                    <ul>
                      {c.new!.map((p) => (
                        <li key={"n" + p.name}>New this month: {p.name} {p.this_month}</li>
                      ))}
                      {c.missing!.map((p) => (
                        <li key={"m" + p.name}>Not in this month's capture: {p.name} ({p.last_month} last month)</li>
                      ))}
                      {c.changed!.map((p) => (
                        <li key={"c" + p.name}>
                          {p.name}: {p.last_month} → {p.this_month}
                        </li>
                      ))}
                      {c.unattributed && (c.unattributed.this_month !== "0.00" || c.unattributed.last_month !== "0.00") && (
                        <li>
                          Without a payee name: {c.unattributed.last_month} → {c.unattributed.this_month}
                        </li>
                      )}
                      {(c.omitted ?? 0) > 0 && <li>And {c.omitted} more payee{c.omitted === 1 ? "" : "s"} not shown.</li>}
                      {!c.reconciled && <li>These are possible drivers; the ledger's payees do not add up to the whole movement.</li>}
                    </ul>
                  )}
                  {(c.questions ?? []).map((q) => (
                    <p key={q}>
                      <small>{q}</small>
                    </p>
                  ))}
                  {ask.asked[c.account_id] && (
                    <p>
                      <small>
                        Already in request #{ask.asked[c.account_id]}. Send it or follow it up in <Link to="?tab=overview#coverage-work">Coverage</Link>.
                      </small>
                    </p>
                  )}
                  {ask.withdraw[c.account_id] && (
                    <p>
                      <small>
                        Request #{ask.withdraw[c.account_id]} was made on an earlier capture, so the client can no longer answer it. Withdraw it in{" "}
                        <Link to="?tab=overview#coverage-work">Coverage</Link> to ask again.
                      </small>
                    </p>
                  )}
                </div>
              </div>
            ))}
          </RowList>
          {sent && (
            <div>
              <p role="status">
                Request #{sent.id} to {sent.recipient} is approved. {sent.queued ? "Its email is queued; delivery shows in Coverage." : "Nothing has been sent yet."}
              </p>
              {sent.link && (
                <label>
                  Copy this private reply link now
                  <input readOnly value={sent.link} onFocus={(e) => e.currentTarget.select()} />
                </label>
              )}
              <PasscodeNotice passcode={sent.passcode} />
              {!sent.queued && <SendRequestEmail closeId={closeId} requestId={sent.id} recipient={sent.recipient} onQueued={(passcode) => setSent({ ...sent, link: "", passcode, queued: true })} />}
            </div>
          )}
          {ask.accounts.length > 0 && (
            <form onSubmit={(e) => void approve(e)}>
              <fieldset disabled={busy}>
                <legend>Client request I drafted from these changes</legend>
                <p>
                  Check the books first: anything recorded after this month's capture on {dates.dateTime(flux.this!.captured_at)} is not in these figures. Edit the
                  wording as you need. Nothing is sent until you approve it and then approve the email.
                </p>
                <label>
                  Request
                  <textarea name="body" key={body} defaultValue={body} rows={8} maxLength={20000} required />
                </label>
                <label>
                  Client recipient
                  <input name="recipient" type="email" maxLength={254} required defaultValue={coverage?.requests[0]?.recipient} />
                </label>
                <label>
                  <input name="confirmed" type="checkbox" required /> I reviewed the wording and recipient. The reply is filed against{" "}
                  {flux.changes!.filter((c) => ask.accounts.includes(c.account_id)).map((c) => c.account).join(", ")} in account coverage.
                </label>
                <button>Approve request and create private link</button>
              </fieldset>
            </form>
          )}
          {error && <p role="alert">{error}</p>}
        </>
      )}
    </section>
  );
}
