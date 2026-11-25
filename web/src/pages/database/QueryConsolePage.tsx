import { useQueryClient } from "@tanstack/react-query";
import { BookmarkPlus, Eraser, Play, Search, ShieldCheck, TerminalSquare } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";

import { isApiError } from "../../api/client";
import { migrationApi, databaseApi } from "../../api/endpoints";
import type { ExplainResponse, QueryResponse } from "../../api/types";
import { CodeBlock, Button, Callout, ErrorBanner, PageHeader, StatusBadge, TableEmpty, TableWrap } from "../../components/ui";
import { SqlEditor } from "../../components/SqlEditor";
import { useToast } from "../../hooks/useToast";
import { cellValue, formatNumber } from "../../lib/format";
import { useDatabaseContext } from "./context";

const SAMPLE_SQL = "select * from products limit 50;";

export default function QueryConsolePage() {
  const { dbId = "" } = useParams();
  const { overview } = useDatabaseContext();
  const navigate = useNavigate();
  const toast = useToast();
  const queryClient = useQueryClient();

  const [sql, setSql] = useState(SAMPLE_SQL);
  const [result, setResult] = useState<QueryResponse | null>(null);
  const [plan, setPlan] = useState<ExplainResponse | null>(null);
  const [tab, setTab] = useState<"results" | "plan">("results");
  const [error, setError] = useState<unknown>(null);
  const [running, setRunning] = useState(false);
  const [explaining, setExplaining] = useState(false);
  const [saving, setSaving] = useState(false);

  const canEdit = overview.level === "edit";

  useEffect(() => {
    setResult(null);
    setPlan(null);
    setError(null);
  }, [dbId]);

  const run = async () => {
    if (!sql.trim() || running) return;
    setRunning(true);
    setError(null);
    setTab("results");
    try {
      const response = await databaseApi.query(dbId, sql);
      setResult(response);
    } catch (cause) {
      setError(cause);
      setResult(null);
    } finally {
      setRunning(false);
    }
  };

  const explain = async () => {
    if (!sql.trim() || explaining) return;
    setExplaining(true);
    setError(null);
    setTab("plan");
    try {
      const response = await databaseApi.explain(dbId, sql);
      setPlan(response);
    } catch (cause) {
      setError(cause);
      setPlan(null);
    } finally {
      setExplaining(false);
    }
  };

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
        event.preventDefault();
        void run();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  });

  const saveAsDraft = async () => {
    if (!sql.trim()) return;
    setSaving(true);
    setError(null);
    try {
      const migration = await migrationApi.create(dbId, {
        title: `Console draft · ${new Date().toLocaleString("en-GB")}`,
        description: "Saved from the read-only Query Console. Add a rollback before submitting.",
        sql_up: sql,
        sql_down: "-- TODO: write the rollback SQL\n",
        is_irreversible: false,
        irreversible_reason: "",
        execution_mode: "normal",
        batch_config: null,
        replaces_migration_id: null,
      });
      await queryClient.invalidateQueries({ queryKey: ["database", dbId, "migrations"] });
      toast.success("Draft saved", "Add a rollback, validate it, then submit for review.");
      navigate(`/databases/${dbId}/migrations/${migration.id}/edit`);
    } catch (cause) {
      setError(cause);
      if (isApiError(cause)) toast.error("Could not save draft", cause.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4">
      <PageHeader
        eyebrow="Read-only"
        title="Query console"
        description="Run SELECT statements with statement_timeout and row limits enforced by the API. The console never returns passwords or platform secrets."
        actions={
          <>
            <Button variant="secondary" onClick={() => { setSql(""); setResult(null); setPlan(null); setError(null); }}>
              <Eraser className="h-4 w-4" /> Clear
            </Button>
            <Button variant="secondary" onClick={() => void explain()} loading={explaining} disabled={!sql.trim()}>
              <Search className="h-4 w-4" /> Explain
            </Button>
            <Button onClick={() => void run()} loading={running} disabled={!sql.trim()}>
              <Play className="h-4 w-4" /> Run
            </Button>
          </>
        }
      />

      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted">
        <span className="badge badge-info"><ShieldCheck className="h-3 w-3" /> read-only role</span>
        <span className="badge badge-neutral">
          max rows {formatNumber(overview ? 1000 : 1000)}
        </span>
        <span className="badge badge-neutral">statement_timeout 15s</span>
        <span className="ml-auto inline-flex items-center gap-1">
          <TerminalSquare className="h-3 w-3" /> <span className="kbd">Ctrl</span> + <span className="kbd">Enter</span> runs
        </span>
      </div>

      {!canEdit ? (
        <Callout tone="info" title="Read-only access">
          You can run queries and explain plans. Saving a draft and other migration actions require
          View + Edit.
        </Callout>
      ) : null}

      <div>
        <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
          <span className="section-title">SQL</span>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void saveAsDraft()}
            loading={saving}
            disabled={!canEdit || !sql.trim()}
            title={canEdit ? "Save as a normal migration draft" : "Edit access required"}
          >
            <BookmarkPlus className="h-3.5 w-3.5" /> Save as migration draft
          </Button>
        </div>
        <SqlEditor value={sql} onChange={setSql} height="280px" ariaLabel="Query console SQL" />
      </div>

      {error ? <ErrorBanner error={error} onDismiss={() => setError(null)} /> : null}

      <div className="flex items-center gap-1 border-b border-line">
        <button
          type="button"
          className={`tab ${tab === "results" ? "tab-active" : ""}`}
          onClick={() => setTab("results")}
        >
          Results {result ? <span className="ml-1 badge badge-neutral">{result.row_count}</span> : null}
        </button>
        <button
          type="button"
          className={`tab ${tab === "plan" ? "tab-active" : ""}`}
          onClick={() => setTab("plan")}
        >
          Explain plan
        </button>
      </div>

      {tab === "results" ? (
        <div className="space-y-3">
          {result ? (
            <>
              <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted">
                <StatusBadge status="success" />
                <span>{formatNumber(result.row_count)} rows</span>
                <span>·</span>
                <span>{result.duration_ms} ms</span>
                {result.truncated ? <span className="badge badge-warning">truncated</span> : null}
              </div>
              {result.notices.length > 0 ? (
                <Callout tone="info">{result.notices.join(" ")}</Callout>
              ) : null}
              <TableWrap>
                <table className="table-base min-w-full">
                  <thead>
                    <tr>
                      {result.columns.map((column) => (
                        <th key={column.name} className="th">
                          {column.name}
                          {column.data_type ? (
                            <span className="ml-1.5 font-mono text-[9px] font-normal normal-case text-muted/70">
                              {column.data_type}
                            </span>
                          ) : null}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {result.rows.map((row, index) => (
                      <tr key={index} className="tr-hover">
                        {result.columns.map((column) => (
                          <td key={column.name} className="td max-w-[24rem] truncate font-mono text-xs" title={cellValue(row[column.name])}>
                            {row[column.name] === null || row[column.name] === undefined ? (
                              <span className="text-muted/50 italic">NULL</span>
                            ) : (
                              cellValue(row[column.name])
                            )}
                          </td>
                        ))}
                      </tr>
                    ))}
                    {result.rows.length === 0 ? (
                      <TableEmpty colSpan={Math.max(1, result.columns.length)}>Query returned zero rows.</TableEmpty>
                    ) : null}
                  </tbody>
                </table>
              </TableWrap>
            </>
          ) : (
            <Callout tone="neutral" title="No result yet">
              Run a statement with <span className="kbd">Ctrl</span> + <span className="kbd">Enter</span>.
              Only SELECT statements are accepted; the console is intentionally read-only.
            </Callout>
          )}
        </div>
      ) : (
        <div className="space-y-3">
          {plan ? (
            <>
              <div className="text-[11px] text-muted">Planning + execution: {plan.duration_ms} ms</div>
              <CodeBlock code={plan.plan} label="EXPLAIN (FORMAT TEXT)" maxHeight="28rem" />
            </>
          ) : (
            <Callout tone="neutral" title="No plan yet">
              Press <strong>Explain</strong> to run the plan on the target without returning rows.
            </Callout>
          )}
        </div>
      )}
    </div>
  );
}
