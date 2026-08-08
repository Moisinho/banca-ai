import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { auth, setSessionExpiredHandler } from "@/lib/api";
import type { User } from "@/types/api";

interface AuthState {
  user: User | null;
  /** True while the initial session restore is in flight. */
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, password: string, fullName: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  // Restores the session from the refresh cookie on load.
  //
  // Without this, reloading the page would log the user out: the access token
  // lives in memory and is gone after a refresh.
  useEffect(() => {
    let cancelled = false;

    void auth.restore().then((session) => {
      if (cancelled) return;
      setUser(session?.user ?? null);
      setLoading(false);
    });

    return () => {
      cancelled = true;
    };
  }, []);

  // The API client calls this when a refresh fails, so an expired session
  // clears the UI instead of leaving a stale user on screen.
  useEffect(() => {
    setSessionExpiredHandler(() => setUser(null));
  }, []);

  const login = useCallback(async (email: string, password: string) => {
    const session = await auth.login({ email, password });
    setUser(session.user);
  }, []);

  const register = useCallback(
    async (email: string, password: string, fullName: string) => {
      const session = await auth.register({ email, password, fullName });
      setUser(session.user);
    },
    [],
  );

  const logout = useCallback(async () => {
    await auth.logout();
    setUser(null);
  }, []);

  const value = useMemo(
    () => ({ user, loading, login, register, logout }),
    [user, loading, login, register, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const context = useContext(AuthContext);

  if (!context) {
    throw new Error("useAuth debe usarse dentro de un AuthProvider");
  }

  return context;
}
