type Month = { source?: string; snapshot_id: string; stale: boolean };
type Changes = { this?: Month; last?: Month; changes?: { account_id: string; questions?: string[] }[] };
export type CoverageStatus = {
  coverage: { snapshot_id: string; stale: boolean; digest: string; items: { account_id: string; state: string }[] };
  requests: { id: string; recipient: string; scope?: { snapshot_id?: string; bindings?: { account_id: string }[] }; link?: { used_at?: string; revoked_at?: string; expires_at: string } }[];
};

// clientAsk is what Changes from last month can ask the client through the capture's account coverage: the open
// accounts whose questions go in a new request; the accounts an earlier request already covers (its link not withdrawn,
// and answered, or open on this capture), with that request; and those whose open request was made on an earlier capture,
// which the client can no longer answer and must be withdrawn before asking again. Nothing is asked when either month is
// out of date or the comparison is not of the coverage's QuickBooks capture.
export function clientAsk(flux: Changes, status: CoverageStatus, now = Date.now()) {
  const accounts: string[] = [];
  const questions: string[] = [];
  const asked: Record<string, string> = {};
  const withdraw: Record<string, string> = {};
  const current = flux.this?.source === "quickbooks" && flux.this.snapshot_id === status.coverage.snapshot_id && !flux.this.stale && !flux.last?.stale && !status.coverage.stale;
  for (const c of flux.changes ?? []) {
    if (!c.questions?.length) continue;
    const earlier = status.requests.find((q) => q.link && !q.link.revoked_at && (q.link.used_at || Date.parse(q.link.expires_at) > now) && q.scope?.bindings?.some((b) => b.account_id === c.account_id));
    if (earlier && (earlier.link?.used_at || earlier.scope?.snapshot_id === status.coverage.snapshot_id)) asked[c.account_id] = earlier.id;
    else if (earlier) withdraw[c.account_id] = earlier.id;
    else if (current && status.coverage.items.some((i) => i.account_id === c.account_id && i.state !== "resolved" && i.state !== "excluded")) {
      accounts.push(c.account_id);
      questions.push(...c.questions);
    }
  }
  return { accounts, questions, asked, withdraw };
}
