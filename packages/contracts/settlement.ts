export type SettlementInput = {
  entity: string;
  close_period: string;
  currency: "USD";
  bank_account: string;
  report_reference: string;
  bank_reference: string;
  report_csv: string;
  bank_csv: string;
  method_confirmed: boolean;
  coverage_confirmed: boolean;
  reviewed_deposits?: { payout_id: string; bank_id: string; reason: string }[];
  in_transit?: {
    payout_id: string;
    policy_reference: string;
    reason: string;
  }[];
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
  status: "matched" | "reviewed" | "in_transit" | "unmatched" | "ambiguous";
  deposit?: { id: string; date: string; amount: string };
  candidate_ids: string[];
  reason?: string;
  policy_reference?: string;
};
export type SettlementWorkpaper = {
  status: "draft";
  method: "stripe_automatic_payouts_v1";
  input: SettlementInput;
  payouts: SettlementPayout[];
  categories: SettlementCategory[];
  total: SettlementCategory;
  unassigned: string[];
  questions: string[];
  blockers: string[];
};
