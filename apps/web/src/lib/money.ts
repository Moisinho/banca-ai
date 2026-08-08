/**
 * Money helpers.
 *
 * Amounts cross the wire as decimal strings ("1234.56"), never as JSON numbers:
 * a JSON number is an IEEE 754 double and cannot hold every decimal exactly.
 * Arithmetic here works on integer cents for the same reason.
 */

/** Parses a decimal string into integer cents. */
export function toCents(amount: string): number {
  const [whole = "0", fraction = ""] = amount.replace(/,/g, "").split(".");
  const normalizedFraction = (fraction + "00").slice(0, 2);
  const sign = whole.startsWith("-") ? -1 : 1;

  const wholeCents = Math.abs(Number.parseInt(whole, 10) || 0) * 100;
  const fractionCents = Number.parseInt(normalizedFraction, 10) || 0;

  return sign * (wholeCents + fractionCents);
}

/** Renders integer cents back to a decimal string. */
export function fromCents(cents: number): string {
  const sign = cents < 0 ? "-" : "";
  const absolute = Math.abs(cents);
  return `${sign}${Math.floor(absolute / 100)}.${String(absolute % 100).padStart(2, "0")}`;
}

/**
 * Formats an amount for display, with thousands separators.
 *
 * Uses es-419 (Latin American Spanish) rather than es-ES: the seed data and the
 * product are Latin American, and the two locales group digits differently.
 */
export function formatAmount(amount: string, currency = "USD"): string {
  const cents = toCents(amount);

  return new Intl.NumberFormat("es-419", {
    style: "currency",
    currency,
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(cents / 100);
}

/** Formats an amount without the currency symbol, for tables. */
export function formatNumber(amount: string): string {
  const cents = toCents(amount);

  return new Intl.NumberFormat("es-419", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(cents / 100);
}

/**
 * Formats an amount with an explicit sign, for transaction lists.
 *
 * Outgoing money always shows a minus so the direction is readable without
 * relying on colour alone.
 */
export function formatSigned(amount: string, direction: "in" | "out"): string {
  const formatted = formatNumber(amount);
  return direction === "out" ? `−${formatted}` : `+${formatted}`;
}

/** Validates user input before sending it to the API. */
export function isValidAmount(input: string): boolean {
  const trimmed = input.trim();
  if (trimmed === "") return false;

  // Up to two decimals, matching what the backend accepts.
  if (!/^\d{1,9}([.,]\d{1,2})?$/.test(trimmed)) return false;

  return toCents(trimmed.replace(",", ".")) > 0;
}

/** Normalises user input to the format the API expects. */
export function normalizeAmount(input: string): string {
  return input.trim().replace(",", ".");
}

/** Formats an account number for display, keeping its grouping. */
export function formatAccountNumber(accountNumber: string): string {
  return accountNumber === "EXTERNAL" ? "Externa" : accountNumber;
}

/** Formats an ISO date as a short readable date. */
export function formatDate(iso: string): string {
  return new Intl.DateTimeFormat("es-419", {
    day: "2-digit",
    month: "short",
    year: "numeric",
  }).format(new Date(iso));
}

/** Formats an ISO date with time, for transaction detail. */
export function formatDateTime(iso: string): string {
  return new Intl.DateTimeFormat("es-419", {
    day: "2-digit",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

/** Spanish labels for transaction types. */
export const transactionTypeLabels: Record<string, string> = {
  deposit: "Depósito",
  withdrawal: "Retiro",
  transfer: "Transferencia",
  internal_transfer: "Entre cuentas",
};

/** Spanish labels for account types. */
export const accountTypeLabels: Record<string, string> = {
  savings: "Ahorros",
  checking: "Corriente",
  investment: "Inversión",
};
