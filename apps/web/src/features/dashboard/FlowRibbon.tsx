import { useId, useMemo, useState } from "react";

import { formatNumber, formatDate } from "@/lib/money";
import type { Transaction } from "@/types/api";

/**
 * Flow Ribbon — the dashboard's signature element.
 *
 * Shows money *moving through* the period rather than sitting still. Inflow
 * rises above the axis, outflow falls below, and the running balance threads
 * through as a line.
 *
 * Readability notes, learned from the previous version:
 *
 * - Bars are solid, not gradient-filled. A gradient that fades to 15% opacity
 *   makes a short bar nearly invisible and makes two bars of the same height
 *   look like different values.
 * - Inflow and outflow are distinguished by colour *and* by side of the axis,
 *   so the direction survives a greyscale print or colour blindness.
 * - There is a value axis. Without one, a bar chart shows shape but not
 *   magnitude, and "how much came in" is the actual question here.
 * - The scale maximum is rounded to a clean number, so the axis labels can be
 *   compared at a glance instead of decoded.
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
  height: 260,
  paddingLeft: 62,
  paddingRight: 12,
  paddingTop: 18,
  paddingBottom: 30,
} as const;

/** Vertical space available above (or below) the axis for the tallest bar. */
const HALF_HEIGHT = (CHART.height - CHART.paddingTop - CHART.paddingBottom) / 2;
const AXIS_Y = CHART.paddingTop + HALF_HEIGHT;

