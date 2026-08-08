import { useCallback, useEffect, useState } from "react";

import { Amount, Button, Card, EmptyState, ErrorState, Skeleton, Status } from "@/components/ui";
import { accounts as accountsApi } from "@/lib/api";
import {
  formatAccountNumber,
  formatDateTime,
  formatSigned,
  transactionTypeLabels,
} from "@/lib/money";
import type { Account, Transaction } from "@/types/api";

export function HistoryPage() {
  const [account, setAccount] = useState<Account | null>(null);
  const [items, setItems] = useState<Transaction[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [exporting, setExporting] = useState<"csv" | "pdf" | null>(null);

  const loadFirstPage = useCallback(async () => {
    setError(null);
    setLoading(true);

    try {
      const { accounts } = await accountsApi.list();
      const primary = accounts[0] ?? null;
      setAccount(primary);

      if (!primary) return;

      const page = await accountsApi.transactions(primary.id, { limit: 25 });
      setItems(page.items);
      setCursor(page.nextCursor);
      setHasMore(page.hasMore);
    } catch {
      setError("No pudimos cargar tus movimientos. Revisá tu conexión.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadFirstPage();
  }, [loadFirstPage]);

  async function loadMore() {
    if (!account || !cursor || loadingMore) return;

    setLoadingMore(true);
    try {
      const page = await accountsApi.transactions(account.id, { limit: 25, cursor });
      // Appended rather than replaced: cursor pagination keeps what is already
      // on screen and adds the next slice below it.
      setItems((current) => [...current, ...page.items]);
      setCursor(page.nextCursor);
      setHasMore(page.hasMore);
    } catch {
      setError("No pudimos cargar más movimientos.");
    } finally {
      setLoadingMore(false);
    }
  }

  async function download(format: "csv" | "pdf") {
    if (!account) return;

    setExporting(format);
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
      setError("No pudimos generar el archivo. Intentá de nuevo.");
    } finally {
      setExporting(null);
    }
  }

  if (loading) return <HistorySkeleton />;
  if (error && items.length === 0) {
    return <ErrorState message={error} onRetry={() => void loadFirstPage()} />;
  }

  return (
    <div>
      <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
        <div>
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
            disabled={exporting !== null || items.length === 0}
          >
            Exportar CSV
          </Button>
          <Button
            variant="secondary"
            onClick={() => void download("pdf")}
            loading={exporting === "pdf"}
            disabled={exporting !== null || items.length === 0}
          >
            Exportar PDF
          </Button>
        </div>
      </div>

      {items.length === 0 ? (
        <Card>
          <EmptyState
            title="Todavía no hay movimientos"
            description="Cuando hagas tu primera operación, la vas a ver acá con todo su detalle."
          />
        </Card>
      ) : (
        <>
          {/* Table on wide screens, cards on phones: a six-column table on a
              360px viewport is unreadable no matter how it scrolls. */}
          <Card padding={false} className="hidden md:block">
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr
                    className="border-b text-left"
                    style={{ borderColor: "var(--border-subtle)", color: "var(--text-muted)" }}
                  >
                    <th className="px-5 py-3 font-medium">Fecha</th>
                    <th className="px-5 py-3 font-medium">Tipo</th>
                    <th className="px-5 py-3 font-medium">Concepto</th>
                    <th className="px-5 py-3 font-medium">Contraparte</th>
                    <th className="px-5 py-3 text-right font-medium">Monto</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((tx) => (
                    <tr
                      key={tx.id}
                      className="border-b last:border-b-0"
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
                        <Amount size="sm" tone={tx.direction === "in" ? "positive" : "default"}>
                          {formatSigned(tx.amount, tx.direction)}
                        </Amount>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>

          <div className="flex flex-col gap-3 md:hidden">
            {items.map((tx) => (
              <Card key={tx.id}>
                <div className="mb-2 flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="truncate text-sm" style={{ color: "var(--text-primary)" }}>
                      {tx.description || transactionTypeLabels[tx.type] || tx.type}
                    </p>
                    <p className="text-xs" style={{ color: "var(--text-muted)" }}>
                      {formatDateTime(tx.timestamp)}
                    </p>
                  </div>
                  <Amount size="sm" tone={tx.direction === "in" ? "positive" : "default"}>
                    {formatSigned(tx.amount, tx.direction)}
                  </Amount>
                </div>
                <div className="flex items-center justify-between text-xs">
                  <span style={{ color: "var(--text-muted)" }}>
                    {transactionTypeLabels[tx.type] ?? tx.type}
                  </span>
                  <Amount size="sm" tone="muted">
                    {formatAccountNumber(tx.direction === "out" ? tx.toAccount : tx.fromAccount)}
                  </Amount>
                </div>
              </Card>
            ))}
          </div>

          {error && (
            <p role="alert" className="mt-4 text-sm" style={{ color: "var(--color-danger)" }}>
              {error}
            </p>
          )}

          {hasMore && (
            <div className="mt-6 flex justify-center">
              <Button variant="secondary" onClick={() => void loadMore()} loading={loadingMore}>
                Cargar más
              </Button>
            </div>
          )}

          {!hasMore && items.length > 0 && (
            <p className="mt-6 text-center text-sm" style={{ color: "var(--text-muted)" }}>
              <Status tone="neutral">No hay más movimientos</Status>
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
