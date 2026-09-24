import type { PrepaidWorkpaper } from "./prepaid";
export type EntityType = "sole_proprietor" | "partnership" | "s_corp" | "c_corp";
export type OwnerInput = {
  entity: string; close_period: string; entity_type: EntityType; transaction_reference: string; transaction_date: string; payee: string; amount: string; currency?: string; ledger_reference: string;
  direction: "personal_paid_by_business" | "business_paid_personally"; purpose_status: "open" | "answered"; stated_purpose?: string; client_confirmation_reference?: string;
  treatment?: "distribution" | "due_from_owner" | "contribution" | "due_to_owner"; business_use_percent: number; booked_account?: string; owner_account?: string; partner?: string;
  dividend_declaration_reference?: string; repayment_reference?: string; reimbursement_reference?: string; not_booked_confirmed?: boolean; method: "owner_reclass_v1"; treatment_confirmed?: boolean;
};
export type OwnerWorkpaper = { status: "draft"; method: "owner_reclass_v1"; input: OwnerInput; portion: string; proposal: PrepaidWorkpaper["proposal"]; question?: string; blockers: string[] };
