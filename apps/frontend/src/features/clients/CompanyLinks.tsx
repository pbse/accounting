import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Card } from "@close/ui/components/Card";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { workspaceQuery } from "../../data/queries";
import { monthName, useDates } from "../../data/time";
import { workspaceRequest } from "../closes/workspace";

// Cross-source identity (docs/books-sources.md, History and takeover): a client whose months come from its files and
// from a live ledger company is compared across them only once a reviewer confirms both are the same company, mapping
// each of the file's accounts to the ledger's, once.

type Link = { id: string; accounts: Record<string, string>; map_stamp: string; current: boolean; reason: string; actor: string; at: string };
type Company = {
  ledger: "quickbooks" | "xero";
  company: string;
  name: string;
  period: string;
  link: Link | null;
  accounts: { id: string; name: string; class: string }[];
  proposal: Record<string, string>;
  unreadable?: string;
};
type LinksRead = { file_accounts: { key: string; name: string; class: string }[]; companies: Company[]; can_confirm: boolean; map: string };

const linksPath = (client: string) => `/client/company-links?name=${encodeURIComponent(client)}`;
const ledgerName = (c: Company) => `${c.ledger === "xero" ? "Xero" : "QuickBooks"}${c.name ? ` (${c.name})` : ""}`;

export function CompanyLinksCard({ client }: { client: string }) {
  const dates = useDates();
  const read = useQuery(workspaceQuery<LinksRead>(linksPath(client)));
  const [open, setOpen] = useState<Company | null>(null);
  const companies = read.data?.companies ?? [];
  if (read.error)
    return (
      <Card icon="link" tone="wait" title="Same company across sources">
        {read.error.message || "Whether the client's ledger companies are its files' company could not be read."}
      </Card>
    );
  // Only a client with file accounts and a ledger company has anything to link.
  if (!read.data || !read.data.file_accounts.length || !companies.length) return null;
  const unlinked = companies.filter((c) => !c.link?.current);
  return (
    <>
      <Card
        icon="link"
        tone={unlinked.length ? "wait" : "done"}
        title="Same company across sources"
        chip={<span className={unlinked.length ? "pill muted" : "pill"}>{unlinked.length ? "Not confirmed" : "Confirmed"}</span>}
      >
        {companies.map((c) => (
          <p key={`${c.ledger}:${c.company}`}>
            {ledgerName(c)}, captured through {monthName(c.period)}:{" "}
            {c.link?.current
              ? `confirmed as the files' company ${dates.dayMonth(c.link.at)} by ${c.link.actor}, ${Object.keys(c.link.accounts).length} accounts mapped.`
              : c.link
                ? "confirmed as the files' company, but the client's account map has changed since: review the mapping before its months are compared."
                : "not confirmed as the files' company, so its months and the months on files are not compared."}{" "}
            {read.data.can_confirm && !c.unreadable && (
              <button type="button" className="link" onClick={() => setOpen(c)}>
                {c.link ? "Review the mapping" : "Confirm and map the accounts"}
              </button>
            )}
            {c.unreadable && <span className="note"> {c.unreadable}</span>}
          </p>
        ))}
      </Card>
      {open && (
        <Dialog open onClose={() => setOpen(null)} title="Same company across sources" description={`Confirm ${ledgerName(open)} is the company the client's files are of, and map each file account to its account there.`}>
          <LinkForm client={client} company={open} fileAccounts={read.data.file_accounts} map={read.data.map} onClose={() => setOpen(null)} />
        </Dialog>
      )}
    </>
  );
}

function LinkForm({ client, company, fileAccounts, map, onClose }: { client: string; company: Company; fileAccounts: LinksRead["file_accounts"]; map: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [accounts, setAccounts] = useState<Record<string, string>>(() => ({ ...company.proposal, ...(company.link?.accounts ?? {}) }));
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const taken = new Set(Object.values(accounts).filter(Boolean));
  async function confirm() {
    setBusy(true);
    setError("");
    try {
      const mapped = Object.fromEntries(Object.entries(accounts).filter(([, id]) => id));
      await workspaceRequest(linksPath(client), { ledger: company.ledger, company: company.company, accounts: mapped, reason: reason.trim(), based_on: company.link?.id ?? "", map });
      await queryClient.invalidateQueries({ queryKey: ["workspace"] });
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The link could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div>
      <p className="note">Each account is proposed by its name. An account left unmapped is not compared; a comparison that needs one with a balance says so.</p>
      <table className="ks-preview ks-accounts">
        <thead>
          <tr>
            <th>Account in the files</th>
            <th>Class</th>
            <th>Account in {company.ledger === "xero" ? "Xero" : "QuickBooks"}</th>
          </tr>
        </thead>
        <tbody>
          {fileAccounts.map((a) => (
            <tr key={a.key}>
              <td>{a.name}</td>
              <td>{a.class || "—"}</td>
              <td>
                <select aria-label={`Account for ${a.name}`} value={accounts[a.key] ?? ""} onChange={(e) => setAccounts({ ...accounts, [a.key]: e.target.value })}>
                  <option value="">Not mapped</option>
                  {company.accounts.map((l) => (
                    <option key={l.id} value={l.id} disabled={l.id !== accounts[a.key] && taken.has(l.id)}>
                      {l.name}
                      {l.class ? ` (${l.class})` : ""}
                    </option>
                  ))}
                </select>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {error && <p role="alert">{error}</p>}
      <label>
        Reason
        <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
      </label>
      <DialogActions>
        <button type="button" onClick={onClose}>
          Close
        </button>
        <button type="button" className="primary" disabled={busy || !reason.trim()} onClick={() => void confirm()}>
          {busy ? "Confirming…" : "Confirm the same company"}
        </button>
      </DialogActions>
    </div>
  );
}
