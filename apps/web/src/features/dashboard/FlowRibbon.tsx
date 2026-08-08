import { useId, useMemo, useState } from "react";

import { formatNumber, formatDate } from "@/lib/money";
import type { Transaction } from "@/types/api";

/**
 * Flow Ribbon — the dashboard's signature element.
 *
 * Shows money *moving through* the month rather than sitting still. Inflow
 * rises above the axis, outflow falls below, and the running balance threads
 * through as a hairline.
 *
 * It exists because people do not experience money as a static number; they
 * experience it as flow — what came in and what went out. A balance card
 * answers "how much"; this answers "what happened".
 */

interface FlowRibbonProps {
  transactions: Transaction[];
  currency?: string;
}

interface DayBucket {
  date: string;
  inflow: number;
  outflow: number;
  /** Running balance at the end of this day, in cents. */
  balance: number;
  transactions: number;
}

/** Chart geometry, in SVG user units. */
const CHART = {
  width: 800,
  height: 200,
  paddingX: 8,
  paddingY: 16,
  /** Vertical centre: the axis that separates inflow from outflow. */
  get axisY() {
    return this.height / 2;
  },
} as const;

export function FlowRibbon({ transactions, currency = "USD" }: FlowRibbonProps) {
  const gradientId = useId();
  const [hovered, setHovered] = useState<number | null>(null);

  const buckets = useMemo(() => bucketByDay(transactions), [transactions]);

  if (buckets.length === 0) {
    return (
      <div
        className="flex h-[200px] items-center justify-center rounded-lg border"
        style={{
          backgroundColor: "var(--surface-raised)",
          borderColor: "var(--border-subtle)",
        }}
      >
        <p className="text-sm" style={{ color: "var(--text-muted)" }}>
          Todavía no hay movimientos para mostrar
        </p>
      </div>
    );
  }

  const maxFlow = Math.max(
    ...buckets.map((b) => Math.max(b.inflow, b.outflow)),
    1, // avoids dividing by zero on a month with no movement
  );

  const balances = buckets.map((b) => b.balance);
  const minBalance = Math.min(...balances);
  const maxBalance = Math.max(...balances, minBalance + 1);

  const usableWidth = CHART.width - CHART.paddingX * 2;
  const usableHeight = CHART.axisY - CHART.paddingY;
  const columnWidth = usableWidth / buckets.length;

  const xFor = (index: number) => CHART.paddingX + index * columnWidth + columnWidth / 2;

  const balancePoints = buckets.map((bucket, index) => {
    const ratio = (bucket.balance - minBalance) / (maxBalance - minBalance);
    // Inverted because SVG's y axis grows downward.
    const y = CHART.height - CHART.paddingY - ratio * (CHART.height - CHART.paddingY * 2);
    return `${xFor(index)},${y}`;
  });

  const active = hovered !== null ? buckets[hovered] : null;

  return (
    <div className="w-full min-w-0">
      {/* flex-wrap: the title plus three legend labels don't fit one row
          below ~420px, and without wrap they'd force the chart wider than
          the viewport instead of stacking. */}
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
        <h2
          className="text-sm font-medium uppercase tracking-wider"
          style={{ color: "var(--text-muted)" }}
        >
          Flujo del período
        </h2>

        {/* Legend. Identity never rests on colour alone: each series is named. */}
        <div className="flex items-center gap-4 text-xs">
          <span className="inline-flex items-center gap-1.5" style={{ color: "var(--text-secondary)" }}>
            <span
              aria-hidden="true"
              className="inline-block h-2 w-2 rounded-sm"
              style={{ backgroundColor: "var(--color-series-2)" }}
            />
            Entradas
          </span>
          <span className="inline-flex items-center gap-1.5" style={{ color: "var(--text-secondary)" }}>
            <span
              aria-hidden="true"
              className="inline-block h-2 w-2 rounded-sm"
              style={{ backgroundColor: "var(--color-series-1)" }}
            />
            Salidas
          </span>
          <span className="inline-flex items-center gap-1.5" style={{ color: "var(--text-secondary)" }}>
            <span
              aria-hidden="true"
              className="inline-block h-[2px] w-4"
              style={{ backgroundColor: "var(--text-secondary)" }}
            />
            Saldo
          </span>
        </div>
      </div>

      <div
        className="relative overflow-hidden rounded-lg border"
        style={{
          backgroundColor: "var(--surface-raised)",
          borderColor: "var(--border-subtle)",
        }}
      >
        <svg
          viewBox={`0 0 ${CHART.width} ${CHART.height}`}
          className="w-full"
          style={{ height: CHART.height }}
          role="img"
          aria-label={`Flujo de dinero: ${buckets.length} días con movimientos`}
          preserveAspectRatio="none"
        >
          <defs>
            <linearGradient id={`${gradientId}-in`} x1="0" y1="1" x2="0" y2="0">
              <stop offset="0%" stopColor="var(--color-series-2)" stopOpacity="0.15" />
              <stop offset="100%" stopColor="var(--color-series-2)" stopOpacity="0.75" />
            </linearGradient>
            <linearGradient id={`${gradientId}-out`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--color-series-1)" stopOpacity="0.75" />
              <stop offset="100%" stopColor="var(--color-series-1)" stopOpacity="0.15" />
            </linearGradient>
          </defs>

          {/* Centre axis: the waterline between money in and money out. */}
          <line
            x1={0}
            y1={CHART.axisY}
            x2={CHART.width}
            y2={CHART.axisY}
            stroke="var(--border-default)"
            strokeWidth={1}
          />

          {buckets.map((bucket, index) => {
            const x = CHART.paddingX + index * columnWidth;
            // A 2px gap between bars keeps adjacent days readable as separate.
            const barWidth = Math.max(columnWidth - 2, 1);

            const inflowHeight = (bucket.inflow / maxFlow) * usableHeight;
            const outflowHeight = (bucket.outflow / maxFlow) * usableHeight;
            const isActive = hovered === index;

            return (
              <g key={bucket.date}>
                {bucket.inflow > 0 && (
                  <rect
                    x={x + 1}
                    y={CHART.axisY - inflowHeight}
                    width={barWidth}
                    height={inflowHeight}
                    fill={`url(#${gradientId}-in)`}
                    opacity={hovered === null || isActive ? 1 : 0.4}
                    rx={2}
                  />
                )}

                {bucket.outflow > 0 && (
                  <rect
                    x={x + 1}
                    y={CHART.axisY}
                    width={barWidth}
                    height={outflowHeight}
                    fill={`url(#${gradientId}-out)`}
                    opacity={hovered === null || isActive ? 1 : 0.4}
                    rx={2}
                  />
                )}

                {/* Invisible hit area, wider than the bar so hovering is easy. */}
                <rect
                  x={x}
                  y={0}
                  width={columnWidth}
                  height={CHART.height}
                  fill="transparent"
                  onMouseEnter={() => setHovered(index)}
                  onMouseLeave={() => setHovered(null)}
                  style={{ cursor: "pointer" }}
                />
              </g>
            );
          })}

          {/* The balance runs as a hairline through the flow. */}
          <polyline
            points={balancePoints.join(" ")}
            fill="none"
            stroke="var(--text-secondary)"
            strokeWidth={1.5}
            strokeLinejoin="round"
            strokeLinecap="round"
            opacity={0.7}
          />

          {hovered !== null && (
            <line
              x1={xFor(hovered)}
              y1={0}
              x2={xFor(hovered)}
              y2={CHART.height}
              stroke="var(--text-muted)"
              strokeWidth={1}
              strokeDasharray="3 3"
            />
          )}
        </svg>

        {active && (
          <div
            className="pointer-events-none absolute left-3 top-3 rounded-md border px-3 py-2 text-xs"
            style={{
              backgroundColor: "var(--surface-base)",
              borderColor: "var(--border-default)",
            }}
          >
            <p className="mb-1 font-medium" style={{ color: "var(--text-primary)" }}>
              {formatDate(active.date)}
            </p>
            {active.inflow > 0 && (
              <p style={{ color: "var(--color-series-2)" }}>
                Entró <span className="amount">{formatNumber(String(active.inflow / 100))}</span>{" "}
                {currency}
              </p>
            )}
            {active.outflow > 0 && (
              <p style={{ color: "var(--color-series-1)" }}>
                Salió <span className="amount">{formatNumber(String(active.outflow / 100))}</span>{" "}
                {currency}
              </p>
            )}
            <p className="mt-1" style={{ color: "var(--text-secondary)" }}>
              Saldo <span className="amount">{formatNumber(String(active.balance / 100))}</span>
            </p>
          </div>
        )}
      </div>

      {/* Table fallback: the chart is not the only way to read this data. */}
      <details className="mt-3">
        <summary
          className="cursor-pointer text-xs"
          style={{ color: "var(--text-muted)" }}
        >
          Ver los datos como tabla
        </summary>
        <div className="mt-2 max-h-48 overflow-y-auto">
          <table className="w-full text-sm">
            <thead>
              <tr style={{ color: "var(--text-muted)" }}>
                <th className="py-1 text-left font-medium">Fecha</th>
                <th className="py-1 text-right font-medium">Entradas</th>
                <th className="py-1 text-right font-medium">Salidas</th>
                <th className="py-1 text-right font-medium">Saldo</th>
              </tr>
            </thead>
            <tbody>
              {buckets.map((bucket) => (
                <tr key={bucket.date} style={{ color: "var(--text-secondary)" }}>
                  <td className="py-1">{formatDate(bucket.date)}</td>
                  <td className="amount py-1 text-right">
                    {bucket.inflow > 0 ? formatNumber(String(bucket.inflow / 100)) : "—"}
                  </td>
                  <td className="amount py-1 text-right">
                    {bucket.outflow > 0 ? formatNumber(String(bucket.outflow / 100)) : "—"}
                  </td>
                  <td className="amount py-1 text-right">
                    {formatNumber(String(bucket.balance / 100))}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}

/**
 * Groups transactions by day and computes the running balance.
 *
 * The API returns newest first, so the list is walked in reverse to accumulate
 * the balance forward in time.
 */
function bucketByDay(transactions: Transaction[]): DayBucket[] {
  if (transactions.length === 0) return [];

  const byDay = new Map<string, { inflow: number; outflow: number; count: number }>();

  for (const tx of transactions) {
    const day = tx.timestamp.slice(0, 10);
    const cents = Math.round(Number.parseFloat(tx.amount) * 100);

    const bucket = byDay.get(day) ?? { inflow: 0, outflow: 0, count: 0 };
    if (tx.direction === "in") {
      bucket.inflow += cents;
    } else {
      bucket.outflow += cents;
    }
    bucket.count += 1;

    byDay.set(day, bucket);
  }

  const days = [...byDay.entries()].sort(([a], [b]) => a.localeCompare(b));

  let running = 0;
  return days.map(([date, { inflow, outflow, count }]) => {
    running += inflow - outflow;
    return { date, inflow, outflow, balance: running, transactions: count };
  });
}
