// Synthetic reference only. Live capability contracts arrive with their implementation.
export type ReferenceWorkpaper = {
  mode: "synthetic";
  entity: string;
  period: string;
  basis: string;
  currency: "USD";
  invoice: {
    id: string;
    page: number;
    amount: string;
    service_start: string;
    service_end: string;
  };
  policy: { id: string; version: number; method: string };
  ledger: {
    id: string;
    revision: string;
    account: string;
    booked_amount: string;
  };
  schedule: { period: string; expense: string; remaining: string }[];
  proposal: {
    debit_account: string;
    credit_account: string;
    amount: string;
    status: "prepared";
  };
};
