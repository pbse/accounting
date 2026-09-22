import type { PrepaidWorkpaper } from "./prepaid";
export type AccrualInput = {
 entity:string;source_reference:string;close_period:string;service_start:string;service_end:string;
 amount:string;booked_balance:string;booking_reference:string;estimation_basis:string;
 expense_account:string;liability_account:string;currency:"USD";basis:"accrual";
 method:"fixed_incurred_amount_v1";method_confirmed:boolean;booking_confirmed:boolean;examples_confirmed:boolean;reverse_next_month:boolean;
};
export type AccrualWorkpaper={status:"draft";input:AccrualInput;proposal:PrepaidWorkpaper["proposal"];reversal:PrepaidWorkpaper["proposal"]};
