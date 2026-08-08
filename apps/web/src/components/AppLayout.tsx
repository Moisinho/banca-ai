import { useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";

import { Button } from "@/components/ui";
import { useAuth } from "@/features/auth/AuthContext";

const links = [
  { to: "/dashboard", label: "Resumen" },
  { to: "/operaciones", label: "Operar" },
  { to: "/movimientos", label: "Movimientos" },
];

export function AppLayout() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  const [menuOpen, setMenuOpen] = useState(false);

  async function handleLogout() {
    await logout();
    navigate("/ingresar", { replace: true });
  }

  return (
    <div className="min-h-screen">
      <header
        className="sticky top-0 z-10 border-b"
        style={{
          backgroundColor: "var(--surface-base)",
          borderColor: "var(--border-subtle)",
        }}
      >
        <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3 sm:px-6">
          <div className="flex items-center gap-8">
            <span
              className="text-lg"
              style={{
                fontFamily: "var(--font-display)",
                fontWeight: 700,
                color: "var(--text-primary)",
                letterSpacing: "-0.02em",
              }}
            >
              Banca AI
            </span>

            <nav className="hidden gap-1 sm:flex" aria-label="Navegación principal">
              {links.map((link) => (
                <NavItem key={link.to} to={link.to} label={link.label} />
              ))}
            </nav>
          </div>

          <div className="flex items-center gap-3">
            {user && (
              <span
                className="hidden text-sm sm:inline"
                style={{ color: "var(--text-secondary)" }}
              >
                {user.fullName}
              </span>
            )}

            <Button variant="ghost" onClick={() => void handleLogout()}>
              Salir
            </Button>

            {/* Menu toggle, only where the inline nav is hidden. */}
            <button
              type="button"
              onClick={() => setMenuOpen((open) => !open)}
              aria-expanded={menuOpen}
              aria-label="Abrir menú de navegación"
              className="rounded-md p-2 sm:hidden"
              style={{ color: "var(--text-secondary)" }}
            >
              <span aria-hidden="true">{menuOpen ? "✕" : "☰"}</span>
            </button>
          </div>
        </div>

        {menuOpen && (
          <nav
            className="border-t px-4 py-2 sm:hidden"
            style={{ borderColor: "var(--border-subtle)" }}
            aria-label="Navegación principal"
          >
            <div className="flex flex-col">
              {links.map((link) => (
                <NavItem
                  key={link.to}
                  to={link.to}
                  label={link.label}
                  onClick={() => setMenuOpen(false)}
                />
              ))}
            </div>
          </nav>
        )}
      </header>

      {/* overflow-x-hidden is the safety net: if something deep in the tree
          refuses to shrink, it clips here instead of pushing the whole page
          into horizontal scroll. */}
      <main className="mx-auto max-w-6xl overflow-x-hidden px-4 py-6 sm:px-6 sm:py-8">
        <Outlet />
      </main>
    </div>
  );
}

function NavItem({
  to,
  label,
  onClick,
}: {
  to: string;
  label: string;
  onClick?: () => void;
}) {
  return (
    <NavLink
      to={to}
      onClick={onClick}
      className="rounded-md px-3 py-2 text-sm font-medium transition-colors"
      style={({ isActive }) => ({
        backgroundColor: isActive ? "var(--surface-sunken)" : "transparent",
        color: isActive ? "var(--text-primary)" : "var(--text-secondary)",
      })}
    >
      {label}
    </NavLink>
  );
}
