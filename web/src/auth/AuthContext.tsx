import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { setCsrfToken } from "../api/client";
import { authApi } from "../api/endpoints";
import type { AuthResponse, User } from "../api/types";

export interface GroupRef {
  id: string;
  name: string;
}

interface AuthContextValue {
  user: User | null;
  csrfToken: string;
  leaderOfGroups: GroupRef[];
  loading: boolean;
  login: (username: string, password: string) => Promise<AuthResponse>;
  logout: () => Promise<void>;
  isLeader: boolean;
}

const AuthContext = createContext<AuthContextValue | null>(null);

const AUTH_FLAG = "leafygo-auth";

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [user, setUser] = useState<User | null>(null);
  const [csrfToken, setToken] = useState("");
  const [leaderOfGroups, setLeaderOfGroups] = useState<GroupRef[]>([]);
  const [hasSession, setHasSession] = useState(() => {
    try {
      return window.localStorage.getItem(AUTH_FLAG) === "1";
    } catch {
      return false;
    }
  });

  const meQuery = useQuery({
    queryKey: ["auth", "me"],
    queryFn: authApi.me,
    enabled: hasSession,
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 60_000,
  });

  useEffect(() => {
    if (meQuery.data) {
      setUser(meQuery.data.user);
      setToken(meQuery.data.csrf_token);
      setLeaderOfGroups(meQuery.data.leader_of_groups);
      setCsrfToken(meQuery.data.csrf_token);
    }
  }, [meQuery.data]);

  useEffect(() => {
    if (meQuery.isError) {
      setUser(null);
      setToken("");
      setLeaderOfGroups([]);
      setCsrfToken("");
      setHasSession(false);
      try {
        window.localStorage.removeItem(AUTH_FLAG);
      } catch {
        // private mode
      }
    }
  }, [meQuery.isError]);

  useEffect(() => {
    const handleUnauthorized = () => {
      setUser(null);
      setToken("");
      setLeaderOfGroups([]);
      setCsrfToken("");
      setHasSession(false);
      try {
        window.localStorage.removeItem(AUTH_FLAG);
      } catch {
        // private mode
      }
      queryClient.clear();
    };
    window.addEventListener("leafygo:unauthorized", handleUnauthorized);
    return () => window.removeEventListener("leafygo:unauthorized", handleUnauthorized);
  }, [queryClient]);

  const login = useCallback(
    async (username: string, password: string) => {
      const response = await authApi.login(username, password);
      setUser(response.user);
      setToken(response.csrf_token);
      setLeaderOfGroups(response.leader_of_groups);
      setCsrfToken(response.csrf_token);
      setHasSession(true);
      try {
        window.localStorage.setItem(AUTH_FLAG, "1");
      } catch {
        // private mode
      }
      queryClient.setQueryData(["auth", "me"], response);
      await queryClient.invalidateQueries({ queryKey: ["databases"] });
      return response;
    },
    [queryClient],
  );

  const logout = useCallback(async () => {
    try {
      await authApi.logout();
    } finally {
      setUser(null);
      setToken("");
      setLeaderOfGroups([]);
      setCsrfToken("");
      setHasSession(false);
      try {
        window.localStorage.removeItem(AUTH_FLAG);
      } catch {
        // private mode
      }
      queryClient.clear();
    }
  }, [queryClient]);

  const value = useMemo<AuthContextValue>(
    () => ({
      user,
      csrfToken,
      leaderOfGroups,
      loading: hasSession && meQuery.isLoading,
      login,
      logout,
      isLeader: leaderOfGroups.length > 0,
    }),
    [user, csrfToken, leaderOfGroups, meQuery.isLoading, login, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error("useAuth must be used inside <AuthProvider>");
  }
  return context;
}
