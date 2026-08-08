/**
 * Shared API contract types.
 *
 * This package is consumed by the web app and, later, by a React Native app.
 * It must stay free of platform-specific imports (no DOM, no Node) so both
 * targets can use it unchanged.
 */

// ---------------------------------------------------------------------------
// Money
// ---------------------------------------------------------------------------

/**
 * Amounts cross the wire as decimal strings ("1234.56"), never as numbers.
 *
 * JSON numbers are IEEE 754 doubles, which cannot represent every decimal
 * exactly. Serialising money as a string keeps the value intact end to end;
 * the client parses it into minor units before doing arithmetic.
 */
export type MoneyString = string;

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

export type AccountType = "savings" | "checking" | "investment";

/**
 * An account as the API returns it, with its balances already resolved.
 *
 * The metadata lives in Postgres and the balances in TigerBeetle; the backend
 * joins them so the client sees one object instead of coordinating two calls.
 */
export interface Account {
  id: string;
  accountNumber: string;
  accountType: AccountType;
  currency: string;
  createdAt: string;

  /** Spendable right now: settled credits minus settled and reserved debits. */
  available: MoneyString;
  /** Settled balance, ignoring reservations. */
  posted: MoneyString;
  /** Reserved by operations awaiting confirmation. */
  pending: MoneyString;
}

export interface AccountBalance {
  accountId: string;
  accountNumber: string;
  /** Funds the user can actually spend right now. */
  available: MoneyString;
  /** Settled balance, ignoring reservations. */
  posted: MoneyString;
  /** Funds reserved by transfers awaiting confirmation. */
  pending: MoneyString;
  currency: string;
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

export type TransactionType =
  | "deposit"
  | "withdrawal"
  | "transfer"
  | "internal_transfer";

export type TransactionStatus = "pending" | "completed" | "voided";

export type TransactionDirection = "in" | "out";

export interface Transaction {
  id: string;
  type: TransactionType;
  status: TransactionStatus;
  amount: MoneyString;
  currency: string;
  /** "EXTERNAL" when funds come from outside the bank. */
  fromAccount: string;
  toAccount: string;
  description: string | null;
  /** Relative to the account being viewed. */
  direction: TransactionDirection;
  timestamp: string;
}

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

export interface User {
  id: string;
  email: string;
  fullName: string;
  createdAt: string;
}

export interface AuthSession {
  user: User;
  accessToken: string;
  /** Seconds until the access token expires. */
  expiresIn: number;
}

// ---------------------------------------------------------------------------
// Chat
// ---------------------------------------------------------------------------

export type ChatRole = "user" | "assistant" | "tool";

export type ConfirmationStatus =
  | "pending"
  | "confirmed"
  | "rejected"
  | "expired";

/**
 * An operation the assistant proposed that moves money.
 *
 * It is never executed on the model's say-so: the funds sit reserved in a
 * pending transfer until the user explicitly confirms or rejects it.
 */
export interface PendingOperation {
  transferId: string;
  operation: TransactionType;
  amount: MoneyString;
  currency: string;
  fromAccount: string;
  toAccount: string;
  description: string | null;
  status: ConfirmationStatus;
  /** When the reservation is released automatically. */
  expiresAt: string;
}

export interface ChatMessage {
  id: string;
  role: ChatRole;
  content: string;
  pendingOperation: PendingOperation | null;
  createdAt: string;
}

// ---------------------------------------------------------------------------
// API envelopes
// ---------------------------------------------------------------------------

/**
 * Error shape returned by every endpoint.
 *
 * `code` is a stable English identifier the client branches on; `message` is
 * Spanish prose meant for the user. Never branch on `message` — its wording
 * changes, the code does not.
 */
export interface ApiError {
  code: string;
  message: string;
  /** Per-field messages for form validation failures. */
  fields?: Record<string, string>;
}

export interface ApiErrorResponse {
  error: ApiError;
}

/** Cursor pagination. `nextCursor` is null on the last page. */
export interface Paginated<T> {
  items: T[];
  nextCursor: string | null;
  hasMore: boolean;
}
