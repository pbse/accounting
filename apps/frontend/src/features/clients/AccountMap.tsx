import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { DialogActions } from "@close/ui/components/Dialog";
import { workspaceQuery } from "../../data/queries";
import { monthName, useDates } from "../../data/time";
import { workspaceRequest } from "../closes/workspace";

// A file's accounts mapped to the classes and types the month's books carry (docs/books-sources.md, How an upload
// becomes the month's books, step 4). The reader proposes each from the file's type column, its heading, its name or its
// number; a reviewer confirms, setting any it would map otherwise. The map is the client's: next month's file finds its
// accounts mapped already.

type Choice = { class: string; type: string };
type Proposal = Choice & { from: "" | "type" | "heading" | "name" | "number"; hint?: string };
type FileAccount = { key: string; number?: string; name: string; balance: string; ref: string; proposal: Proposal; mapped?: Choice & { name: string; number?: string } };
type AccountsRead = {
  accounts: FileAccount[];
  count: number;
  unmapped: number;
  recorded: boolean;
  unidentified: number;
  more: boolean;
  classes: Record<string, string[]>;
  on: boolean;
  can_confirm: boolean;
  reading: string;
  map: null | { id: string; stamp: string; period: string; reason: string; actor: string; at: string };
};

const classOrder = ["Asset", "Liability", "Equity", "Revenue", "Expense"];
const accountsPath = (client: string, id: string, offset = 0) => `/client/import/accounts?name=${encodeURIComponent(client)}&id=${encodeURIComponent(id)}&offset=${offset}`;

// fromText says where a proposal came from, so a person knows how far to trust it.
function fromText(p: Proposal) {
  switch (p.from) {
    case "type":
      return `From its type, “${p.hint}”`;
    case "heading":
      return `Under “${p.hint}”`;
    case "name":
      return "From its name";
    case "number":
      return "From its number: check it";
  }
  return "Nothing to go on";
}

export function AccountMap({ client, id, onClose }: { client: string; id: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const dates = useDates();
  const read = useQuery(workspaceQuery<AccountsRead>(accountsPath(client, id)));
  // Later pages, two hundred accounts at a time, and what the reviewer has set.
  const [later, setLater] = useState<{ accounts: FileAccount[]; more: boolean } | undefined>(undefined);
  const [set, setSet] = useState<Record<string, Choice>>({});
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  if (!read.data) return read.error ? <p role="alert">{read.error.message || "The accounts could not be read."}</p> : <p role="status">Reading the accounts…</p>;
  const data = read.data;
  const accounts = [...data.accounts, ...(later?.accounts ?? [])];
  const more = later ? later.more : data.more;
  async function loadMore() {
    setError("");
    try {
      const page: AccountsRead = await workspaceRequest(accountsPath(client, id, accounts.length));
      setLater({ accounts: [...(later?.accounts ?? []), ...page.accounts], more: page.more });
    } catch (e) {
      setError(e instanceof Error ? e.message : "More accounts could not be read.");
    }
  }
  async function confirm() {
    setBusy(true);
    setError("");
    try {
      const out: { unmapped: number } = await workspaceRequest(accountsPath(client, id), { accept: true, accounts: set, reason: reason.trim(), based_on: data.map?.stamp ?? "", reading: data.reading });
      setSet({});
      setReason("");
      setLater(undefined);
      setDone(out.unmapped ? `Confirmed. ${out.unmapped} account${out.unmapped === 1 ? "" : "s"} had nothing to go on and still need a class.` : "Confirmed: every account is mapped.");
      await queryClient.invalidateQueries({ queryKey: ["workspace"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : "The map could not be confirmed.");
    } finally {
      setBusy(false);
    }
  }
  const choose = (a: FileAccount, choice: Choice) => setSet({ ...set, [a.key]: choice });
  const editable = data.can_confirm;
  return (
    <div className="ks-import">
      <p>
        {data.count} account{data.count === 1 ? "" : "s"}: {data.count - data.unmapped} mapped, {data.unmapped} to map.
        {data.map && ` The map was last confirmed ${dates.dayMonth(data.map.at)} by ${data.map.actor}, on ${monthName(data.map.period)}'s file: ${data.map.reason}`}
      </p>
      {data.unidentified > 0 && (
        <p role="alert">
          {data.unidentified} row{data.unidentified === 1 ? "" : "s"} with an amount name{data.unidentified === 1 ? "s" : ""} no account: read the file with its account column, or mark the row, before it can be mapped.
        </p>
      )}
      {editable && data.unmapped > 0 && <p className="note">Confirming accepts what is proposed for every account not mapped yet, on every page, with any class or type you set here.</p>}
      {editable && !data.unmapped && !data.recorded && <p className="note">Every account is mapped already. Confirm the map for this file to record that it names them, so they stay mapped if an earlier file is deleted.</p>}
      <table className="ks-preview">
        <thead>
          <tr>
            <th>Account</th>
            <th>Balance</th>
            <th>Class</th>
            <th>Type</th>
            <th>Read from</th>
          </tr>
        </thead>
        <tbody>
          {accounts.map((a) => {
            const shown = set[a.key] ?? a.mapped ?? (a.proposal.class ? a.proposal : undefined);
            return (
              <tr key={a.key}>
                <td>{a.number ? `${a.number} ${a.name}` : a.name}</td>
                <td>{a.balance}</td>
                <td>
                  {editable ? (
                    <select aria-label={`Class of ${a.name}`} value={shown?.class ?? ""} onChange={(e) => choose(a, { class: e.target.value, type: data.classes[e.target.value]?.[0] ?? "" })}>
                      {!shown && <option value="">Choose</option>}
                      {classOrder.map((c) => (
                        <option key={c} value={c}>
                          {c}
                        </option>
                      ))}
                    </select>
                  ) : (
                    (shown?.class ?? "—")
                  )}
                </td>
                <td>
                  {editable && shown ? (
                    <select aria-label={`Type of ${a.name}`} value={shown.type} onChange={(e) => choose(a, { class: shown.class, type: e.target.value })}>
                      {(data.classes[shown.class] ?? []).map((t) => (
                        <option key={t} value={t}>
                          {t}
                        </option>
                      ))}
                    </select>
                  ) : (
                    (shown?.type ?? "—")
                  )}
                </td>
                <td>{set[a.key] ? <span className="pill neutral">Set here</span> : a.mapped ? <span className="pill">Mapped</span> : <span className={a.proposal.class ? "pill muted" : "pill danger"}>{fromText(a.proposal)}</span>}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {more && (
        <button type="button" className="link" onClick={() => void loadMore()}>
          Show more accounts
        </button>
      )}
      {done && <p role="status">{done}</p>}
      {error && <p role="alert">{error}</p>}
      {!data.on && <p className="note">File imports are switched off for your firm: the map can be read, not changed.</p>}
      {editable && (
        <label>
          Reason
          <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
        </label>
      )}
      <DialogActions>
        <button type="button" onClick={onClose}>
          Close
        </button>
        {editable && (
          <button type="button" className="primary" disabled={busy || !reason.trim() || (data.recorded && !Object.keys(set).length)} onClick={() => void confirm()}>
            {busy ? "Confirming…" : "Confirm the map"}
          </button>
        )}
      </DialogActions>
    </div>
  );
}
