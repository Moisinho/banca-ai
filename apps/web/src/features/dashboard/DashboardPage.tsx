import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { Amount, Card, EmptyState, ErrorState, Skeleton, Status } from "@/components/ui";
import { accounts as accountsApi } from "@/lib/api";
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

export function DashboardPage() {
  const [account, setAccount] = useState<Account | null>(null);
  const [transactions, setTransactions] = useState<Transaction[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);

    try {
      const { accounts } = await accountsApi.list();

      if (accounts.length === 0) {
        setAccount(null);
        setTransactions([]);
        return;
      }

      const primary = accounts[0]!;
      setAccount(primary);

      const history = await accountsApi.transactions(primary.id, { limit: 50 });
      setTransactions(history.items);
    } catch {
      setError("No pudimos cargar tus cuentas. Revisá tu conexión.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading) return <DashboardSkeleton />;
  if (error) return <ErrorState message={error} onRetry={() => void load()} />;

  if (!account) {
    return (
      <EmptyState
        title="Todavía no tenés cuentas"
        description="Cuando se abra tu primera cuenta, vas a ver acá el resumen de tu dinero."
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

        <FlowRibbon transactions={transactions} currency={account.currency} />

        <RecentActivity transactions={transactions} />
      </div>

      <Card padding={false} className="min-w-0 lg:sticky lg:top-6 lg:h-[600px]">
        <ChatPanel onOperationResolved={() => void load()} />
      </Card>
    </div>
  );
}

function BalanceHeader({ account }: { account: Account }) {
  const hasPending = Number.parseFloat(account.pending) > 0;

  return (
    <div>
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
        <p className="mt-2 flex items-center gap-2 text-sm">
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

function RecentActivity({ transactions }: { transactions: Transaction[] }) {
  const recent = transactions.slice(0, 8);

  return (
    <Card padding={false}>
      <div
        className="flex items-center justify-between border-b px-5 py-3.5"
        style={{ borderColor: "var(--border-subtle)" }}
      >
        <h2 className="text-sm font-medium" style={{ color: "var(--text-primary)" }}>
          Movimientos recientes
        </h2>
        <Link
          to="/movimientos"
          className="text-sm underline underline-offset-2"
          style={{ color: "var(--text-accent)" }}
        >
          Ver todos
        </Link>
      </div>

      {recent.length === 0 ? (
        <EmptyState
          title="Sin movimientos todavía"
          description="Cuando hagas tu primera operación, va a aparecer acá."
        />
      ) : (
        <ul>
          {recent.map((tx) => (
            <li
              key={tx.id}
              className="flex items-center justify-between gap-4 border-b px-5 py-3 last:border-b-0"
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
        <Skeleton className="h-[200px] w-full" />
        <Skeleton className="h-[300px] w-full" />
      </div>
      <Skeleton className="h-[300px] w-full lg:h-[600px]" />
    </div>
  );
}
