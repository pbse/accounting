import type { PrepaidWorkpaper } from "./prepaid";
export type LoanPayment = { date: string; principal: string; interest: string };
export type LoanInput = {
  entity: string; close_period: string; lender: string; source_reference: string; agreement_reference: string; currency: "USD";
  annual_rate: string; day_count: "actual_360" | "actual_365" | "30_360_bond" | "30e_360";
  opening_principal: string; interest_paid_through: string; payments: LoanPayment[]; closing_principal: string; book_balance: string;
  loan_account: string; interest_account: string;
  accrue_interest: boolean; accrued_interest_account?: string; booked_accrued_interest?: string;
  reclass_current_portion: boolean; principal_due_next_12_months?: string; schedule_reference?: string; current_portion_account?: string; booked_current_portion?: string;
  method: "lender_statement_v1"; method_confirmed: boolean; rate_confirmed: boolean; booking_confirmed: boolean;
};
export type LoanWorkpaper = {
  status: "draft"; method: "lender_statement_v1"; input: LoanInput;
  schedule: { date: string; principal_before: string; days: number; principal: string; interest: string; expected_interest: string; difference: string }[];
  accrued_interest?: { from: string; to: string; days: number; amount: string };
  book_difference: string; proposals: NonNullable<PrepaidWorkpaper["proposal"]>[]; questions: string[]; blockers: string[];
};