export function FlowRibbon({ transactions, currency = "USD" }: FlowRibbonProps) {
  const gradientId = useId();
  const [hovered, setHovered] = useState<number | null>(null);

  const buckets = useMemo(() => bucketByDay(transactions), [transactions]);

  const totals = useMemo(
    () =>
      buckets.reduce(
        (acc, b) => ({ inflow: acc.inflow + b.inflow, outflow: acc.outflow + b.outflow }),
        { inflow: 0, outflow: 0 },
      ),
    [buckets],
  );

  if (buckets.length === 0) {
    return (
      <div
        className="animate-fade-in flex h-[200px] items-center justify-center rounded-lg border"
        style={{
          backgroundColor: "var(--surface-raised)",
          borderColor: "var(--border-subtle)",
          boxShadow: "var(--shadow-sm)",
        }}
      >
        <p className="text-sm" style={{ color: "var(--text-muted)" }}>
          Todavía no hay movimientos para mostrar
        </p>
      </div>
    );
  }

  const maxFlow = Math.max(...buckets.map((b) => Math.max(b.inflow, b.outflow)), 1);

  // Round the scale up to a clean number so the axis labels are readable
  // ("1.500" instead of "1.487,33").
  const scaleMax = niceCeiling(maxFlow);

  const usableWidth = CHART.width - CHART.paddingLeft - CHART.paddingRight;
  const columnWidth = usableWidth / buckets.length;

  // Bars stay slim and centred in their slot, with a floor so a single-day
  // period does not render one bar 700px wide.
  const barWidth = Math.max(Math.min(columnWidth - 6, 26), 3);

  const xFor = (index: number) => CHART.paddingLeft + index * columnWidth + columnWidth / 2;
  const heightFor = (value: number) => (value / scaleMax) * HALF_HEIGHT;

  const balances = buckets.map((b) => b.balance);
  const minBalance = Math.min(...balances, 0);
  const maxBalance = Math.max(...balances, minBalance + 1);

  const balancePoints = buckets
    .map((bucket, index) => {
      const ratio = (bucket.balance - minBalance) / (maxBalance - minBalance);
      const y = CHART.height - CHART.paddingBottom - ratio * (CHART.height - CHART.paddingTop - CHART.paddingBottom);
      return `${xFor(index)},${y}`;
    })
    .join(" ");

  const active = hovered !== null ? buckets[hovered] : null;

  // Four gridlines per half: enough to read a value, few enough to stay quiet.
  const gridValues = [0.5, 1].flatMap((fraction) => [fraction, -fraction]);

  return (
    <div className="animate-rise w-full min-w-0">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
        <div>
          <h2
            className="text-sm font-semibold uppercase tracking-wider"
            style={{ color: "var(--text-secondary)" }}
          >
            Flujo del período
          </h2>
          {/* The totals answer the question the chart raises, in words. */}
          <p className="mt-0.5 text-xs" style={{ color: "var(--text-muted)" }}>
            Entró{" "}
            <span className="amount" style={{ color: "var(--color-series-2)" }}>
              {formatNumber(String(totals.inflow / 100))}
            </span>{" "}
            · Salió{" "}
            <span className="amount" style={{ color: "var(--color-series-1)" }}>
              {formatNumber(String(totals.outflow / 100))}
            </span>{" "}
            {currency}
          </p>
        </div>

        {/* Legend. Identity never rests on colour alone: each series is named,
            and the swatch shape mirrors the mark used in the chart. */}
        <div className="flex flex-wrap items-center gap-4 text-xs">
          <LegendItem color="var(--color-series-2)" label="Entradas" />
          <LegendItem color="var(--color-series-1)" label="Salidas" />
          <span
            className="inline-flex items-center gap-1.5"
            style={{ color: "var(--text-secondary)" }}
          >
            <span
              aria-hidden="true"
              className="inline-block h-[2px] w-4 rounded-full"
              style={{ backgroundColor: "var(--color-series-3)" }}
            />
            Saldo acumulado
          </span>
        </div>
      </div>

      <div
        className="relative overflow-hidden rounded-lg border"
        style={{
          backgroundColor: "var(--surface-raised)",
          borderColor: "var(--border-subtle)",
          boxShadow: "var(--shadow-sm)",
        }}
        onMouseLeave={() => setHovered(null)}
      >
        <svg
          viewBox={`0 0 ${CHART.width} ${CHART.height}`}
          className="w-full"
          style={{ height: CHART.height }}
          role="img"
          aria-label={
            `Flujo de dinero en ${buckets.length} días. ` +
            `Entradas por ${formatNumber(String(totals.inflow / 100))} ${currency}, ` +
            `salidas por ${formatNumber(String(totals.outflow / 100))} ${currency}.`
          }
        >
          <defs>
            {/* A soft fill under the balance line, to separate it from the
                bars without competing with them. */}
            <linearGradient id={`${gradientId}-balance`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--color-series-3)" stopOpacity="0.18" />
              <stop offset="100%" stopColor="var(--color-series-3)" stopOpacity="0" />
            </linearGradient>
          </defs>

          {/* --- Gridlines and value axis --- */}
          {gridValues.map((fraction) => {
            const y = AXIS_Y - fraction * HALF_HEIGHT;
            const value = Math.abs(fraction) * scaleMax;

            return (
              <g key={fraction}>
                <line
                  x1={CHART.paddingLeft}
                  y1={y}
                  x2={CHART.width - CHART.paddingRight}
                  y2={y}
                  stroke="var(--border-subtle)"
                  strokeWidth={1}
                />
                <text
                  x={CHART.paddingLeft - 8}
                  y={y + 3.5}
                  textAnchor="end"
                  fontSize={10}
                  fill="var(--text-muted)"
                  className="amount"
                >
                  {compactNumber(value / 100)}
                </text>
              </g>
            );
          })}

          {/* Zero line: the waterline between money in and money out. */}
          <line
            x1={CHART.paddingLeft}
            y1={AXIS_Y}
            x2={CHART.width - CHART.paddingRight}
            y2={AXIS_Y}
            stroke="var(--border-default)"
            strokeWidth={1.5}
          />
          <text
            x={CHART.paddingLeft - 8}
            y={AXIS_Y + 3.5}
            textAnchor="end"
            fontSize={10}
            fill="var(--text-muted)"
            className="amount"
          >
            0
          </text>

          {/* --- Bars --- */}
          {buckets.map((bucket, index) => {
            const x = xFor(index) - barWidth / 2;
            const inflowHeight = heightFor(bucket.inflow);
            const outflowHeight = heightFor(bucket.outflow);
            const dimmed = hovered !== null && hovered !== index;

            return (
              <g
                key={bucket.date}
                style={{
                  opacity: dimmed ? 0.35 : 1,
                  transition: "opacity var(--duration-fast) var(--ease-standard)",
                }}
              >
                {bucket.inflow > 0 && (
                  <rect
                    x={x}
                    y={AXIS_Y - inflowHeight}
                    width={barWidth}
                    height={inflowHeight}
                    fill="var(--color-series-2)"
                    rx={2}
                  >
                    {/* Bars grow from the axis on first paint. */}
                    <animate
                      attributeName="height"
                      from="0"
                      to={inflowHeight}
                      dur="0.45s"
                      fill="freeze"
                      calcMode="spline"
                      keySplines="0.2 0 0 1"
                    />
                    <animate
                      attributeName="y"
                      from={AXIS_Y}
                      to={AXIS_Y - inflowHeight}
                      dur="0.45s"
                      fill="freeze"
                      calcMode="spline"
                      keySplines="0.2 0 0 1"
                    />
                  </rect>
                )}

                {bucket.outflow > 0 && (
                  <rect
                    x={x}
                    y={AXIS_Y}
                    width={barWidth}
                    height={outflowHeight}
                    fill="var(--color-series-1)"
                    rx={2}
                  >
                    <animate
                      attributeName="height"
                      from="0"
                      to={outflowHeight}
                      dur="0.45s"
                      fill="freeze"
                      calcMode="spline"
                      keySplines="0.2 0 0 1"
                    />
                  </rect>
                )}

                {/* Invisible hit area spanning the full column, so hovering
                    does not require aiming at a thin bar. */}
                <rect
                  x={CHART.paddingLeft + index * columnWidth}
                  y={CHART.paddingTop}
                  width={columnWidth}
                  height={CHART.height - CHART.paddingTop - CHART.paddingBottom}
                  fill="transparent"
                  onMouseEnter={() => setHovered(index)}
                  style={{ cursor: "pointer" }}
                />
              </g>
            );
          })}

          {/* --- Running balance --- */}
          <polyline
            points={balancePoints}
            fill="none"
            stroke="var(--color-series-3)"
            strokeWidth={2}
            strokeLinejoin="round"
            strokeLinecap="round"
          />

          {hovered !== null && (
            <>
              <line
                x1={xFor(hovered)}
                y1={CHART.paddingTop}
                x2={xFor(hovered)}
                y2={CHART.height - CHART.paddingBottom}
                stroke="var(--text-muted)"
                strokeWidth={1}
                strokeDasharray="3 3"
              />
              <circle
                cx={xFor(hovered)}
                cy={balancePointY(buckets[hovered]!, minBalance, maxBalance)}
                r={3.5}
                fill="var(--color-series-3)"
                stroke="var(--surface-raised)"
                strokeWidth={2}
              />
            </>
          )}

          {/* --- Date axis --- */}
          {buckets.map((bucket, index) => {
            // Only a few labels fit; thinning keeps them from colliding.
            const step = Math.ceil(buckets.length / 8);
            if (index % step !== 0 && index !== buckets.length - 1) return null;

            return (
              <text
                key={`label-${bucket.date}`}
                x={xFor(index)}
                y={CHART.height - 10}
                textAnchor="middle"
                fontSize={10}
                fill="var(--text-muted)"
              >
                {shortDate(bucket.date)}
              </text>
            );
          })}
        </svg>

        {active && (
          <div
            className="animate-fade-in pointer-events-none absolute left-3 top-3 rounded-md border px-3 py-2 text-xs"
            style={{
              backgroundColor: "var(--surface-raised)",
              borderColor: "var(--border-default)",
              boxShadow: "var(--shadow-lg)",
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
            <p className="mt-1 border-t pt-1" style={{ color: "var(--text-secondary)" }}>
              Saldo <span className="amount">{formatNumber(String(active.balance / 100))}</span>
            </p>
            <p style={{ color: "var(--text-muted)" }}>
              {active.transactions}{" "}
              {active.transactions === 1 ? "movimiento" : "movimientos"}
            </p>
          </div>
        )}
      </div>

      {/* Table fallback: the chart is not the only way to read this data. */}
      <details className="mt-3">
        <summary
          className="cursor-pointer text-xs transition-colors hover:text-[var(--text-secondary)]"
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

function LegendItem({ color, label }: { color: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5" style={{ color: "var(--text-secondary)" }}>
      <span
        aria-hidden="true"
        className="inline-block h-2.5 w-2.5 rounded-sm"
        style={{ backgroundColor: color }}
      />
      {label}
    </span>
  );
}

function balancePointY(bucket: DayBucket, minBalance: number, maxBalance: number): number {
  const ratio = (bucket.balance - minBalance) / (maxBalance - minBalance);
  return (
    CHART.height - CHART.paddingBottom - ratio * (CHART.height - CHART.paddingTop - CHART.paddingBottom)
  );
}

/**
 * Rounds a value up to a readable scale maximum.
 *
 * A raw maximum like 148.733 produces axis labels nobody can compare at a
 * glance; 150.000 reads instantly.
 */
function niceCeiling(value: number): number {
  if (value <= 0) return 1;

  const magnitude = 10 ** Math.floor(Math.log10(value));
  const normalized = value / magnitude;

  const step = normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10;
  return step * magnitude;
}

/** Compact axis label: 1500 becomes "1,5 k". */
function compactNumber(value: number): string {
  if (Math.abs(value) >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(1).replace(".", ",")} M`;
  }
  if (Math.abs(value) >= 1000) {
    return `${(value / 1000).toFixed(1).replace(".", ",")} k`;
  }
  return String(Math.round(value));
}

/** Axis date label: "2024-03-15" becomes "15/3". */
function shortDate(iso: string): string {
  const [, month, day] = iso.split("-");
  return `${Number(day)}/${Number(month)}`;
}

/**
 * Groups transactions by day and computes the running balance.
 *
 * The API returns newest first, so the list is walked in date order to
 * accumulate the balance forward in time.
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
