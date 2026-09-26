export type SettlementMethod = "stripe_automatic_payouts_v1" | "stripe_payouts_v1";
export type SettlementInput = {
  method?: SettlementMethod;
  entity: string;
  close_period: string;
  currency: string;
  bank_account: string;
  report_reference: string;
  bank_reference: string;
  report_csv: string;
  bank_csv: string;
  activity_reference?: string;
  activity_csv?: string;
  method_confirmed: boolean;
  coverage_confirmed: boolean;
  reviewed_deposits?: { payout_id: string; bank_id: string; reason: string }[];
  in_transit?: {
    payout_id: string;
    policy_reference: string;
    reason: string;
  }[];
  fee_entry?: {
    fee_account: string;
    deposit_account: string;
    booked_fees: string;
    rule_reference: string;
  };
  refund_entry?: { refund_account: string; booked_refunds: string; rule_reference: string };
  revenue_entry?: { revenue_account: string; booked_revenue: string; rule_reference: string; clearing_confirmed: boolean };
};
export type SettlementCategory = {
  category: string;
  count: number;
  gross: string;
  fee: string;
  net: string;
};
export type SettlementPayout = {
  id: string;
  expected_date: string;
  count: number;
  gross: string;
  fee: string;
  net: string;
  categories: SettlementCategory[];
  status: "matched" | "reviewed" | "in_transit" | "unmatched" | "ambiguous" | "failed";
  deposit?: { id: string; date: string; amount: string };
  candidate_ids: string[];
  reason?: string;
  policy_reference?: string;
};
export type SettlementWorkpaper = {
  status: "draft";
  method: SettlementMethod;
  input: SettlementInput;
  payouts: SettlementPayout[];
  categories: SettlementCategory[];
  total: SettlementCategory;
  unassigned: string[];
  questions: string[];
  blockers: string[];
  fees?: string;
  proposal?: { date: string; debit_account: string; credit_account: string; amount: string };
  refunds?: string;
  revenue?: string;
  refund_proposal?: { date: string; debit_account: string; credit_account: string; amount: string };
  revenue_proposal?: { date: string; debit_account: string; credit_account: string; amount: string };
  roll_forward?: { activity: string; paid_out: string; returned: string; change: string };
};
