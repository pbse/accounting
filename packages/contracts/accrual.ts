import type { PrepaidWorkpaper } from "./prepaid";
export type AccrualInput = {
 entity:string;source_reference:string;close_period:string;service_start:string;service_end:string;
 amount:string;booked_balance:string;booking_reference:string;estimation_basis:string;
 expense_account:string;liability_account:string;currency:string;basis:"accrual";
 method:"fixed_incurred_amount_v1"|"actual_invoice_true_up_v1"|"fixed_recurring_incurred_v1";method_confirmed:boolean;booking_confirmed:boolean;examples_confirmed:boolean;reverse_next_month:boolean;
 estimate_amount?:string;estimate_reference?:string;actual_reference?:string;accountant_rule_reference?:string;
};
export type AccrualWorkpaper={status:"draft";input:AccrualInput;proposal:PrepaidWorkpaper["proposal"];reversal:PrepaidWorkpaper["proposal"];estimate_variance?:string};
