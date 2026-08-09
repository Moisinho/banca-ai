import type {
  Account,
  AccountBalance,
  ApiErrorResponse,
  AuthSession,
  ChatMessage,
  Paginated,
  Transaction,
} from "@/types/api";

const API_URL = import.meta.env.VITE_API_URL ?? "http://localhost:8080";
const BASE = `${API_URL}/api/v1`;

/**
 * Error carrying the API's stable error code.
 *
 * Callers branch on `code`, never on `message` — the wording is Spanish prose
 * written for the user and changes freely; the code does not.
 */
export class ApiError extends Error {
  constructor(
    readonly code: string,
    message: string,
    readonly status: number,
    readonly fields?: Record<string, string>,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

/**
 * The access token lives in memory only, never in localStorage.
 *
 * Anything in localStorage is readable by any script on the page, so an XSS
 * would hand the attacker a valid token. The refresh token sits in an httpOnly
 * cookie the browser attaches automatically and JavaScript cannot read at all.
 */
let accessToken: string | null = null;

export function setAccessToken(token: string | null): void {
  accessToken = token;
}

export function getAccessToken(): string | null {
  return accessToken;
}

/** Notifies the app when the session dies so it can redirect to login. */
type SessionExpiredHandler = () => void;
let onSessionExpired: SessionExpiredHandler = () => {};

export function setSessionExpiredHandler(handler: SessionExpiredHandler): void {
  onSessionExpired = handler;
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  /** Set internally to stop a refresh failure from retrying forever. */
  skipRefresh?: boolean;
}

/**
 * In-flight refresh, shared across concurrent callers.
 *
 * Without this, a page that fires several requests at once on an expired token
 * would kick off several refreshes. Since refresh tokens rotate and are
 * single-use, the second one would look like a stolen token and the backend
 * would revoke the whole session.
 */
let refreshInFlight: Promise<boolean> | null = null;

/**
 * Full session from the most recent successful refresh.
 *
 * `restore()` needs the user object, not just the access token that
 * `refreshSession()` stores globally — this is how it gets it without firing
 * a second request.
 */
let lastSession: AuthSession | null = null;

async function refreshSession(): Promise<boolean> {
  refreshInFlight ??= (async () => {
    try {
      const response = await fetch(`${BASE}/auth/refresh`, {
        method: "POST",
        credentials: "include",
      });

      if (!response.ok) return false;

      const session = (await response.json()) as AuthSession;
      accessToken = session.accessToken;
      lastSession = session;
      return true;
    } catch {
      return false;
    } finally {
      // Cleared on the next tick so callers awaiting this promise all see the
      // same result before a new refresh can start.
      queueMicrotask(() => {
        refreshInFlight = null;
      });
    }
  })();

  return refreshInFlight;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = "GET", body, skipRefresh = false } = options;

  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (accessToken) headers.Authorization = `Bearer ${accessToken}`;

  const response = await fetch(`${BASE}${path}`, {
    method,
    headers,
    // Needed so the browser sends the refresh cookie.
    credentials: "include",
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  // An expired access token is routine, not an error: refresh once and retry.
  if (response.status === 401 && !skipRefresh) {
    const refreshed = await refreshSession();

    if (refreshed) {
      return request<T>(path, { ...options, skipRefresh: true });
    }

    accessToken = null;
    onSessionExpired();
    throw new ApiError("UNAUTHORIZED", "Su sesión expiró. Inicie sesión de nuevo.", 401);
  }

  if (!response.ok) {
    throw await toApiError(response);
  }

  // 204 has no body to parse.
  if (response.status === 204) return undefined as T;

  return (await response.json()) as T;
}

async function toApiError(response: Response): Promise<ApiError> {
  try {
    const body = (await response.json()) as ApiErrorResponse;
    return new ApiError(
      body.error.code,
      body.error.message,
      response.status,
      body.error.fields,
    );
  } catch {
    // A non-JSON error body (a proxy timeout, say) still has to surface
    // something the user can read.
    return new ApiError(
      "UNKNOWN_ERROR",
      "No se pudo completar la operación. Intentá de nuevo.",
      response.status,
    );
  }
}

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

export const auth = {
  async register(input: {
    email: string;
    password: string;
    fullName: string;
  }): Promise<AuthSession> {
    const session = await request<AuthSession>("/auth/register", {
      method: "POST",
      body: input,
      skipRefresh: true,
    });
    accessToken = session.accessToken;
    return session;
  },

  async login(input: { email: string; password: string }): Promise<AuthSession> {
    const session = await request<AuthSession>("/auth/login", {
      method: "POST",
      body: input,
      skipRefresh: true,
    });
    accessToken = session.accessToken;
    return session;
  },

  async logout(): Promise<void> {
    try {
      await request("/auth/logout", { method: "POST", skipRefresh: true });
    } finally {
      // The local session is dropped even if the server call fails: from the
      // user's point of view, logging out must always work.
      accessToken = null;
    }
  },

  /**
   * Restores a session from the refresh cookie on page load.
   *
   * Goes through the same `refreshSession` dedup as everything else, instead
   * of calling `/auth/refresh` on its own. In React 19's StrictMode dev
   * double-mount, the effect that calls this fires twice in a row; two
   * separate fetches here each consumed the (single-use, rotating) refresh
   * token, so the first succeeded and the second landed on an already-burned
   * token and looked like a stolen one — the session got logged out on
   * reload. Routing through the shared in-flight promise means both callers
   * await the same request instead of firing two.
   */
  async restore(): Promise<AuthSession | null> {
    const refreshed = await refreshSession();
    if (!refreshed) return null;

    // refreshSession() only stores the access token; the caller needs the
    // full session (with the user) to hydrate the UI.
    return lastSession;
  },
};

// ---------------------------------------------------------------------------
// Accounts
// ---------------------------------------------------------------------------

export const accounts = {
  list(): Promise<{ accounts: Account[] }> {
    return request("/accounts");
  },

  get(accountId: string): Promise<Account> {
    return request(`/accounts/${accountId}`);
  },

  balance(accountId: string): Promise<AccountBalance> {
    return request(`/accounts/${accountId}/balance`);
  },

  transactions(
    accountId: string,
    params: { limit?: number; cursor?: string; from?: string; to?: string } = {},
  ): Promise<Paginated<Transaction>> {
    const query = new URLSearchParams();
    if (params.limit) query.set("limit", String(params.limit));
    if (params.cursor) query.set("cursor", params.cursor);
    if (params.from) query.set("from", params.from);
    if (params.to) query.set("to", params.to);

    const suffix = query.toString() ? `?${query}` : "";
    return request(`/accounts/${accountId}/transactions${suffix}`);
  },

  /**
   * Downloads the statement.
   *
   * Uses fetch rather than a plain link because the endpoint needs the
   * Authorization header, which an <a href> cannot set.
   */
  async exportUrl(accountId: string, format: "csv" | "pdf"): Promise<Blob> {
    const response = await fetch(
      `${BASE}/accounts/${accountId}/transactions/export?format=${format}`,
      {
        headers: accessToken ? { Authorization: `Bearer ${accessToken}` } : {},
        credentials: "include",
      },
    );

    if (!response.ok) throw await toApiError(response);
    return response.blob();
  },
};

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

export const transactions = {
  deposit(input: {
    accountId: string;
    amount: string;
    description?: string;
  }): Promise<Transaction> {
    return request("/transactions/deposit", { method: "POST", body: input });
  },

  withdraw(input: {
    accountId: string;
    amount: string;
    description?: string;
  }): Promise<Transaction> {
    return request("/transactions/withdraw", { method: "POST", body: input });
  },

  transfer(input: {
    fromAccountId: string;
    toAccountNumber: string;
    amount: string;
    description?: string;
  }): Promise<Transaction> {
    return request("/transactions/transfer", { method: "POST", body: input });
  },
};

// ---------------------------------------------------------------------------
// Chat
// ---------------------------------------------------------------------------

export const chat = {
  send(message: string): Promise<ChatMessage> {
    return request("/chat/messages", { method: "POST", body: { message } });
  },

  /**
   * Loads a slice of the conversation.
   *
   * Without `before` it returns the newest messages. With `before` it returns
   * the ones older than that message, which is how the panel walks backwards
   * through a long thread as the person scrolls up.
   */
  history(
    limit = 50,
    before?: string,
  ): Promise<{ messages: ChatMessage[]; hasMore: boolean }> {
    const query = new URLSearchParams({ limit: String(limit) });
    if (before) query.set("before", before);

    return request(`/chat/messages?${query}`);
  },

  confirm(transferId: string): Promise<{ message: string }> {
    return request(`/chat/operations/${transferId}/confirm`, { method: "POST" });
  },

  reject(transferId: string): Promise<{ message: string }> {
    return request(`/chat/operations/${transferId}/reject`, { method: "POST" });
  },
};
