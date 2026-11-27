import { useQuery } from "@tanstack/react-query";
import { ListTree } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router";

import { databaseApi } from "../../api/endpoints";
import {
  EmptyState,
  ErrorBanner,
  PageLoader,
  SearchInput,
  TableEmpty,
  TableWrap,
} from "../../components/ui";
import { reveal } from "../../components/reveal";
import { formatBytes, formatNumber } from "../../lib/format";

export default function SchemaPage() {
  const { dbId = "" } = useParams();
  const schemaQuery = useQuery({
    queryKey: ["database", dbId, "schema"],
    queryFn: () => databaseApi.schema(dbId),
    enabled: Boolean(dbId),
  });

  const [selected, setSelected] = useState("");
  const [filter, setFilter] = useState("");

  const tables = schemaQuery.data?.tables ?? [];
  const filtered = useMemo(
    () =>
      tables.filter(
        (table) =>
          table.name.toLowerCase().includes(filter.toLowerCase()) ||
          table.schema.toLowerCase().includes(filter.toLowerCase()),
      ),
    [tables, filter],
  );

  useEffect(() => {
    setSelected("");
    setFilter("");
  }, [dbId]);

  const activeTable =
    tables.find((table) => `${table.schema}.${table.name}` === selected || table.name === selected) ??
    tables[0];
  const detailQuery = useQuery({
    queryKey: ["database", dbId, "schema-detail", activeTable?.schema, activeTable?.name],
    queryFn: () => databaseApi.schemaTable(dbId, activeTable as (typeof tables)[number]),
    enabled: Boolean(activeTable),
  });
  const detail = detailQuery.data ?? activeTable;

  if (schemaQuery.isLoading) return <PageLoader label="Reading target catalog" />;
  if (schemaQuery.isError) {
    return <ErrorBanner error={schemaQuery.error} onRetry={() => void schemaQuery.refetch()} />;
  }

  return (
    <div className="space-y-6">
      {tables.length === 0 ? (
        <EmptyState
          icon={ListTree}
          title="No tables yet"
          description="This database has no tables or views outside the built-in PostgreSQL schemas."
        />
      ) : (
        <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.65fr)]">
          <section aria-labelledby="tables-caption" className="space-y-3">
            <SearchInput value={filter} onChange={setFilter} placeholder="Filter tables…" />
            <TableWrap>
              <table className="table-base">
                <caption id="tables-caption">
                  {filtered.length} {filtered.length === 1 ? "table or view" : "tables and views"}
                </caption>
                <thead>
                  <tr>
                    <th scope="col" className="th">Schema</th>
                    <th scope="col" className="th">Name</th>
                    <th scope="col" className="th text-right">Rows</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map((table) => {
                    const key = `${table.schema}.${table.name}`;
                    const isSelected =
                      activeTable?.schema === table.schema && activeTable?.name === table.name;
                    return (
                      <tr
                        key={key}
                        className={isSelected ? "is-selected" : undefined}
                      >
                        <td className="td font-mono text-xs text-ink-soft">{table.schema}</td>
                        <th scope="row" className="td font-mono">
                          <button
                            type="button"
                            onClick={() => {
                              setSelected(key);
                              reveal("table-detail");
                            }}
                            className="cursor-pointer border-0 bg-transparent p-0 text-left font-mono font-semibold break-all text-leaf-800 underline hover:text-leaf-900 pointer-coarse:min-h-11 pointer-coarse:w-full"
                          >
                            {table.name}
                          </button>
                        </th>
                        <td className="td text-right font-mono tabular-nums">
                          {formatNumber(table.row_estimate)}
                        </td>
                      </tr>
                    );
                  })}
                  {filtered.length === 0 ? (
                    <TableEmpty colSpan={3}>No table matches the filter.</TableEmpty>
                  ) : null}
                </tbody>
              </table>
            </TableWrap>
          </section>

          <section id="table-detail" className="min-w-0 space-y-4">
            {activeTable ? (
              <>
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <h2 className="m-0 flex flex-wrap items-center gap-2 text-xl font-semibold text-ink">
                    <span className="badge badge-neutral">TABLE</span>
                    <span className="min-w-0 font-mono break-all">
                      {activeTable.schema}.{activeTable.name}
                    </span>
                  </h2>
                  <div className="flex flex-wrap items-center gap-2 text-sm text-muted">
                    <span>~{formatNumber(activeTable.row_estimate)} rows</span>
                    <span>·</span>
                    <span className="font-mono">{formatBytes(activeTable.size_bytes)}</span>
                    <Link
                      to={`/databases/${dbId}/data?table=${encodeURIComponent(`${activeTable.schema}.${activeTable.name}`)}`}
                      className="btn btn-sm ml-1"
                    >
                      Browse data
                    </Link>
                  </div>
                </div>

                <TableWrap>
                  <table className="table-base table-stack">
                    <caption id="cols-caption">
                      {(detail?.columns ?? []).length}{" "}
                      {(detail?.columns ?? []).length === 1 ? "column" : "columns"}
                    </caption>
                    <thead>
                      <tr>
                        <th scope="col" className="th text-right">#</th>
                        <th scope="col" className="th">Name</th>
                        <th scope="col" className="th">Type</th>
                        <th scope="col" className="th">Nullable</th>
                        <th scope="col" className="th">Default</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(detail?.columns ?? []).map((column, index) => (
                        <tr key={column.name}>
                          <td data-label="#" className="td text-right font-mono tabular-nums">{index + 1}</td>
                          <th scope="row" data-label="Name" className="td cell-title font-mono font-semibold">
                            <span>
                              {column.name}
                              {column.is_primary_key ? (
                                <abbr
                                  title="Primary key"
                                  className="ml-1.5 font-sans text-xs font-bold tracking-wider text-ink-soft no-underline"
                                >
                                  PK
                                </abbr>
                              ) : null}
                            </span>
                          </th>
                          <td data-label="Type" className="td font-mono">{column.data_type}</td>
                          <td data-label="Nullable" className="td">{column.nullable ? "Yes" : "No"}</td>
                          <td
                            data-label="Default"
                            className="td max-w-64 truncate font-mono"
                            title={column.default_value ?? ""}
                          >
                            {column.default_value ? (
                              column.default_value
                            ) : (
                              <span className="text-muted italic">none</span>
                            )}
                          </td>
                        </tr>
                      ))}
                      {(detail?.columns ?? []).length === 0 ? (
                        <TableEmpty colSpan={5}>Catalog returned no columns.</TableEmpty>
                      ) : null}
                    </tbody>
                  </table>
                </TableWrap>
              </>
            ) : null}
          </section>
        </div>
      )}
    </div>
  );
}
