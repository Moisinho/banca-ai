import { useEffect, useState } from "react";

import { Amount, Button, Status } from "@/components/ui";
import { ApiError, chat } from "@/lib/api";
import { formatAccountNumber, formatNumber, transactionTypeLabels } from "@/lib/money";
import type { PendingOperation } from "@/types/api";

/**
 * Confirmation card for an operation the assistant proposed.
 *
 * This is the barrier the test asks for: "La IA debe confirmar acciones
 * críticas antes de ejecutarlas". The funds are already reserved in the ledger,
 * but nothing moves until the person presses Confirmar here — and that request
 * goes over authenticated HTTP the model cannot issue.
 */
export function PendingOperationCard({
  operation,
  onResolved,
}: {
  operation: PendingOperation;
  onResolved: (transferId: string, status: "confirmed" | "rejected") => void;
}) {
  const [resolving, setResolving] = useState<"confirm" | "reject" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expired, setExpired] = useState(false);

  // The reservation releases itself after a timeout. Tracking it here stops the
  // person from confirming something the ledger already let go.
  useEffect(() => {
    if (operation.status !== "pending" || !operation.expiresAt) return;

    const expiresAt = new Date(operation.expiresAt).getTime();

    function check() {
      setExpired(Date.now() > expiresAt);
    }

    check();
    const timer = setInterval(check, 1000);
    return () => clearInterval(timer);
  }, [operation.status, operation.expiresAt]);

  async function resolve(action: "confirm" | "reject") {
    setResolving(action);
    setError(null);

    try {
      if (action === "confirm") {
        await chat.confirm(operation.transferId);
        onResolved(operation.transferId, "confirmed");
      } else {
        await chat.reject(operation.transferId);
        onResolved(operation.transferId, "rejected");
      }
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.message
          : "No pudimos completar la operación. Intentá de nuevo.",
      );
    } finally {
      setResolving(null);
    }
  }

  // Already decided: show the outcome instead of the buttons.
  if (operation.status !== "pending") {
    return (
      <div
        className="rounded-lg border px-3.5 py-3"
        style={{
          backgroundColor: "var(--surface-base)",
          borderColor: "var(--border-subtle)",
        }}
      >
        {operation.status === "confirmed" ? (
          <Status tone="success">Operación confirmada</Status>
        ) : operation.status === "rejected" ? (
          <Status tone="neutral">Operación cancelada</Status>
        ) : (
          <Status tone="warning">La operación expiró</Status>
        )}
      </div>
    );
  }

  const isTransfer =
    operation.operation === "transfer" || operation.operation === "internal_transfer";

  return (
    <div
      className="rounded-lg border-2 px-4 py-3.5"
      style={{
        backgroundColor: "var(--surface-base)",
        // A deliberately stronger border: this card asks for a decision, so it
        // must read as distinct from the conversation around it.
        borderColor: "var(--color-violet-600)",
      }}
    >
      <div className="mb-3 flex items-center justify-between">
        <Status tone="warning">Pendiente de confirmación</Status>
        {!expired && operation.expiresAt && <Countdown expiresAt={operation.expiresAt} />}
      </div>

      <dl className="mb-4 flex flex-col gap-2 text-sm">
        <div className="flex items-baseline justify-between">
          <dt style={{ color: "var(--text-muted)" }}>Operación</dt>
          <dd style={{ color: "var(--text-primary)" }}>
            {transactionTypeLabels[operation.operation] ?? operation.operation}
          </dd>
        </div>

        <div className="flex items-baseline justify-between">
          <dt style={{ color: "var(--text-muted)" }}>Monto</dt>
          <dd>
            <Amount size="lg">{formatNumber(operation.amount)}</Amount>{" "}
            <span className="text-xs" style={{ color: "var(--text-muted)" }}>
              {operation.currency}
            </span>
          </dd>
        </div>

        {isTransfer && (
          <div className="flex items-baseline justify-between">
            <dt style={{ color: "var(--text-muted)" }}>Destino</dt>
            <dd>
              <Amount size="sm">{formatAccountNumber(operation.toAccount)}</Amount>
            </dd>
          </div>
        )}

        {operation.description && (
          <div className="flex items-baseline justify-between gap-4">
            <dt style={{ color: "var(--text-muted)" }}>Concepto</dt>
            <dd className="text-right" style={{ color: "var(--text-primary)" }}>
              {operation.description}
            </dd>
          </div>
        )}
      </dl>

      {error && (
        <p role="alert" className="mb-3 text-sm" style={{ color: "var(--color-danger)" }}>
          {error}
        </p>
      )}

      {expired ? (
        <p className="text-sm" style={{ color: "var(--text-muted)" }}>
          La operación expiró. Pedile al asistente que la prepare de nuevo.
        </p>
      ) : (
        <div className="flex gap-2">
          <Button
            onClick={() => void resolve("confirm")}
            loading={resolving === "confirm"}
            disabled={resolving !== null}
            fullWidth
          >
            Confirmar
          </Button>
          <Button
            variant="secondary"
            onClick={() => void resolve("reject")}
            loading={resolving === "reject"}
            disabled={resolving !== null}
            fullWidth
          >
            Cancelar
          </Button>
        </div>
      )}
    </div>
  );
}

/** Shows how long the reservation has left. */
function Countdown({ expiresAt }: { expiresAt: string }) {
  const [remaining, setRemaining] = useState(() => secondsUntil(expiresAt));

  useEffect(() => {
    const timer = setInterval(() => setRemaining(secondsUntil(expiresAt)), 1000);
    return () => clearInterval(timer);
  }, [expiresAt]);

  if (remaining <= 0) return null;

  const minutes = Math.floor(remaining / 60);
  const seconds = remaining % 60;

  return (
    <span className="amount text-xs" style={{ color: "var(--text-muted)" }}>
      {minutes}:{String(seconds).padStart(2, "0")}
    </span>
  );
}

function secondsUntil(iso: string): number {
  return Math.max(0, Math.floor((new Date(iso).getTime() - Date.now()) / 1000));
}
