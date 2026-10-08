import * as React from "react";
import { QueryClient, QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { AuthProvider, useAuth } from "@/lib/auth";
import { ToastProvider } from "@/components/toast";
import { Layout } from "@/components/layout";
import { Spinner } from "@/components/ui";
import { ApiError } from "@/lib/api";
import { applyLive, dropLive, loadLive } from "@/lib/live";
import { LoginPage } from "@/pages/login";
import { DashboardPage } from "@/pages/dashboard";
import { ServersPage, ServerDetailPage } from "@/pages/servers";
import { NodesPage } from "@/pages/nodes";
import { UsersPage, UserDetailPage } from "@/pages/users";
import { RulesPage } from "@/pages/rules";
import { ConnlogPage } from "@/pages/connlog";
import { PersonalPageView } from "@/pages/public";
import { SettingsPage } from "@/pages/settings";
import { AccountPage } from "@/pages/account";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2,
      staleTime: 5_000,
      refetchOnWindowFocus: true,
    },
  },
});

function RequireAuth({ children, admin }: { children: React.ReactNode; admin?: boolean }) {
  const { user, loading } = useAuth();
  const loc = useLocation();
  if (loading) return <Spinner className="min-h-screen" />;
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname }} />;
  if (admin && user.role !== "admin") return <Navigate to="/" replace />;
  return <>{children}</>;
}

function LoginRoute() {
  const { user, loading } = useAuth();
  if (loading) return <Spinner className="min-h-screen" />;
  if (user) return <Navigate to="/" replace />;
  return <LoginPage />;
}

// Live updates: subscribe to SSE and invalidate affected queries. An admin's
// open stream is also what makes the servers send a reading every second, so
// a tab nobody is looking at lets go of it.
function EventStream() {
  const qc = useQueryClient();
  const { user } = useAuth();
  React.useEffect(() => {
    if (!user) return;
    const admin = user.role === "admin";
    let es: EventSource | null = null;
    let timer: number | undefined;
    let away: number | undefined;
    // coalesce bursts (many agents heartbeating) into one refetch per key every few seconds
    const pending = new Set<string>();
    let flush: number | undefined;
    const inval = (keys: string[][]) => {
      keys.forEach((k) => pending.add(JSON.stringify(k)));
      if (flush) return;
      flush = window.setTimeout(() => {
        pending.forEach((k) => qc.invalidateQueries({ queryKey: JSON.parse(k) }));
        pending.clear();
        flush = undefined;
      }, 3000);
    };
    const connect = () => {
      es = new EventSource("/api/v1/events");
      if (admin) {
        es.addEventListener("hello", () => void loadLive().catch(() => undefined));
        es.addEventListener("live", (e) => applyLive(JSON.parse((e as MessageEvent).data)));
      }
      es.addEventListener("agent.heartbeat", () => inval([["servers"], ["dashboard"]]));
      es.addEventListener("agent.enrolled", () => inval([["servers"], ["dashboard"]]));
      es.addEventListener("agent.applied", () => inval([["servers"], ["nodes"]]));
      es.addEventListener("share.changed", () => inval([["shares"], ["subscriptions"], ["nodes"], ["dashboard"]]));
      es.addEventListener("quota", () => inval([["servers"], ["shares"], ["dashboard"]]));
      es.onerror = () => {
        es?.close();
        dropLive();
        timer = window.setTimeout(connect, 5000);
      };
    };
    const close = () => {
      es?.close();
      es = null;
      window.clearTimeout(timer);
      dropLive();
    };
    const visibility = () => {
      window.clearTimeout(away);
      if (document.hidden) {
        away = window.setTimeout(close, 30_000);
      } else if (!es) {
        connect();
        qc.invalidateQueries();
      }
    };
    document.addEventListener("visibilitychange", visibility);
    connect();
    return () => {
      document.removeEventListener("visibilitychange", visibility);
      window.clearTimeout(away);
      close();
      if (flush) window.clearTimeout(flush);
    };
  }, [qc, user]);
  return null;
}

// Last line of defence: a render error in one page must not blank the whole app.
class ErrorBoundary extends React.Component<{ children: React.ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error("render error", error, info.componentStack);
  }
  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-3 p-6 text-center">
        <h1 className="text-lg font-semibold">页面渲染出错</h1>
        <pre className="max-w-xl overflow-auto rounded-md border bg-muted/40 p-3 text-left text-xs">{String(this.state.error?.message || this.state.error)}</pre>
        <div className="flex gap-2">
          <button className="rounded-md border px-3 py-1.5 text-sm" onClick={() => this.setState({ error: null })}>重试</button>
          <button className="rounded-md border px-3 py-1.5 text-sm" onClick={() => { window.location.href = "/"; }}>返回总览</button>
        </div>
      </div>
    );
  }
}

// A user's own link (/s/<token>, /r/<code>) opened in a browser. It needs no
// session and must not touch the panel's login state.
const personalPage = /^\/(s|r)\/[^/]+\/?$/.test(window.location.pathname);

export default function App() {
  if (personalPage) return <PersonalPageView />;
  return (
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <BrowserRouter>
          <AuthProvider>
            <EventStream />
            <ErrorBoundary>
            <Routes>
              <Route path="/login" element={<LoginRoute />} />
              <Route element={<RequireAuth><Layout /></RequireAuth>}>
                <Route index element={<DashboardPage />} />
                <Route path="servers" element={<ServersPage />} />
                <Route path="servers/:id" element={<ServerDetailPage />} />
                <Route path="nodes" element={<NodesPage />} />
                <Route path="users" element={<UsersPage />} />
                <Route path="users/:id" element={<UserDetailPage />} />
                <Route path="rules" element={<RulesPage />} />
                <Route path="connlog" element={<ConnlogPage />} />
                <Route path="account" element={<AccountPage />} />
                <Route path="settings" element={<SettingsPage />} />
                <Route path="*" element={<Navigate to="/" replace />} />
              </Route>
            </Routes>
            </ErrorBoundary>
          </AuthProvider>
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  );
}
