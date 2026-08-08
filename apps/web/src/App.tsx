import { useEffect, useState } from "react";

const API_URL = import.meta.env.VITE_API_URL ?? "http://localhost:8080";

type HealthStatus = "checking" | "online" | "offline";

/**
 * Provisional shell used to verify the dev environment end to end.
 * Replaced by the router and real screens in the frontend phase.
 */
export default function App() {
  const [status, setStatus] = useState<HealthStatus>("checking");
  const [checkedAt, setCheckedAt] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function checkHealth() {
      try {
        const response = await fetch(`${API_URL}/health`);
        if (cancelled) return;

        if (response.ok) {
          const body = (await response.json()) as { time?: string };
          setStatus("online");
          setCheckedAt(body.time ?? null);
        } else {
          setStatus("offline");
        }
      } catch {
        if (!cancelled) setStatus("offline");
      }
    }

    void checkHealth();
    const timer = setInterval(() => void checkHealth(), 5000);

    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, []);

  return (
    <main className="min-h-screen flex items-center justify-center p-6">
      <div className="w-full max-w-md">
        <p
          className="text-xs uppercase tracking-[0.2em] mb-3"
          style={{ color: "var(--text-muted)" }}
        >
          Entorno de desarrollo
        </p>

        <h1
          className="text-4xl mb-8"
          style={{
            fontFamily: "var(--font-display)",
            fontWeight: 700,
            letterSpacing: "-0.02em",
          }}
        >
          Banca AI
        </h1>

        <div
          className="rounded-lg border p-5"
          style={{
            backgroundColor: "var(--surface-raised)",
            borderColor: "var(--border-subtle)",
          }}
        >
          <div className="flex items-center justify-between mb-4">
            <span style={{ color: "var(--text-secondary)" }}>Estado de la API</span>
            <StatusBadge status={status} />
          </div>

          <div
            className="flex items-center justify-between pt-4 border-t"
            style={{ borderColor: "var(--border-subtle)" }}
          >
            <span style={{ color: "var(--text-secondary)" }}>Saldo de ejemplo</span>
            {/* Muestra la tipografía tabular que usamos para todos los montos. */}
            <span className="amount text-2xl" style={{ color: "var(--text-primary)" }}>
              32,354.53
            </span>
          </div>

          {checkedAt && (
            <p className="text-xs mt-4" style={{ color: "var(--text-muted)" }}>
              Última verificación: {new Date(checkedAt).toLocaleTimeString("es")}
            </p>
          )}
        </div>
      </div>
    </main>
  );
}

function StatusBadge({ status }: { status: HealthStatus }) {
  const config = {
    checking: { label: "Verificando", color: "var(--text-muted)", symbol: "○" },
    online: { label: "En línea", color: "var(--color-success)", symbol: "●" },
    offline: { label: "Sin conexión", color: "var(--color-danger)", symbol: "▲" },
  }[status];

  return (
    // The symbol carries the state alongside colour, so the badge stays
    // readable without relying on hue alone.
    <span
      className="inline-flex items-center gap-2 text-sm font-medium"
      style={{ color: config.color }}
    >
      <span aria-hidden="true">{config.symbol}</span>
      {config.label}
    </span>
  );
}
