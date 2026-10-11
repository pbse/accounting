import type { PrepaidWorkpaper } from "./prepaid";
export type PayrollInput = {
  entity: string; close_period: string; currency: string; provider: string; source_reference: string; first_pay_date: string; last_pay_date: string;
  gross_wages: string; employee_taxes: string; employee_deductions: string; net_pay: string; employer_taxes: string; employer_contributions: string;
  provider_debits: string; ledger_debits: string; ledger_reference: string; wage_account: string; tax_expense_account: string; benefits_account?: string;
  clearing_account: string; liability_account: string; method: "payroll_summary_v1"; approved_confirmed?: boolean; not_booked_confirmed?: boolean;
};
export type PayrollWorkpaper = { status: "draft"; method: "payroll_summary_v1"; input: PayrollInput; total_cost: string; unpaid: string; proposals: NonNullable<PrepaidWorkpaper["proposal"]>[]; blockers: string[]; checks?: { name: string; state: "passed" | "failed" | "unavailable"; detail: string }[] };
