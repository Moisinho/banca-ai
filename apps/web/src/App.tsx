import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";

import { AppLayout } from "@/components/AppLayout";
import { AuthProvider, useAuth } from "@/features/auth/AuthContext";
import { LoginPage } from "@/features/auth/LoginPage";
import { RegisterPage } from "@/features/auth/RegisterPage";
import { DashboardPage } from "@/features/dashboard/DashboardPage";
import { HistoryPage } from "@/features/transactions/HistoryPage";
import { TransactionsPage } from "@/features/transactions/TransactionsPage";

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path="/ingresar" element={<PublicOnly><LoginPage /></PublicOnly>} />
          <Route path="/registro" element={<PublicOnly><RegisterPage /></PublicOnly>} />

          <Route element={<Protected><AppLayout /></Protected>}>
            <Route path="/dashboard" element={<DashboardPage />} />
            <Route path="/operaciones" element={<TransactionsPage />} />
            <Route path="/movimientos" element={<HistoryPage />} />
          </Route>

          <Route path="/" element={<Navigate to="/dashboard" replace />} />
          <Route path="*" element={<Navigate to="/dashboard" replace />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  );
}

/**
 * Gate for authenticated routes.
 *
 * While the session is being restored it renders nothing rather than
 * redirecting: without that wait, a page reload would bounce a logged-in
 * person to the login screen before the refresh cookie was checked.
 */
function Protected({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();

  if (loading) return <FullPageLoader />;
  if (!user) return <Navigate to="/ingresar" replace />;

  return <>{children}</>;
}

/** Keeps a signed-in person out of the login and register screens. */
function PublicOnly({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();

  if (loading) return <FullPageLoader />;
  if (user) return <Navigate to="/dashboard" replace />;

  return <>{children}</>;
}

function FullPageLoader() {
  return (
    <div className="flex min-h-screen items-center justify-center">
      <span
        aria-label="Cargando"
        role="status"
        className="inline-block h-6 w-6 animate-spin rounded-full border-2 border-t-transparent"
        style={{ borderColor: "var(--color-violet-600)", borderTopColor: "transparent" }}
      />
    </div>
  );
}
