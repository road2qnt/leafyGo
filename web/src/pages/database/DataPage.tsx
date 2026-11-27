import { useQuery } from "@tanstack/react-query";
import { Search, Table2 } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { Link, useParams, useSearchParams } from "react-router";

import { databaseApi } from "../../api/endpoints";
import type { SchemaTable } from "../../api/types";
import {
  Callout,
  EmptyState,
  ErrorBanner,
  PageLoader,
  Pagination,
  SearchInput,
  Select,
  TableEmpty,
  TableWrap,
} from "../../components/ui";
import { cellValue, formatNumber } from "../../lib/format";

function tableKey(entry: Pick<SchemaTable, "schema" | "name">): string {
  return `${entry.schema}.${entry.name}`;
}

export default function DataPage() {
  const { dbId = "" } = useParams();
  const [searchParams] = useSearchParams();
  const requestedTable = searchParams.get("table") ?? "";

  const [selectedKey, setSelectedKey] = useState(requestedTable);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");

  const schemaQuery = useQuery({
    queryKey: ["database", dbId, "schema"],
    queryFn: () => databaseApi.schema(dbId),
    enabled: Boolean(dbId),
  });

  const tables = schemaQuery.data?.tables ?? [];

  useEffect(() => {
    setSelectedKey(requestedTable);
    setPage(1);
    setSearch("");
    setSearchInput("");
  }, [dbId, requestedTable]);

  const activeTable =
    tables.find((t) => tableKey(t) === selectedKey || t.name === selectedKey) ??
    tables.find((t) => t.can_select !== false) ??
    tables[0];

  const activeSchema = activeTable?.schema ?? "public";
  const activeTableName = activeTable?.name ?? "";
  const activeKey = activeTable ? tableKey(activeTable) : "";

  const dataQuery = useQuery({
    queryKey: ["database", dbId, "data", activeSchema, activeTableName, page, pageSize, search],
    queryFn: () =>
      databaseApi.data(dbId, {
        schema: activeSchema,
        table: activeTableName,
        page,
        page_size: pageSize,
        q: search || undefined,
      }),
    enabled: Boolean(dbId && activeTableName),
  });

  const data = dataQuery.data;
  const visibleRows = search
    ? (data?.rows ?? []).filter((row) =>
        JSON.stringify(row).toLowerCase().includes(search.toLowerCase()),
      )
    : (data?.rows ?? []);

  const submitSearch = (event: FormEvent) => {
    event.preventDefault();
    setPage(1);
    setSearch(searchInput.trim());
  };

  const chooseTable = (key: string) => {
    setSelectedKey(key);
    setPage(1);
    setSearch("");
    setSearchInput("");
  };

  useEffect(() => {
    setPage(1);
  }, [pageSize]);

  if (schemaQuery.isLoading) return <PageLoader label="Reading target catalog" />;
  if (schemaQuery.isError) {
    return <ErrorBanner error={schemaQuery.error} onRetry={() => void schemaQuery.refetch()} />;
  }
  if (tables.length === 0) {
    return (
      <EmptyState
        icon={Table2}
        title="No tables yet"
        description="There are no tables to show."
      />
    );
  }

  return (
    <div className="space-y-4">
      <div className="card p-4">
        <div className="grid gap-4 md:grid-cols-2 lg:flex lg:items-end">
          <div className="lg:w-72">
            <label className="label" htmlFor="data-table">
              Table
            </label>
            <Select
              id="data-table"
              value={activeKey}
              onChange={(event) => chooseTable(event.target.value)}
            >
              {tables.map((entry) => {
                const key = tableKey(entry);
                return (
                  <option key={key} value={key}>
                    {entry.schema}.{entry.name}
                  </option>
                );
              })}
            </Select>
          </div>
          <form
            className="flex flex-1 items-end gap-2 max-lg:order-last md:max-lg:col-span-2"
            onSubmit={submitSearch}
          >
            <div className="min-w-0 flex-1">
              <label className="label" htmlFor="data-search">
                Filter rows
              </label>
              <SearchInput
                value={searchInput}
                onChange={setSearchInput}
                placeholder="Search visible values…"
              />
            </div>
            <button type="submit" className="btn btn-secondary">
              <Search className="h-4 w-4" /> <span className="max-[22.5rem]:sr-only">Search</span>
            </button>
          </form>
          <div className="lg:w-40">
            <label className="label" htmlFor="data-page-size">
              Show
            </label>
            <Select
              id="data-page-size"
              value={String(pageSize)}
              onChange={(event) => setPageSize(Number(event.target.value))}
            >
              {[10, 20, 50, 100].map((size) => (
                <option key={size} value={size}>
                  {size} rows per page
                </option>
              ))}
            </Select>
          </div>
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="m-0 min-w-0 font-mono text-xl font-semibold break-all text-ink">
          {activeSchema}.{activeTableName}
        </h2>
        <p className="m-0 text-sm text-muted">
          {formatNumber(data?.total_rows ?? 0)} rows · Data is read only
        </p>
      </div>

      {search ? (
        <Callout tone="info">
          Filter active: <code className="border border-leaf-200 bg-leaf-100 px-1 font-mono text-xs">{search}</code>
        </Callout>
      ) : null}

      {dataQuery.isError ? (
        <ErrorBanner error={dataQuery.error} onRetry={() => void dataQuery.refetch()} />
      ) : null}

      {dataQuery.isLoading && !data ? (
        <PageLoader label="Loading rows" />
      ) : (
        <div>
        <TableWrap>
          <table className="table-base table-pin min-w-full">
            <caption id="data-caption">
              Page {data?.page ?? page}, {visibleRows.length}{" "}
              {visibleRows.length === 1 ? "row" : "rows"}
            </caption>
            <thead>
              <tr>
                {(data?.columns ?? []).map((column) => (
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
              {visibleRows.map((row, index) => (
                <tr key={index}>
                  {(data?.columns ?? []).map((column) => {
                    const value = row[column.name];
                    const isNull = value === null || value === undefined;
                    const isNum = typeof value === "number";
                    return (
                      <td
                        key={column.name}
                        className={`td max-w-[28rem] truncate font-mono text-sm whitespace-nowrap ${
                          isNum ? "text-right tabular-nums" : ""
                        }`}
                        title={cellValue(value)}
                      >
                        {isNull ? (
                          <span className="text-muted italic">NULL</span>
                        ) : value === "" ? (
                          <span className="text-muted italic">
                            {`''`}
                            <span className="sr-only"> (empty string)</span>
                          </span>
                        ) : (
                          cellValue(value)
                        )}
                      </td>
                    );
                  })}
                </tr>
              ))}
              {data && visibleRows.length === 0 ? (
                <TableEmpty colSpan={Math.max(1, data.columns.length)}>
                  No row matches the current filter.
                </TableEmpty>
              ) : null}
            </tbody>
          </table>
        </TableWrap>
        {data && data.total_pages > 1 ? (
          <div className="-mt-0.5 border-x-2 border-b-2 border-ink">
            <Pagination page={data.page} totalPages={data.total_pages} onChange={setPage} />
          </div>
        ) : null}
        </div>
      )}

      <p className="m-0 text-sm">
        <Link to={`/databases/${dbId}/schema`}>View table structure</Link>
      </p>
    </div>
  );
}
