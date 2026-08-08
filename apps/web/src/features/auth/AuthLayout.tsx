import type { ReactNode } from "react";

/**
 * Shell for the login and register screens.
 *
 * Two columns on wide viewports, one on narrow. The brand panel is hidden on
 * small screens rather than stacked: on a phone it would push the form below
 * the fold, and the form is what the person came for.
 */
export function AuthLayout({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle: string;
  children: ReactNode;
}) {
  return (
    <div className="flex min-h-screen">
      {/* Brand panel */}
      <div
        className="hidden w-1/2 flex-col justify-between p-12 lg:flex"
        style={{ backgroundColor: "var(--color-ink)" }}
      >
        <p
          className="text-xl"
          style={{
            fontFamily: "var(--font-display)",
            fontWeight: 700,
            color: "#ffffff",
            letterSpacing: "-0.02em",
          }}
        >
          Banca AI
        </p>

        <div>
          <p
            className="mb-4 text-3xl leading-tight"
            style={{
              fontFamily: "var(--font-display)",
              fontWeight: 600,
              color: "#ffffff",
              letterSpacing: "-0.02em",
            }}
          >
            Tu dinero,
            <br />
            en movimiento.
          </p>
          <p className="max-w-sm text-sm leading-relaxed" style={{ color: "var(--color-violet-300)" }}>
            Consultá saldos, transferí y revisá tus movimientos hablando en
            lenguaje natural con tu asistente.
          </p>
        </div>

        <p className="text-xs" style={{ color: "var(--color-slate-300)" }}>
          Las operaciones que mueven dinero siempre requieren tu confirmación.
        </p>
      </div>

      {/* Form */}
      <div className="flex w-full flex-col justify-center px-6 py-12 lg:w-1/2 lg:px-16">
        <div className="mx-auto w-full max-w-sm">
          {/* Brand mark, shown only where the side panel is hidden. */}
          <p
            className="mb-8 text-lg lg:hidden"
            style={{
              fontFamily: "var(--font-display)",
              fontWeight: 700,
              color: "var(--text-primary)",
            }}
          >
            Banca AI
          </p>

          <h1
            className="mb-1 text-2xl"
            style={{
              fontFamily: "var(--font-display)",
              fontWeight: 600,
              color: "var(--text-primary)",
              letterSpacing: "-0.02em",
            }}
          >
            {title}
          </h1>
          <p className="mb-8 text-sm" style={{ color: "var(--text-muted)" }}>
            {subtitle}
          </p>

          {children}
        </div>
      </div>
    </div>
  );
}
