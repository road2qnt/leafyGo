import { useQueryClient } from "@tanstack/react-query";
import { BookmarkPlus, Eraser, Play, Search } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";

import { isApiError } from "../../api/client";
import { migrationApi, databaseApi } from "../../api/endpoints";
import type { ExplainResponse, QueryResponse } from "../../api/types";
import {
  CodeBlock,
  Button,
  Callout,
  ErrorBanner,
  TableEmpty,
  TableWrap,
} from "../../components/ui";
import { SqlEditor } from "../../components/SqlEditor";
import { useToast } from "../../hooks/useToast";
import { cellValue, formatNumber } from "../../lib/format";
import { useDatabaseContext } from "./context";

const SAMPLE_SQL = "SELECT * FROM products LIMIT 50;";

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
        if (event.shiftKey) {
          void explain();
        } else {
          void run();
        }
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
    <div className="space-y-5">
      {!canEdit ? (
        <Callout tone="info" title="Read-only access">
          You can run queries and explain plans. Saving a draft and other migration actions require
          View + Edit.
        </Callout>
      ) : null}

      {error ? <ErrorBanner error={error} onDismiss={() => setError(null)} /> : null}

      <div className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-semibold text-ink">SQL Query</span>
            <span className="badge badge-info">Read only</span>
            <span className="badge badge-muted">max rows {formatNumber(1000)}</span>
            <span className="badge badge-muted">statement_timeout 15s</span>
          </div>
          {canEdit ? (
            <Button
              variant="secondary"
              size="sm"
              onClick={() => void saveAsDraft()}
              loading={saving}
              disabled={!sql.trim()}
            >
              <BookmarkPlus className="h-3.5 w-3.5" /> Save as migration draft
            </Button>
          ) : null}
        </div>

        <SqlEditor value={sql} onChange={setSql} height="240px" ariaLabel="Query console SQL" />

        <div className="flex flex-wrap items-center justify-between gap-3 pt-1">
          <div className="flex flex-wrap items-center gap-3 max-sm:grid max-sm:w-full max-sm:grid-cols-2">
            <Button onClick={() => void run()} loading={running} disabled={!sql.trim()}>
              <Play className="h-4 w-4" /> Execute
            </Button>
            <Button
              variant="secondary"
              onClick={() => void explain()}
              loading={explaining}
              disabled={!sql.trim()}
            >
              <Search className="h-4 w-4" /> Explain
            </Button>
            <Button
              variant="ghost"
              className="max-sm:col-span-2 max-sm:justify-self-start"
              onClick={() => {
                setSql("");
                setResult(null);
                setPlan(null);
                setError(null);
              }}
            >
              <Eraser className="h-4 w-4" /> Clear
            </Button>
            <p className="m-0 text-sm text-muted max-sm:col-span-2">
              Ad-hoc read queries run inside a read-only transaction and automatically roll back.
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-3 text-xs text-muted pointer-coarse:max-lg:hidden">
            <span>
              <kbd className="kbd">Ctrl</kbd> + <kbd className="kbd">Enter</kbd> to execute
            </span>
            <span>
              <kbd className="kbd">Ctrl</kbd> + <kbd className="kbd">Shift</kbd> +{" "}
              <kbd className="kbd">Enter</kbd> to explain
            </span>
          </div>
        </div>
      </div>

      <div className="tabs">
        <button
          type="button"
          className={`tab ${tab === "results" ? "tab-active" : ""}`}
          onClick={() => setTab("results")}
        >
          Result{" "}
          {result ? <span className="badge badge-muted ml-1">{result.row_count}</span> : null}
        </button>
        <button
          type="button"
          className={`tab ${tab === "plan" ? "tab-active" : ""}`}
          onClick={() => setTab("plan")}
        >
          EXPLAIN result
        </button>
      </div>

      {tab === "results" ? (
        <div className="space-y-3">
          {result ? (
            <>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <h2 className="m-0 text-lg font-semibold text-ink">Result</h2>
                  <span className="badge badge-muted">SELECT</span>
                  <span className="text-sm text-muted">{formatNumber(result.row_count)} rows</span>
                  <span className="text-sm text-muted">·</span>
                  <span className="text-sm text-muted">{result.duration_ms} ms</span>
                </div>
                {result.truncated ? <span className="badge badge-warning">Truncated</span> : null}
              </div>

              {result.notices.length > 0 ? (
                <Callout tone="info">{result.notices.join(" ")}</Callout>
              ) : null}

              <TableWrap>
                <table className="table-base table-pin min-w-full">
                  <thead>
                    <tr>
                      {result.columns.map((column) => (
                        <th key={column.name} scope="col" className="th th-data whitespace-nowrap">
                          {column.name}
                          {column.data_type ? (
                            <span className="ml-2 font-mono text-xs font-normal text-lime">
                              {column.data_type}
                            </span>
                          ) : null}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {result.rows.map((row, index) => (
                      <tr key={index}>
                        {result.columns.map((column) => {
                          const val = row[column.name];
                          const isNull = val === null || val === undefined;
                          const isNum = typeof val === "number";
                          return (
                            <td
                              key={column.name}
                              className={`td max-w-[28rem] truncate font-mono text-sm whitespace-nowrap ${
                                isNum ? "text-right tabular-nums" : ""
                              }`}
                              title={cellValue(val)}
                            >
                              {isNull ? (
                                <span className="text-muted italic">NULL</span>
                              ) : val === "" ? (
                                <span className="text-muted italic">
                                  {`''`}
                                  <span className="sr-only"> (empty string)</span>
                                </span>
                              ) : (
                                cellValue(val)
                              )}
                            </td>
                          );
                        })}
                      </tr>
                    ))}
                    {result.rows.length === 0 ? (
                      <TableEmpty colSpan={Math.max(1, result.columns.length)}>
                        Query returned zero rows.
                      </TableEmpty>
                    ) : null}
                  </tbody>
                </table>
              </TableWrap>
            </>
          ) : (
            <Callout tone="neutral" title="No result yet">
              Run a statement with <kbd className="kbd">Ctrl</kbd> + <kbd className="kbd">Enter</kbd>.
              Only read-only statements (<code className="font-mono text-xs">SELECT</code>,{" "}
              <code className="font-mono text-xs">WITH</code>,{" "}
              <code className="font-mono text-xs">SHOW</code>,{" "}
              <code className="font-mono text-xs">EXPLAIN</code>) are accepted.
            </Callout>
          )}
        </div>
      ) : (
        <div className="space-y-3">
          {plan ? (
            <>
              <div className="text-sm text-muted">Planning + execution: {plan.duration_ms} ms</div>
              <CodeBlock code={plan.plan} label="EXPLAIN result" maxHeight="28rem" />
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
