export type PrepaidInput = {
  entity: string;
  source_reference: string;
  amount: string;
  service_start: string;
  service_end: string;
  close_period: string;
  currency: string;
  basis: "accrual";
  expense_account: string;
  prepaid_account: string;
  schedule?: "deferred_revenue" | "depreciation";
  salvage_value?: string;
  method: "equal_full_months_v1" | "actual_days_v1" | "declining_balance_v1";
  factor?: "2" | "1.5";
  convention?: "mid_month";
  examples_confirmed?: boolean;
  credit_amount?: string;
  credit_period?: string;
  credit_reference?: string;
  credits?: { amount: string; effective_period: string; recorded_period: string; reference: string }[];
  accountant_rule_reference?: string;
  cancellation_method?: "" | "full_refund_month_boundary_v1" | "no_refund_month_boundary_v1";
  cancellation_period?: string;
  cancellation_reference?: string;
  booked_balance?: string;
  booking_reference?: string;
  method_confirmed: boolean;
  booking_confirmed: boolean;
};

export type PrepaidWorkpaper = {
  carry_forward?: {
    close_id: string;
    paper_id: string;
    digest: string;
    review_id: string;
    period: string;
    opening_balance: string;
    source_ids: Record<string, string>;
  };
  status: "draft";
  input: PrepaidInput;
  schedule: { period: string; expense: string; remaining: string; credit?: string }[];
  period_expense: string;
  recognized_expense: string;
  prepaid_balance: string;
  proposal: null | {
    date: string;
    debit_account: string;
    credit_account: string;
    amount: string;
  };
};
