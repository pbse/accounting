export type ReconciliationInput = {
  entity: string;
  account: string;
  close_period: string;
  currency: "USD";
  statement_reference: string;
  ledger_reference: string;
  expected_pages: number;
  pages: number[];
  statement_opening: string;
  statement_closing: string;
  ledger_opening: string;
  ledger_closing: string;
  statement_csv: string;
  ledger_csv: string;
  policy_confirmed: boolean;
};
export type ReconciliationLine = { id: string; date: string; amount: string };
export type ReconciliationWorkpaper = {
  status: "draft";
  method: "exact_bank_month_v1";
  input: ReconciliationInput;
  matches: { statement_id: string; ledger_id: string; reason: string }[];
  unmatched_statement: ReconciliationLine[];
  unmatched_ledger: ReconciliationLine[];
  exceptions?: {
    side: "statement" | "ledger";
    id: string;
    kind: "ambiguous_exact" | "same_amount_other_date" | "same_date_other_amount" | "no_counterpart";
    candidate_ids: string[];
    candidate_count: number;
    question: string;
  }[];
  blockers: string[];
  statement_roll_forward_difference: string;
  ledger_roll_forward_difference: string;
  opening_difference: string;
  closing_difference: string;
};
