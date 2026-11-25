import { Navigate, Outlet, Route, Routes, useLocation } from "react-router";

import { useAuth } from "./auth/AuthContext";
import { AppShell } from "./components/layout/AppShell";
import { PageLoader, EmptyState } from "./components/ui";
import DatabasesPage from "./pages/admin/DatabasesPage";
import GroupsPage from "./pages/admin/GroupsPage";
import UsersPage from "./pages/admin/UsersPage";
import DashboardPage from "./pages/DashboardPage";
import DatabaseLayout from "./pages/database/DatabaseLayout";
import DataPage from "./pages/database/DataPage";
import HistoryPage from "./pages/database/HistoryPage";
import JobMonitorPage from "./pages/database/JobMonitorPage";
import MigrationEditorPage from "./pages/database/MigrationEditorPage";
import MigrationsPage from "./pages/database/MigrationsPage";
import OverviewPage from "./pages/database/OverviewPage";
import ProposalReviewPage from "./pages/database/ProposalReviewPage";
import QueryConsolePage from "./pages/database/QueryConsolePage";
import SchemaPage from "./pages/database/SchemaPage";
import LoginPage from "./pages/LoginPage";
import NotFoundPage from "./pages/NotFoundPage";
import WorkersPage from "./pages/WorkersPage";
import { ShieldAlert } from "lucide-react";

function ProtectedRoute() {
  const { user, loading } = useAuth();
  const location = useLocation();

  if (loading) {
    return (
      <div className="grid min-h-dvh place-items-center bg-paper">
        <PageLoader label="Checking session" />
      </div>
    );
  }
  if (!user) {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  }
  return <Outlet />;
}

function AdminRoute() {
  const { user } = useAuth();
  if (!user?.is_platform_admin) {
    return (
      <EmptyState
        icon={ShieldAlert}
        title="Platform Admins only"
        description="This section manages users, groups, and target connections. Database tools are available to group members instead."
      />
    );
  }
  return <Outlet />;
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<ProtectedRoute />}>
        <Route element={<AppShell />}>
          <Route index element={<DashboardPage />} />
          <Route path="workers" element={<WorkersPage />} />

          <Route element={<AdminRoute />}>
            <Route path="admin/users" element={<UsersPage />} />
            <Route path="admin/groups" element={<GroupsPage />} />
            <Route path="admin/databases" element={<DatabasesPage />} />
          </Route>

          <Route path="databases/:dbId" element={<DatabaseLayout />}>
            <Route index element={<Navigate to="overview" replace />} />
            <Route path="overview" element={<OverviewPage />} />
            <Route path="schema" element={<SchemaPage />} />
            <Route path="data" element={<DataPage />} />
            <Route path="query" element={<QueryConsolePage />} />
            <Route path="migrations" element={<MigrationsPage />} />
            <Route path="migrations/new" element={<MigrationEditorPage />} />
            <Route path="migrations/:migrationId/edit" element={<MigrationEditorPage />} />
            <Route path="proposals/:proposalId" element={<ProposalReviewPage />} />
            <Route path="jobs/:jobId" element={<JobMonitorPage />} />
            <Route path="history" element={<HistoryPage />} />
          </Route>

          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>
    </Routes>
  );
}
