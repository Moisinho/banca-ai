import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { PendingOperation } from "@/types/api";
import { PendingOperationCard } from "./PendingOperationCard";

// El módulo de API se sustituye para no pegarle al backend en los tests.
vi.mock("@/lib/api", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api")>("@/lib/api");
  return {
    ...actual,
    chat: {
      confirm: vi.fn().mockResolvedValue({ message: "Operación confirmada" }),
      reject: vi.fn().mockResolvedValue({ message: "Operación cancelada" }),
      send: vi.fn(),
      history: vi.fn(),
    },
  };
});

const { chat } = await import("@/lib/api");

function buildOperation(overrides: Partial<PendingOperation> = {}): PendingOperation {
  return {
    transferId: "123456789",
    operation: "transfer",
    amount: "300.00",
    currency: "USD",
    fromAccount: "4001-1111-2222-3333",
    toAccount: "4001-4444-5555-6666",
    description: "Pago de alquiler",
    status: "pending",
    // Cinco minutos por delante, para que no se considere expirada.
    expiresAt: new Date(Date.now() + 5 * 60 * 1000).toISOString(),
    ...overrides,
  };
}

describe("PendingOperationCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("muestra los datos que la persona necesita revisar", () => {
    render(<PendingOperationCard operation={buildOperation()} onResolved={vi.fn()} />);

    // El monto y la cuenta destino son lo que hay que verificar antes de
    // confirmar: si no están visibles, la confirmación es un acto ciego.
    expect(screen.getByText(/300,00|300\.00/)).toBeInTheDocument();
    expect(screen.getByText("4001-4444-5555-6666")).toBeInTheDocument();
    expect(screen.getByText("Pago de alquiler")).toBeInTheDocument();
    expect(screen.getByText(/pendiente de confirmación/i)).toBeInTheDocument();
  });

  it("ofrece confirmar y cancelar", () => {
    render(<PendingOperationCard operation={buildOperation()} onResolved={vi.fn()} />);

    expect(screen.getByRole("button", { name: /confirmar/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /cancelar/i })).toBeInTheDocument();
  });

  it("confirma la operación al pulsar Confirmar", async () => {
    const user = userEvent.setup();
    const onResolved = vi.fn();

    render(<PendingOperationCard operation={buildOperation()} onResolved={onResolved} />);

    await user.click(screen.getByRole("button", { name: /confirmar/i }));

    await waitFor(() => {
      expect(chat.confirm).toHaveBeenCalledWith("123456789");
      expect(onResolved).toHaveBeenCalledWith("123456789", "confirmed");
    });
  });

  it("cancela la operación al pulsar Cancelar", async () => {
    const user = userEvent.setup();
    const onResolved = vi.fn();

    render(<PendingOperationCard operation={buildOperation()} onResolved={onResolved} />);

    await user.click(screen.getByRole("button", { name: /cancelar/i }));

    await waitFor(() => {
      expect(chat.reject).toHaveBeenCalledWith("123456789");
      expect(onResolved).toHaveBeenCalledWith("123456789", "rejected");
    });
  });

  it("no ofrece decidir una operación ya confirmada", () => {
    render(
      <PendingOperationCard
        operation={buildOperation({ status: "confirmed" })}
        onResolved={vi.fn()}
      />,
    );

    expect(screen.queryByRole("button", { name: /confirmar/i })).not.toBeInTheDocument();
    expect(screen.getByText(/operación confirmada/i)).toBeInTheDocument();
  });

  it("no ofrece decidir una operación ya cancelada", () => {
    render(
      <PendingOperationCard
        operation={buildOperation({ status: "rejected" })}
        onResolved={vi.fn()}
      />,
    );

    expect(screen.queryByRole("button", { name: /confirmar/i })).not.toBeInTheDocument();
    expect(screen.getByText(/operación cancelada/i)).toBeInTheDocument();
  });

  it("bloquea la confirmación cuando la reserva expiró", () => {
    render(
      <PendingOperationCard
        operation={buildOperation({
          expiresAt: new Date(Date.now() - 1000).toISOString(),
        })}
        onResolved={vi.fn()}
      />,
    );

    // Con la reserva vencida los fondos ya se liberaron: confirmar fallaría.
    expect(screen.queryByRole("button", { name: /confirmar/i })).not.toBeInTheDocument();
    expect(screen.getByText(/expiró/i)).toBeInTheDocument();
  });

  it("evita la doble confirmación deshabilitando los botones", async () => {
    const user = userEvent.setup();

    // Una confirmación que nunca resuelve deja ver el estado intermedio.
    vi.mocked(chat.confirm).mockImplementation(() => new Promise(() => {}));

    render(<PendingOperationCard operation={buildOperation()} onResolved={vi.fn()} />);

    const confirmar = screen.getByRole("button", { name: /confirmar/i });
    await user.click(confirmar);

    // Mientras la petición está en curso ningún botón acepta más clics: en
    // banca un doble clic no puede convertirse en dos transferencias.
    await waitFor(() => {
      expect(confirmar).toBeDisabled();
      expect(screen.getByRole("button", { name: /cancelar/i })).toBeDisabled();
    });
  });

  it("muestra el error si la confirmación falla", async () => {
    const user = userEvent.setup();
    const { ApiError } = await import("@/lib/api");

    vi.mocked(chat.confirm).mockRejectedValue(
      new ApiError("INSUFFICIENT_FUNDS", "No tenés fondos suficientes", 422),
    );

    render(<PendingOperationCard operation={buildOperation()} onResolved={vi.fn()} />);

    await user.click(screen.getByRole("button", { name: /confirmar/i }));

    await waitFor(() => {
      expect(screen.getByRole("alert")).toHaveTextContent(/fondos suficientes/i);
    });
  });
});
