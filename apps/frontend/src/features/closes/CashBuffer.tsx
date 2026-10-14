import { useQuery } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import type { CashBuffer } from "./CashPlan";
import { amount, workspaceRequest } from "./workspace";

// The client's minimum cash buffer: the one in force and the earlier versions, newest first, and a firm reviewer's form
// to agree another, in the client's home currency with its basis. Each agreement is kept; the cash plan compares its
// lowest balance with the newest, and a reviewed plan keeps the one it compared with.
export function CashBufferSettings({ closeId, currency }: { closeId: string; currency: string }) {
  const dates = useDates();
  const read = useQuery(workspaceQuery<{ buffers: CashBuffer[] }>(`/${closeId}/cash-buffer`));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  async function agree(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const values = Object.fromEntries(new FormData(form));
    setBusy(true);
    setError("");
    setSaved("");
    try {
      await workspaceRequest(`/${closeId}/cash-buffer`, { amount: values.amount, basis: values.basis, confirmed: values.confirmed === "on" });
      form.reset();
      setSaved("Agreed. The cash plan now compares its lowest balance with it.");
      void read.refetch();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The buffer could not be agreed.");
    } finally {
      setBusy(false);
    }
  }
  const buffers = read.data?.buffers ?? [];
  return (
    <section aria-label="Cash buffer">
      <p>The least book cash the client means to keep. The cash plan shows its lowest forecast balance's headroom over it, or the shortfall.</p>
      {read.error && <p role="alert">{read.error.message || "The cash buffer could not be read."}</p>}
      {read.data &&
        (buffers.length ? (
          <ul>
            {buffers.map((b, i) => (
              <li key={b.id}>
                <strong>{amount(b.amount, b.currency)}</strong> {i === 0 ? "(in force)" : "(earlier)"} · {b.basis} · {b.proposed_by === "agent" ? "the agent's proposal" : `set by ${b.proposed_by}`}, agreed by {b.approved_by} {dates.dateTime(b.approved_at)}
              </li>
            ))}
          </ul>
        ) : (
          <p>No cash buffer agreed. The cash plan shows the agent's proposal, which you may agree there.</p>
        ))}
      <form onSubmit={(e) => void agree(e)}>
        <label>
          Buffer ({currency})
          <input name="amount" required inputMode="decimal" pattern="[0-9]{1,13}(\.[0-9]{1,2})?" placeholder="25000.00" />
        </label>
        <label>
          Basis
          <input name="basis" required minLength={5} maxLength={1000} placeholder="Two payrolls and a month's rent, as agreed with the owner" />
        </label>
        <label>
          <input name="confirmed" type="checkbox" required /> Agree this as the client's minimum cash buffer
        </label>
        <button className="primary" disabled={busy}>
          {busy ? "Agreeing…" : "Agree buffer"}
        </button>
      </form>
      {saved && <p role="status">{saved}</p>}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
