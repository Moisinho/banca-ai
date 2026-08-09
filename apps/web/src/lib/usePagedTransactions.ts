import { useCallback, useEffect, useRef, useState } from "react";

import { accounts as accountsApi } from "@/lib/api";
import type { Transaction } from "@/types/api";

/**
 * Windowed pagination over the transactions endpoint.
 *
 * Only the page currently on screen is held in state. Moving forward or back
 * refetches; nothing accumulates. With a thousand movements the browser still
 * only ever renders `pageSize` rows.
 *
 * The API paginates by cursor, not by offset, so there is no way to jump to an
 * arbitrary page — a cursor only points forward. Going back is solved by
 * remembering the cursor that opened each visited page: `cursors[n]` is the
 * cursor for page n + 1, and page 1 is always the empty cursor.
 *
 * Why cursors and not offsets: with offsets, a new transaction arriving shifts
 * every row down, so page 2 would repeat a record page 1 already showed. A
 * cursor points at a fixed position in time and stays correct.
 */
export function usePagedTransactions(accountId: string | null, pageSize: number) {
  const [items, setItems] = useState<Transaction[]>([]);
  const [page, setPage] = useState(1);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Cursor that opens each page. Index 0 is page 1, and is always undefined.
  const cursors = useRef<(string | undefined)[]>([undefined]);

  // Identifies the newest request so a slow earlier one cannot overwrite it.
  // Without this, clicking Next twice quickly can leave page 1's response
  // painted over page 2 — the classic out-of-order render.
  const requestId = useRef(0);

  const fetchPage = useCallback(
    async (target: number) => {
      if (!accountId) return;

      const id = ++requestId.current;
      setLoading(true);
      setError(null);

      try {
        const result = await accountsApi.transactions(accountId, {
          limit: pageSize,
          cursor: cursors.current[target - 1],
        });

        // A newer request started while this one was in flight: discard it.
        if (id !== requestId.current) return;

        setItems(result.items);
        setHasMore(result.hasMore);
        setPage(target);

        // Remember how to reach the following page.
        if (result.nextCursor) {
          cursors.current[target] = result.nextCursor;
        }
      } catch {
        if (id !== requestId.current) return;
        setError("No se pudieron cargar los movimientos. Revise su conexión.");
      } finally {
        if (id === requestId.current) setLoading(false);
      }
    },
    [accountId, pageSize],
  );

  // Reset when the account changes: the cursors belong to the previous one.
  useEffect(() => {
    cursors.current = [undefined];
    if (accountId) void fetchPage(1);
  }, [accountId, fetchPage]);

  const next = useCallback(() => {
    if (hasMore && !loading) void fetchPage(page + 1);
  }, [hasMore, loading, page, fetchPage]);

  const previous = useCallback(() => {
    if (page > 1 && !loading) void fetchPage(page - 1);
  }, [page, loading, fetchPage]);

  /** Reloads the current page, e.g. after an operation is confirmed. */
  const refresh = useCallback(() => {
    void fetchPage(page);
  }, [fetchPage, page]);

  /** Returns to the first page and forgets every cursor learned so far. */
  const reset = useCallback(() => {
    cursors.current = [undefined];
    void fetchPage(1);
  }, [fetchPage]);

  return { items, page, hasMore, loading, error, next, previous, refresh, reset };
}
