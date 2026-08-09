import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { accounts as accountsApi } from "@/lib/api";
import type { Paginated, Transaction } from "@/types/api";
import { usePagedTransactions } from "./usePagedTransactions";

/**
 * The promise this hook makes is that only the page on screen exists in memory:
 * with a thousand movements the browser still holds `pageSize` rows. These
 * tests guard that promise, and the cursor bookkeeping that makes going back
 * possible over an API that only paginates forward.
 */

function transaction(id: string): Transaction {
  return {
    id,
    type: "deposit",
    status: "completed",
    amount: "100.00",
    currency: "USD",
    fromAccount: "EXTERNAL",
    toAccount: "4001-0000-0000-0001",
    description: `Movimiento ${id}`,
    direction: "in",
    timestamp: "2024-03-15T10:30:00Z",
  };
}

function page(ids: string[], nextCursor: string | null, hasMore: boolean): Paginated<Transaction> {
  return { items: ids.map(transaction), nextCursor, hasMore };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("usePagedTransactions", () => {
  it("carga la primera página sin cursor", async () => {
    const spy = vi
      .spyOn(accountsApi, "transactions")
      .mockResolvedValue(page(["1", "2"], "cursor-1", true));

    const { result } = renderHook(() => usePagedTransactions("account-1", 2));

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.items).toHaveLength(2);
    expect(result.current.page).toBe(1);
    expect(spy).toHaveBeenCalledWith("account-1", { limit: 2, cursor: undefined });
  });

  it("reemplaza los datos al avanzar en lugar de acumularlos", async () => {
    vi.spyOn(accountsApi, "transactions")
      .mockResolvedValueOnce(page(["1", "2"], "cursor-1", true))
      .mockResolvedValueOnce(page(["3", "4"], "cursor-2", true));

    const { result } = renderHook(() => usePagedTransactions("account-1", 2));
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => result.current.next());
    await waitFor(() => expect(result.current.page).toBe(2));

    // Lo esencial: en pantalla hay dos filas, no cuatro.
    expect(result.current.items).toHaveLength(2);
    expect(result.current.items.map((t) => t.id)).toEqual(["3", "4"]);
  });

  it("vuelve atrás reutilizando el cursor de la página anterior", async () => {
    const spy = vi
      .spyOn(accountsApi, "transactions")
      .mockResolvedValueOnce(page(["1", "2"], "cursor-1", true))
      .mockResolvedValueOnce(page(["3", "4"], "cursor-2", true))
      .mockResolvedValueOnce(page(["1", "2"], "cursor-1", true));

    const { result } = renderHook(() => usePagedTransactions("account-1", 2));
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => result.current.next());
    await waitFor(() => expect(result.current.page).toBe(2));

    act(() => result.current.previous());
    await waitFor(() => expect(result.current.page).toBe(1));

    // La primera página se vuelve a pedir: no quedó guardada de antes.
    expect(result.current.items.map((t) => t.id)).toEqual(["1", "2"]);
    expect(spy).toHaveBeenCalledTimes(3);
    expect(spy).toHaveBeenLastCalledWith("account-1", { limit: 2, cursor: undefined });
  });

  it("usa el cursor aprendido al pedir la página siguiente", async () => {
    const spy = vi
      .spyOn(accountsApi, "transactions")
      .mockResolvedValueOnce(page(["1"], "cursor-1", true))
      .mockResolvedValueOnce(page(["2"], "cursor-2", true));

    const { result } = renderHook(() => usePagedTransactions("account-1", 1));
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => result.current.next());
    await waitFor(() => expect(result.current.page).toBe(2));

    expect(spy).toHaveBeenLastCalledWith("account-1", { limit: 1, cursor: "cursor-1" });
  });

  it("no avanza más allá de la última página", async () => {
    const spy = vi
      .spyOn(accountsApi, "transactions")
      .mockResolvedValue(page(["1"], null, false));

    const { result } = renderHook(() => usePagedTransactions("account-1", 5));
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.hasMore).toBe(false);

    act(() => result.current.next());
    await waitFor(() => expect(result.current.page).toBe(1));

    // Sin página siguiente, no se hace una petición inútil.
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("no retrocede desde la primera página", async () => {
    const spy = vi
      .spyOn(accountsApi, "transactions")
      .mockResolvedValue(page(["1"], "cursor-1", true));

    const { result } = renderHook(() => usePagedTransactions("account-1", 5));
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => result.current.previous());
    await waitFor(() => expect(result.current.page).toBe(1));

    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("ignora una petición en curso mientras otra está pendiente", async () => {
    // Con una petición en vuelo, la interfaz deshabilita los botones. El hook
    // no confía en eso: también se protege por su cuenta, porque un doble clic
    // rápido puede colarse entre el estado y el repintado.
    let resolveSlow: ((value: Paginated<Transaction>) => void) | undefined;

    const spy = vi
      .spyOn(accountsApi, "transactions")
      .mockResolvedValueOnce(page(["1"], "cursor-1", true))
      .mockImplementationOnce(
        () =>
          new Promise<Paginated<Transaction>>((resolve) => {
            resolveSlow = resolve;
          }),
      );

    const { result } = renderHook(() => usePagedTransactions("account-1", 1));
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => result.current.next());
    // Segundo intento mientras el primero sigue colgado: no debe dispararse.
    act(() => result.current.next());

    expect(spy).toHaveBeenCalledTimes(2);

    act(() => resolveSlow?.(page(["2"], "cursor-2", true)));

    await waitFor(() => expect(result.current.page).toBe(2));
    expect(result.current.items.map((t) => t.id)).toEqual(["2"]);
  });

  it("descarta la respuesta lenta de una petición ya superada", async () => {
    // Dos peticiones en vuelo y la vieja responde última. Sin el control de
    // orden, sus datos se pintarían encima de los actuales.
    const pending: ((value: Paginated<Transaction>) => void)[] = [];

    vi.spyOn(accountsApi, "transactions")
      .mockResolvedValueOnce(page(["1"], "cursor-1", true))
      .mockImplementation(
        () =>
          new Promise<Paginated<Transaction>>((resolve) => {
            pending.push(resolve);
          }),
      );

    const { result } = renderHook(() => usePagedTransactions("account-1", 1));
    await waitFor(() => expect(result.current.loading).toBe(false));

    // Primera petición de la página 2: queda colgada.
    act(() => result.current.next());
    // Se fuerza una segunda petición con refresh, que también queda colgada.
    act(() => result.current.refresh());

    expect(pending).toHaveLength(2);

    // Responde primero la MÁS RECIENTE.
    act(() => pending[1]!(page(["nuevo"], "cursor-x", true)));
    await waitFor(() => expect(result.current.items.map((t) => t.id)).toEqual(["nuevo"]));

    // Y ahora la vieja: debe ignorarse por completo.
    act(() => pending[0]!(page(["viejo"], "cursor-y", true)));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.items.map((t) => t.id)).toEqual(["nuevo"]);
  });

  it("informa el error sin dejar datos a medias", async () => {
    vi.spyOn(accountsApi, "transactions").mockRejectedValue(new Error("sin red"));

    const { result } = renderHook(() => usePagedTransactions("account-1", 5));

    await waitFor(() => expect(result.current.error).not.toBeNull());
    expect(result.current.items).toHaveLength(0);
    expect(result.current.loading).toBe(false);
  });

  it("no pide nada mientras no haya cuenta", async () => {
    const spy = vi.spyOn(accountsApi, "transactions");

    renderHook(() => usePagedTransactions(null, 5));

    expect(spy).not.toHaveBeenCalled();
  });
});
