import { useQuery } from "@tanstack/react-query";
import { KeyRound, ListTree, Search, Table2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useParams } from "react-router";

import { databaseApi } from "../../api/endpoints";
import {
  Callout,
  Card,
  EmptyState,
  ErrorBanner,
  PageLoader,
  SearchInput,
  SectionHeader,
  TableEmpty,
  TableWrap,
} from "../../components/ui";
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
    if (!selected && tables.length > 0) {
      setSelected(tables[0].name);
    }
  }, [selected, tables]);

  useEffect(() => {
    setSelected("");
    setFilter("");
  }, [dbId]);

  const activeTable = tables.find((table) => table.name === selected) ?? tables[0];

  if (schemaQuery.isLoading) return <PageLoader label="Reading target catalog" />;
  if (schemaQuery.isError) {
    return <ErrorBanner error={schemaQuery.error} onRetry={() => void schemaQuery.refetch()} />;
  }

  return (
    <div className="space-y-4">
      <Callout tone="info" title="Catalog only — internal tables are hidden">
        LeafyGo hides its own <code className="font-mono text-xs">_leafygo_*</code> checkpoint and
        evidence tables from this inspector.
      </Callout>

      {tables.length === 0 ? (
        <EmptyState
          icon={ListTree}
          title="No tables visible"
          description="The target is reachable but the catalog is empty, or the configured role has no visible objects."
        />
      ) : (
        <div className="grid gap-4 lg:grid-cols-[300px_minmax(0,1fr)]">
          <Card className="flex max-h-[70vh] flex-col overflow-hidden">
            <div className="border-b border-line p-3">
              <SearchInput value={filter} onChange={setFilter} placeholder="Filter tables…" />
            </div>
            <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto p-2">
              {filtered.map((table) => (
                <button
                  key={`${table.schema}.${table.name}`}
                  type="button"
                  onClick={() => setSelected(table.name)}
                  className={`mb-1 flex w-full items-start gap-2 rounded-lg px-2.5 py-2 text-left transition ${
                    activeTable?.name === table.name
                      ? "bg-leaf-50 text-leaf-900"
                      : "hover:bg-[#f7f6f0]"
                  }`}
                >
                  <Table2 className="mt-0.5 h-4 w-4 shrink-0 text-leaf-600" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{table.name}</span>
                    <span className="block font-mono text-[10px] text-muted">
                      {formatNumber(table.row_estimate)} rows · {formatBytes(table.size_bytes)}
                    </span>
                  </span>
                </button>
              ))}
              {filtered.length === 0 ? (
                <p className="px-2 py-6 text-center text-xs text-muted">
                  <Search className="mx-auto mb-1 h-4 w-4" /> No table matches.
                </p>
              ) : null}
            </div>
          </Card>

          <div className="min-w-0 space-y-4">
            {activeTable ? (
              <>
                <Card className="p-5">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div>
                      <p className="font-mono text-[11px] text-muted">{activeTable.schema}</p>
                      <h2 className="text-lg font-semibold text-ink">{activeTable.name}</h2>
                    </div>
                    <div className="flex gap-2">
                      <span className="badge badge-neutral">{activeTable.columns.length} columns</span>
                      <span className="badge badge-neutral">~{formatNumber(activeTable.row_estimate)} rows</span>
                      <span className="badge badge-neutral">{formatBytes(activeTable.size_bytes)}</span>
                    </div>
                  </div>
                </Card>

                <div>
                  <SectionHeader title="Columns" description="Types as reported by the target catalog." />
                  <TableWrap>
                    <table className="table-base min-w-[640px]">
                      <thead>
                        <tr>
                          <th className="th">Column</th>
                          <th className="th">Type</th>
                          <th className="th">Nullable</th>
                          <th className="th">Default</th>
                        </tr>
                      </thead>
                      <tbody>
                        {activeTable.columns.map((column) => (
                          <tr key={column.name} className="tr-hover">
                            <td className="td">
                              <span className="inline-flex items-center gap-1.5 font-mono text-xs">
                                {column.is_primary_key ? (
                                  <KeyRound className="h-3.5 w-3.5 text-[#b38327]" aria-label="Primary key" />
                                ) : null}
                                {column.name}
                              </span>
                            </td>
                            <td className="td font-mono text-xs text-muted">{column.data_type}</td>
                            <td className="td">
                              <span className={`badge ${column.nullable ? "badge-warning" : "badge-success"}`}>
                                {column.nullable ? "NULL" : "NOT NULL"}
                              </span>
                            </td>
                            <td className="td max-w-64 truncate font-mono text-[11px] text-muted" title={column.default_value ?? ""}>
                              {column.default_value ?? "—"}
                            </td>
                          </tr>
                        ))}
                        {activeTable.columns.length === 0 ? (
                          <TableEmpty colSpan={4}>Catalog returned no columns.</TableEmpty>
                        ) : null}
                      </tbody>
                    </table>
                  </TableWrap>
                </div>
              </>
            ) : null}
          </div>
        </div>
      )}
    </div>
  );
}
