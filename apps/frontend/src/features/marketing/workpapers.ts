// Three illustrative bank reconciliations the agents prepare in turn on the home page. Every set ties: each listed
// item explains part of the difference, which reaches 0.00. Each row is label, source tag, source title, source text
// and amount.
export type Workpaper = {
  head: string;
  lines: number;
  ajeRef: string;
  aje: string;
  signer: string;
  meta: string;
  prepared: string;
  diff: [null, number, number, number, number];
  rows: [string, string, string, string, string][];
};

export const WORKPAPERS: Workpaper[] = [
  {
    head: "Harbor Street Dental · Operating account · September 2026",
    lines: 214,
    ajeRef: "AJE-3",
    aje: "Debit 1010 Operating 632.60, credit 1200 Accounts receivable 632.60.",
    signer: "M. Alvarez",
    meta: "M. Alvarez, CPA · 2 Oct",
    prepared: "Four agents · 1 Oct, 06:12",
    diff: [null, 4212.6, 1812.6, 632.6, 0],
    rows: [
      ["Balance per bank statement, 30 Sep", "Statement, p. 4", "September statement · page 4", "Closing balance 48,212.60, captured 1 Oct at 06:12.", "48,212.60"],
      ["Balance per ledger, 1010 Operating", "QuickBooks", "QuickBooks Online · trial balance", "1010 Operating 44,000.00 at 30 Sep. Agents read the ledger and change nothing.", "44,000.00"],
      ["Check 1043, outstanding at 30 Sep", "Cleared 2 Oct", "October statement · 2 Oct", "CHECK 1043 · 2,400.00, issued 28 Sep to Lakeside Dental Supply.", "(2,400.00)"],
      ["Check 1047, outstanding at 30 Sep", "Not yet cleared", "Ledger · 29 Sep", "Check 1047 to Brightline Software · 1,180.00. Not on the September statement.", "(1,180.00)"],
      ["Insurance payment, not yet in the ledger", "Statement, 30 Sep", "September statement · 30 Sep", "ACH CREDIT NORTHSTAR DENTAL BENEFITS · 632.60. Paid against claims already billed; no ledger entry yet.", "(632.60)"],
    ],
  },
  {
    head: "Alder & Finch Architects · Operating account · September 2026",
    lines: 168,
    ajeRef: "AJE-2",
    aje: "Debit 1000 Operating 381.25, credit 1250 Card clearing 381.25.",
    signer: "J. Okafor",
    meta: "J. Okafor, CPA · 2 Oct",
    prepared: "Four agents · 1 Oct, 06:24",
    diff: [null, 4456.25, 1256.25, 381.25, 0],
    rows: [
      ["Balance per bank statement, 30 Sep", "Statement, p. 3", "September statement · page 3", "Closing balance 91,406.25, captured 1 Oct at 06:24.", "91,406.25"],
      ["Balance per ledger, 1000 Operating", "QuickBooks", "QuickBooks Online · trial balance", "1000 Operating 86,950.00 at 30 Sep. Agents read the ledger and change nothing.", "86,950.00"],
      ["Check 2210, outstanding at 30 Sep", "Cleared 1 Oct", "October statement · 1 Oct", "CHECK 2210 · 3,200.00, issued 29 Sep to Marlowe Surveying.", "(3,200.00)"],
      ["Check 2214, outstanding at 30 Sep", "Not yet cleared", "Ledger · 30 Sep", "Check 2214 to Keystone Print · 875.00. Not on the September statement.", "(875.00)"],
      ["Card settlement, not yet in the ledger", "Statement, 30 Sep", "September statement · 30 Sep", "ACH CREDIT CARD SETTLEMENT · 381.25. Client fees paid by card; no ledger entry yet.", "(381.25)"],
    ],
  },
  {
    head: "Northgate Logistics · Payroll account · September 2026",
    lines: 96,
    ajeRef: "AJE-5",
    aje: "Debit 1020 Payroll 475.40, credit 2310 Payroll taxes payable 475.40.",
    signer: "R. Chen",
    meta: "R. Chen, CPA · 3 Oct",
    prepared: "Four agents · 1 Oct, 06:35",
    diff: [null, 6215.4, 2035.4, 475.4, 0],
    rows: [
      ["Balance per bank statement, 30 Sep", "Statement, p. 2", "September statement · page 2", "Closing balance 38,120.40, captured 1 Oct at 06:35.", "38,120.40"],
      ["Balance per ledger, 1020 Payroll", "QuickBooks", "QuickBooks Online · trial balance", "1020 Payroll 31,905.00 at 30 Sep. Agents read the ledger and change nothing.", "31,905.00"],
      ["Check 5521, outstanding at 30 Sep", "Cleared 3 Oct", "October statement · 3 Oct", "CHECK 5521 · 4,180.00, final pay issued 30 Sep.", "(4,180.00)"],
      ["Check 5523, outstanding at 30 Sep", "Not yet cleared", "Ledger · 30 Sep", "Check 5523 · 1,560.00, a payroll correction. Not on the September statement.", "(1,560.00)"],
      ["Payroll tax refund, not yet in the ledger", "Statement, 29 Sep", "September statement · 29 Sep", "ACH CREDIT IRS TREAS 310 TAX REF · 475.40. Refund of an overpaid deposit; no ledger entry yet.", "(475.40)"],
    ],
  },
];
