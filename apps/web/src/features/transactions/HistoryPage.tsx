import { useCallback, useEffect, useState } from "react";

import {
  Amount,
  Button,
  Card,
  EmptyState,
  ErrorState,
  Pagination,
  Skeleton,
} from "@/components/ui";
import { accounts as accountsApi } from "@/lib/api";
import { usePagedTransactions } from "@/lib/usePagedTransactions";
import {
  formatAccountNumber,
  formatDateTime,
  formatSigned,
  transactionTypeLabels,
} from "@/lib/money";
import type { Account } from "@/types/api";

/**
 * Rows per page.
 *
 * Only this many transactions exist in the DOM at any moment: moving between
 * pages refetches instead of appending, so the page stays the same weight
 * whether the account has fifty movements or fifty thousand.
 */
const PAGE_SIZE = 10;

export function HistoryPage() {
  const [account, setAccount] = useState<Account | null>(null);
  const [loadingAccount, setLoadingAccount] = useState(true);
  const [accountError, setAccountError] = useState<string | null>(null);
  const [exporting, setExporting] = useState<"csv" | "pdf" | null>(null);
  const [exportError, setExportError] = useState<string | null>(null);

  const paged = usePagedTransactions(account?.id ?? null, PAGE_SIZE);

  const loadAccount = useCallback(async () => {
    setAccountError(null);
    setLoadingAccount(true);

    try {
      const { accounts } = await accountsApi.list();
      setAccount(accounts[0] ?? null);
    } catch {
      setAccountError("No pudimos cargar sus movimientos. Revise su conexión.");
    } finally {
      setLoadingAccount(false);
    }
  }, []);

  useEffect(() => {
    void loadAccount();
  }, [loadAccount]);

  async function download(format: "csv" | "pdf") {
    if (!account) return;

    setExporting(format);
    setExportError(null);

    try {
      const blob = await accountsApi.exportUrl(account.id, format);

      // The endpoint needs an Authorization header, so the file arrives as a
      // blob and is handed to the browser through a temporary object URL.
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `movimientos-${account.accountNumber}.${format}`;
      document.body.appendChild(link);
      link.click();
      document.body.removeChild(link);
      URL.revokeObjectURL(url);
    } catch {
      setExportError("No pudimos generar el archivo. Intente de nuevo.");
    } finally {
      setExporting(null);
    }
  }

  if (loadingAccount) return <HistorySkeleton />;
  if (accountError) {
    return <ErrorState message={accountError} onRetry={() => void loadAccount()} />;
  }

  const isEmpty = !paged.loading && paged.items.length === 0 && paged.page === 1;

  return (
    <div className="animate-fade-in">
      <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
        <div className="animate-rise">
          <h1
            className="mb-1 text-2xl"
            style={{
              fontFamily: "var(--font-display)",
              fontWeight: 600,
              color: "var(--text-primary)",
              letterSpacing: "-0.02em",
            }}
          >
            Movimientos
          </h1>
          {account && (
            <p className="text-sm" style={{ color: "var(--text-muted)" }}>
              Cuenta <span className="amount">{account.accountNumber}</span>
            </p>
          )}
        </div>

        <div className="flex gap-2">
          <Button
            variant="secondary"
            onClick={() => void download("csv")}
            loading={exporting === "csv"}
            disabled={exporting !== null || isEmpty}
          >
            Exportar CSV
          </Button>
          <Button
            variant="secondary"
            onClick={() => void download("pdf")}
            loading={exporting === "pdf"}
            disabled={exporting !== null || isEmpty}
          >
            Exportar PDF
          </Button>
        </div>
      </div>

      {exportError && (
        <p role="alert" className="mb-4 text-sm" style={{ color: "var(--color-danger)" }}>
          {exportError}
        </p>
      )}

      {isEmpty ? (
        <Card>
          <EmptyState
            title="Todavía no hay movimientos"
            description="Cuando realice su primera operación, la verá aquí con todo su detalle."
          />
        </Card>
      ) : (
        <>
          {/* Table on wide screens, cards on phones: a five-column table on a
              360px viewport is unreadable no matter how it scrolls. */}
          <Card padding={false} className="hidden md:block">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr
                    className="border-b text-left"
                    style={{ borderColor: "var(--border-subtle)", color: "var(--text-muted)" }}
                  >
                    <th className="px-5 py-3 text-xs font-semibold uppercase tracking-wider">
                      Fecha
                    </th>
                    <th className="px-5 py-3 text-xs font-semibold uppercase tracking-wider">
                      Tipo
                    </th>
                    <th className="px-5 py-3 text-xs font-semibold uppercase tracking-wider">
                      Concepto
                    </th>
                    <th className="px-5 py-3 text-xs font-semibold uppercase tracking-wider">
                      Contraparte
                    </th>
                    <th className="px-5 py-3 text-right text-xs font-semibold uppercase tracking-wider">
                      Monto
                    </th>
                  </tr>
                </thead>
                <tbody className={paged.loading ? "opacity-50" : "stagger"}>
                  {paged.loading
                    ? Array.from({ length: PAGE_SIZE }, (_, i) => (
                        <tr key={`skeleton-${i}`}>
                          <td colSpan={5} className="px-5 py-2">
                            <Skeleton className="h-7 w-full" />
                          </td>
                        </tr>
                      ))
                    : paged.items.map((tx) => (
                        <tr
                          key={tx.id}
                          className="interactive border-b last:border-b-0"
                          style={{ borderColor: "var(--border-subtle)" }}
                        >
                          <td className="px-5 py-3" style={{ color: "var(--text-secondary)" }}>
                            {formatDateTime(tx.timestamp)}
                          </td>
                          <td className="px-5 py-3" style={{ color: "var(--text-primary)" }}>
                            {transactionTypeLabels[tx.type] ?? tx.type}
                          </td>
                          <td
                            className="max-w-xs truncate px-5 py-3"
                            style={{ color: "var(--text-secondary)" }}
                          >
                            {tx.description || "—"}
                          </td>
                          <td className="px-5 py-3">
                            <Amount size="sm" tone="muted">
                              {formatAccountNumber(
                                tx.direction === "out" ? tx.toAccount : tx.fromAccount,
                              )}
                            </Amount>
                          </td>
                          <td className="px-5 py-3 text-right">
                            <Amount
                              size="sm"
                              tone={tx.direction === "in" ? "positive" : "default"}
                            >
                              {formatSigned(tx.amount, tx.direction)}
                            </Amount>
                          </td>
                        </tr>
                      ))}
                </tbody>
              </table>
            </div>

            <div className="border-t" style={{ borderColor: "var(--border-subtle)" }}>
              <Pagination
                page={paged.page}
                canGoNext={paged.hasMore}
                onPrevious={paged.previous}
                onNext={paged.next}
                loading={paged.loading}
                label="Paginación del historial"
              />
            </div>
          </Card>

          <div className="md:hidden">
            <div className="flex flex-col gap-3">
              {paged.loading
                ? Array.from({ length: PAGE_SIZE }, (_, i) => (
                    <Skeleton key={`m-skeleton-${i}`} className="h-24 w-full" />
                  ))
                : paged.items.map((tx) => (
                    <Card key={tx.id} className="lift">
                      <div className="mb-2 flex items-start justify-between gap-3">
                        <div className="min-w-0">
                          <p
                            className="truncate text-sm"
                            style={{ color: "var(--text-primary)" }}
                          >
                            {tx.description || transactionTypeLabels[tx.type] || tx.type}
                          </p>
                          <p className="text-xs" style={{ color: "var(--text-muted)" }}>
                            {formatDateTime(tx.timestamp)}
                          </p>
                        </div>
                        <Amount
                          size="sm"
                          tone={tx.direction === "in" ? "positive" : "default"}
                        >
                          {formatSigned(tx.amount, tx.direction)}
                        </Amount>
                      </div>
                      <div className="flex items-center justify-between text-xs">
                        <span style={{ color: "var(--text-muted)" }}>
                          {transactionTypeLabels[tx.type] ?? tx.type}
                        </span>
                        <Amount size="sm" tone="muted">
                          {formatAccountNumber(
                            tx.direction === "out" ? tx.toAccount : tx.fromAccount,
                          )}
                        </Amount>
                      </div>
                    </Card>
                  ))}
            </div>

            <Pagination
              page={paged.page}
              canGoNext={paged.hasMore}
              onPrevious={paged.previous}
              onNext={paged.next}
              loading={paged.loading}
              label="Paginación del historial"
            />
          </div>

          {paged.error && (
            <p role="alert" className="mt-4 text-sm" style={{ color: "var(--color-danger)" }}>
              {paged.error}
            </p>
          )}
        </>
      )}
    </div>
  );
}

function HistorySkeleton() {
  return (
    <div>
      <Skeleton className="mb-2 h-8 w-48" />
      <Skeleton className="mb-6 h-4 w-40" />
      <div className="flex flex-col gap-2">
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-14 w-full" />
        ))}
      </div>
    </div>
  );
}
