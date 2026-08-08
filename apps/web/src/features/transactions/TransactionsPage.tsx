import { useEffect, useState, type FormEvent } from "react";

import { Amount, Button, Card, Input, Status } from "@/components/ui";
import { accounts as accountsApi, ApiError, transactions as txApi } from "@/lib/api";
import { isValidAmount, normalizeAmount, formatNumber } from "@/lib/money";
import type { Account } from "@/types/api";

type Operation = "deposit" | "withdraw" | "transfer";

const operationLabels: Record<Operation, string> = {
  deposit: "Depositar",
  withdraw: "Retirar",
  transfer: "Transferir",
};

/**
 * Manual operations form.
 *
 * The test allows operations "via chat o formularios"; both exist here because
 * a form is faster for a known amount, and the chat is better when you are not
 * sure what you want.
 */
export function TransactionsPage() {
  const [account, setAccount] = useState<Account | null>(null);
  const [operation, setOperation] = useState<Operation>("deposit");

  const [amount, setAmount] = useState("");
  const [toAccountNumber, setToAccountNumber] = useState("");
  const [description, setDescription] = useState("");

  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    void accountsApi.list().then(({ accounts }) => {
      setAccount(accounts[0] ?? null);
    });
  }, []);

  async function refreshAccount() {
    const { accounts } = await accountsApi.list();
    setAccount(accounts[0] ?? null);
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    if (!account) return;

    setError(null);
    setSuccess(null);
    setFieldErrors({});

    const errors: Record<string, string> = {};

    if (!isValidAmount(amount)) {
      errors.amount = "Ingresá un monto válido, con hasta dos decimales";
    }

    if (operation === "transfer") {
      if (!toAccountNumber.trim()) {
        errors.toAccountNumber = "Ingresá la cuenta destino";
      } else if (!/^\d{4}(-\d{4}){3}$/.test(toAccountNumber.trim())) {
        errors.toAccountNumber = "El formato debe ser 4001-XXXX-XXXX-XXXX";
      } else if (toAccountNumber.trim() === account.accountNumber) {
        errors.toAccountNumber = "No podés transferir a tu misma cuenta";
      }
    }

    if (Object.keys(errors).length > 0) {
      setFieldErrors(errors);
      return;
    }

    setSubmitting(true);
    try {
      const normalized = normalizeAmount(amount);

      if (operation === "deposit") {
        await txApi.deposit({ accountId: account.id, amount: normalized, description });
        setSuccess(`Depositaste ${formatNumber(normalized)} ${account.currency}`);
      } else if (operation === "withdraw") {
        await txApi.withdraw({ accountId: account.id, amount: normalized, description });
        setSuccess(`Retiraste ${formatNumber(normalized)} ${account.currency}`);
      } else {
        await txApi.transfer({
          fromAccountId: account.id,
          toAccountNumber: toAccountNumber.trim(),
          amount: normalized,
          description,
        });
        setSuccess(
          `Transferiste ${formatNumber(normalized)} ${account.currency} a ${toAccountNumber.trim()}`,
        );
      }

      setAmount("");
      setToAccountNumber("");
      setDescription("");
      await refreshAccount();
    } catch (err) {
      setError(
        err instanceof ApiError
          ? err.message
          : "No pudimos completar la operación. Intentá de nuevo.",
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="mx-auto max-w-lg">
      <h1
        className="mb-1 text-2xl"
        style={{
          fontFamily: "var(--font-display)",
          fontWeight: 600,
          color: "var(--text-primary)",
          letterSpacing: "-0.02em",
        }}
      >
        Nueva operación
      </h1>
      <p className="mb-6 text-sm" style={{ color: "var(--text-muted)" }}>
        También podés pedírselo al asistente en el panel de conversación.
      </p>

      {account && (
        <Card className="mb-5">
          <div className="flex items-baseline justify-between">
            <span className="text-sm" style={{ color: "var(--text-muted)" }}>
              Saldo disponible
            </span>
            <div>
              <Amount size="lg">{formatNumber(account.available)}</Amount>{" "}
              <span className="text-xs" style={{ color: "var(--text-muted)" }}>
                {account.currency}
              </span>
            </div>
          </div>
        </Card>
      )}

      {/* Operation selector as radios, not a dropdown: three options are worth
          showing at once, and radios are reachable by keyboard without opening
          a menu. */}
      <fieldset className="mb-5">
        <legend className="sr-only">Tipo de operación</legend>
        <div className="grid grid-cols-3 gap-2">
          {(Object.keys(operationLabels) as Operation[]).map((value) => (
            <label
              key={value}
              className="cursor-pointer rounded-md border px-3 py-2.5 text-center text-sm transition-colors"
              style={{
                backgroundColor:
                  operation === value ? "var(--color-violet-600)" : "var(--surface-raised)",
                borderColor:
                  operation === value ? "var(--color-violet-600)" : "var(--border-default)",
                color: operation === value ? "#ffffff" : "var(--text-secondary)",
              }}
            >
              <input
                type="radio"
                name="operation"
                value={value}
                checked={operation === value}
                onChange={() => {
                  setOperation(value);
                  setError(null);
                  setSuccess(null);
                  setFieldErrors({});
                }}
                className="sr-only"
              />
              {operationLabels[value]}
            </label>
          ))}
        </div>
      </fieldset>

      <form onSubmit={handleSubmit} noValidate className="flex flex-col gap-4">
        {success && (
          <div
            role="status"
            className="rounded-md border px-3 py-2.5"
            style={{
              backgroundColor: "var(--surface-sunken)",
              borderColor: "var(--color-success)",
            }}
          >
            <Status tone="success">{success}</Status>
          </div>
        )}

        {error && (
          <div
            role="alert"
            className="rounded-md border px-3 py-2.5 text-sm"
            style={{
              backgroundColor: "var(--surface-sunken)",
              borderColor: "var(--color-danger)",
              color: "var(--color-danger)",
            }}
          >
            {error}
          </div>
        )}

        <Input
          label="Monto"
          type="text"
          inputMode="decimal"
          placeholder="0.00"
          numeric
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          error={fieldErrors.amount}
          disabled={submitting}
        />

        {operation === "transfer" && (
          <Input
            label="Cuenta destino"
            type="text"
            placeholder="4001-0000-0000-0000"
            numeric
            value={toAccountNumber}
            onChange={(e) => setToAccountNumber(e.target.value)}
            error={fieldErrors.toAccountNumber}
            disabled={submitting}
          />
        )}

        <Input
          label="Concepto"
          type="text"
          placeholder="Opcional"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          disabled={submitting}
        />

        <Button type="submit" loading={submitting} fullWidth className="mt-2">
          {operationLabels[operation]}
        </Button>
      </form>
    </div>
  );
}
