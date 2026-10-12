import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import { workspaceRequest } from "./workspace";

// Current cash on files (docs/books-sources.md, Open items and cash dates): a reviewer records a files month's cash at a
// recent date, as book balances, or as bank balances with the outstanding items that bridge them to book cash. The
// newest opens the cash plan from its date, the open items then wanted as of the same date.

type Item = { account: string; description: string; amount: string; evidence: string };
type Observation = { id: string; as_of: string; kind: "book" | "bank"; accounts: { name: string; balance: string }[]; bridge: Item[]; book_cash: string; note: string; actor: string; created_at: string };
type Read = { on: boolean; observations: Observation[]; banks: string[]; from: string; can_record: boolean };

export function CashObservations({ closeId }: { closeId: string }) {
  const queryClient = useQueryClient();
  const dates = useDates();
  const path = `/${closeId}/cash-observations`;
  const read = useQuery(workspaceQuery<Read>(path));
  const [kind, setKind] = useState<"book" | "bank">("book");
  const [items, setItems] = useState<Item[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (!read.data?.on) return null;
  const { observations, banks, from, can_record } = read.data;
  async function record(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const element = event.currentTarget;
    const form = new FormData(element);
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(path, {
        kind,
        as_of: form.get("as_of"),
        accounts: banks.map((name, i) => ({ name, balance: String(form.get(`balance-${i}`) ?? "").trim() })).filter((a) => a.balance !== ""),
        bridge: kind === "bank" ? items : [],
        note: form.get("note"),
        confirmed: true,
      });
      element.reset();
      setItems([]);
      await queryClient.invalidateQueries({ queryKey: ["workspace"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : "The cash could not be recorded.");
    } finally {
      setBusy(false);
    }
  }
  const change = (i: number, field: keyof Item, value: string) => setItems(items.map((item, n) => (n === i ? { ...item, [field]: value } : item)));
  return (
    <section aria-label="Cash at a date">
      <h3>Cash at a date</h3>
      <p className="note">
        The plan opens from the month's file, as of {dates.calendarDate(from)}, until cash at a later date is recorded here; its open items are then wanted as of
        that date too. This is never the month-end bank reconciliation.
      </p>
      {observations.length > 0 && (
        <ul>
          {observations.map((o) => (
            <li key={o.id}>
              {o.kind === "bank" ? "Bank balances" : "Book balances"} as of {dates.calendarDate(o.as_of)}: book cash {o.book_cash}
              {o.bridge.length > 0 && `, bridged by ${o.bridge.length} outstanding item${o.bridge.length === 1 ? "" : "s"}`}, recorded by {o.actor}. <small>{o.note}</small>
            </li>
          ))}
        </ul>
      )}
      {error && <p role="alert">{error}</p>}
      {can_record && (
        <form onSubmit={record}>
          <fieldset disabled={busy}>
            <label>
              The balances are
              <select value={kind} onChange={(e) => setKind(e.target.value as "book" | "bank")}>
                <option value="book">Book balances (a Trial Balance or cash balances run to the date)</option>
                <option value="bank">Bank balances, bridged to book cash</option>
              </select>
            </label>
            <label>
              As of
              <input type="date" name="as_of" min={from} required />
            </label>
            {banks.map((name, i) => (
              <label key={name}>
                {name}
                <input name={`balance-${i}`} inputMode="decimal" placeholder="Balance" />
              </label>
            ))}
            {kind === "bank" && (
              <>
                {items.map((item, i) => (
                  <fieldset key={i}>
                    <legend>Outstanding item {i + 1}</legend>
                    <select value={item.account} onChange={(e) => change(i, "account", e.target.value)} required>
                      <option value="">Account</option>
                      {banks.map((name) => (
                        <option key={name} value={name}>
                          {name}
                        </option>
                      ))}
                    </select>
                    <input placeholder="What it is (check 1043 to Landlord)" value={item.description} onChange={(e) => change(i, "description", e.target.value)} required />
                    <input placeholder="Amount: a deposit in transit positive, an uncleared check negative" inputMode="decimal" value={item.amount} onChange={(e) => change(i, "amount", e.target.value)} required />
                    <input placeholder="Evidence (the statement or register showing it)" value={item.evidence} onChange={(e) => change(i, "evidence", e.target.value)} required />
                  </fieldset>
                ))}
                <button type="button" onClick={() => setItems([...items, { account: "", description: "", amount: "", evidence: "" }])}>
                  Add an outstanding item
                </button>
              </>
            )}
            <label>
              Where the balances come from
              <textarea name="note" maxLength={2000} required />
            </label>
            <button type="submit" className="primary">
              {busy ? "Recording…" : "Record the cash"}
            </button>
          </fieldset>
        </form>
      )}
    </section>
  );
}
