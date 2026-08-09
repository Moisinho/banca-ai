import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";

import {
  Amount,
  Card,
  EmptyState,
  ErrorState,
  Pagination,
  Skeleton,
  Status,
} from "@/components/ui";
import { accounts as accountsApi } from "@/lib/api";
import { usePagedTransactions } from "@/lib/usePagedTransactions";
import {
  accountTypeLabels,
  formatDate,
  formatNumber,
  formatSigned,
  transactionTypeLabels,
} from "@/lib/money";
import type { Account, Transaction } from "@/types/api";
import { ChatPanel } from "@/features/chat/ChatPanel";
import { FlowRibbon } from "./FlowRibbon";

/** Movements per page in the dashboard list. */
const PAGE_SIZE = 5;

/**
 * How many movements feed the chart.
 *
 * The chart needs a span of history to be meaningful, so it is fetched
 * separately from the paginated list rather than being driven by whatever
 * page happens to be on screen.
 */
const CHART_SIZE = 60;

export function DashboardPage() {
  const [account, setAccount] = useState<Account | null>(null);
  const [chartData, setChartData] = useState<Transaction[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const paged = usePagedTransactions(account?.id ?? null, PAGE_SIZE);

  const loadAccount = useCallback(async () => {
    setError(null);

    try {
      const { accounts } = await accountsApi.list();

      if (accounts.length === 0) {
        setAccount(null);
        return;
      }

      const primary = accounts[0]!;
      setAccount(primary);

      const history = await accountsApi.transactions(primary.id, { limit: CHART_SIZE });
      setChartData(history.items);
    } catch {
      setError("No pudimos cargar sus cuentas. Revise su conexión.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadAccount();
  }, [loadAccount]);

  // After the assistant's operation is confirmed or rejected, the balance and
  // the movements both changed: reload the account and return to page one.
  const handleOperationResolved = useCallback(() => {
    void loadAccount();
    paged.reset();
  }, [loadAccount, paged]);

  if (loading) return <DashboardSkeleton />;
  if (error) return <ErrorState message={error} onRetry={() => void loadAccount()} />;

  if (!account) {
    return (
      <EmptyState
        title="Todavía no tiene cuentas"
        description="Cuando se abra su primera cuenta, aquí verá el resumen de su dinero."
      />
    );
  }

  return (
    // Single column on phones, chat beside the summary from large screens up:
    // the assistant is useful, but the balance is what people open the app for.
    //
    // min-w-0 on both columns is load-bearing: a grid item defaults to
    // min-width:auto, so unbreakable content inside it (a long account number,
    // the SVG's viewBox, a table) would otherwise force the column past the
    // viewport instead of shrinking to fit.
    <div className="grid min-w-0 gap-6 lg:grid-cols-[1fr_380px]">
      <div className="flex min-w-0 flex-col gap-6">
        <BalanceHeader account={account} />

        <FlowRibbon transactions={chartData} currency={account.currency} />

        <RecentActivity paged={paged} />
      </div>

      <Card
        padding={false}
        className="animate-rise min-w-0 lg:sticky lg:top-6 lg:h-[600px]"
      >
        <ChatPanel onOperationResolved={handleOperationResolved} />
      </Card>
    </div>
  );
}

function BalanceHeader({ account }: { account: Account }) {
  const hasPending = Number.parseFloat(account.pending) > 0;

  return (
    <div className="animate-rise">
      {/* flex-wrap: the tabular account number is wide enough on its own to
          push a narrow phone viewport into overflow if it can't drop to its
          own line. */}
      <div className="mb-1 flex flex-wrap items-center gap-x-2 gap-y-0.5">
        <p className="text-sm" style={{ color: "var(--text-muted)" }}>
          Cuenta {accountTypeLabels[account.accountType] ?? account.accountType}
        </p>
        <span className="amount text-xs" style={{ color: "var(--text-muted)" }}>
          {account.accountNumber}
        </span>
      </div>

      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <Amount size="xl">{formatNumber(account.available)}</Amount>
        <span className="text-sm" style={{ color: "var(--text-muted)" }}>
          {account.currency} disponibles
        </span>
      </div>

      {/* Reserved funds are only mentioned when they exist: naming them when
          they are zero raises a question the person did not have. */}
      {hasPending && (
        <p className="animate-fade-in mt-2 flex items-center gap-2 text-sm">
          <Status tone="warning">
            <span>
              <Amount size="sm">{formatNumber(account.pending)}</Amount> reservados por
              operaciones sin confirmar
            </span>
          </Status>
        </p>
      )}
    </div>
  );
}

function RecentActivity({ paged }: { paged: ReturnType<typeof usePagedTransactions> }) {
  const { items, page, hasMore, loading, error, next, previous } = paged;

  return (
    <Card padding={false} className="animate-rise">
      <div
        className="flex items-center justify-between border-b px-5 py-3.5"
        style={{ borderColor: "var(--border-subtle)" }}
      >
        <h2 className="text-sm font-medium" style={{ color: "var(--text-primary)" }}>
          Movimientos recientes
        </h2>
        <Link
          to="/movimientos"
          className="text-sm underline underline-offset-2 transition-opacity hover:opacity-70"
          style={{ color: "var(--text-accent)" }}
        >
          Ver todos
        </Link>
      </div>

      {loading ? (
        // Placeholder rows keep the card at a stable height while a page
        // loads, so the pagination controls do not jump under the cursor.
        <div className="flex flex-col gap-px p-5">
          {Array.from({ length: PAGE_SIZE }, (_, i) => (
            <Skeleton key={i} className="h-11 w-full" />
          ))}
        </div>
      ) : error ? (
        <p role="alert" className="px-5 py-6 text-sm" style={{ color: "var(--color-danger)" }}>
          {error}
        </p>
      ) : items.length === 0 ? (
        <EmptyState
          title="Sin movimientos todavía"
          description="Cuando realice su primera operación, aparecerá aquí."
        />
      ) : (
        <ul className="stagger">
          {items.map((tx) => (
            <li
              key={tx.id}
              className="interactive flex items-center justify-between gap-4 border-b px-5 py-3 last:border-b-0"
              style={{ borderColor: "var(--border-subtle)" }}
            >
              <div className="min-w-0">
                <p className="truncate text-sm" style={{ color: "var(--text-primary)" }}>
                  {tx.description || transactionTypeLabels[tx.type] || tx.type}
                </p>
                <p className="text-xs" style={{ color: "var(--text-muted)" }}>
                  {formatDate(tx.timestamp)} ·{" "}
                  {transactionTypeLabels[tx.type] ?? tx.type}
                </p>
              </div>

              <Amount
                size="sm"
                tone={tx.direction === "in" ? "positive" : "default"}
                className="shrink-0"
              >
                {formatSigned(tx.amount, tx.direction)}
              </Amount>
            </li>
          ))}
        </ul>
      )}

      <div className="border-t" style={{ borderColor: "var(--border-subtle)" }}>
        <Pagination
          page={page}
          canGoNext={hasMore}
          onPrevious={previous}
          onNext={next}
          loading={loading}
          label="Paginación de movimientos recientes"
        />
      </div>
    </Card>
  );
}

function DashboardSkeleton() {
  return (
    <div className="grid min-w-0 gap-6 lg:grid-cols-[1fr_380px]">
      <div className="flex min-w-0 flex-col gap-6">
        <div>
          <Skeleton className="mb-2 h-4 w-40" />
          <Skeleton className="h-10 w-56" />
        </div>
        <Skeleton className="h-[260px] w-full" />
        <Skeleton className="h-[300px] w-full" />
      </div>
      <Skeleton className="h-[300px] w-full lg:h-[600px]" />
    </div>
  );
}
