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
        className="relative hidden w-1/2 flex-col justify-between overflow-hidden p-12 lg:flex"
        style={{ backgroundColor: "var(--color-ink)" }}
      >
        {/* Ambient wash in the brand violet. Purely atmospheric, so it is
            hidden from assistive technology and sits behind the content. */}
        <div
          aria-hidden="true"
          className="pointer-events-none absolute inset-0"
          style={{
            background:
              "radial-gradient(60% 50% at 15% 15%, rgb(71 59 240 / 0.35) 0%, transparent 60%), " +
              "radial-gradient(45% 40% at 85% 80%, rgb(102 101 221 / 0.22) 0%, transparent 65%)",
          }}
        />

        {/* A quiet echo of the Flow Ribbon: money as movement, not a number. */}
        <svg
          aria-hidden="true"
          className="pointer-events-none absolute inset-x-0 bottom-0 w-full"
          viewBox="0 0 400 160"
          preserveAspectRatio="none"
          style={{ height: 200, opacity: 0.5 }}
        >
          <defs>
            <linearGradient id="auth-flow" x1="0" y1="0" x2="1" y2="0">
              <stop offset="0%" stopColor="var(--color-violet-600)" stopOpacity="0" />
              <stop offset="50%" stopColor="var(--color-violet-500)" stopOpacity="0.9" />
              <stop offset="100%" stopColor="var(--color-violet-600)" stopOpacity="0" />
            </linearGradient>
          </defs>
          <path
            d="M0 110 C 60 110, 70 60, 130 60 S 210 120, 270 95 S 340 40, 400 55"
            fill="none"
            stroke="url(#auth-flow)"
            strokeWidth="2"
            strokeLinecap="round"
          />
          <path
            d="M0 130 C 70 130, 80 95, 140 98 S 220 140, 280 120 S 350 85, 400 92"
            fill="none"
            stroke="url(#auth-flow)"
            strokeWidth="1.5"
            strokeLinecap="round"
            opacity="0.6"
          />
        </svg>

        <p
          className="animate-fade-in relative text-xl"
          style={{
            fontFamily: "var(--font-display)",
            fontWeight: 700,
            color: "#ffffff",
            letterSpacing: "-0.02em",
          }}
        >
          Banca AI
        </p>

        <div className="animate-rise relative">
          <p
            className="mb-4 text-3xl leading-tight"
            style={{
              fontFamily: "var(--font-display)",
              fontWeight: 600,
              color: "#ffffff",
              letterSpacing: "-0.02em",
            }}
          >
            Su dinero,
            <br />
            en movimiento.
          </p>
          <p
            className="max-w-sm text-sm leading-relaxed"
            style={{ color: "var(--color-violet-300)" }}
          >
            Consulte saldos, transfiera y revise sus movimientos hablando en
            lenguaje natural con su asistente.
          </p>
        </div>

        <p className="relative text-xs" style={{ color: "var(--color-slate-300)" }}>
          Las operaciones que mueven dinero siempre requieren su confirmación.
        </p>
      </div>

      {/* Form */}
      <div className="flex w-full flex-col justify-center px-6 py-12 lg:w-1/2 lg:px-16">
        <div className="animate-rise mx-auto w-full max-w-sm">
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
