import { useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Search, Table2 } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { useParams } from "react-router";

import { databaseApi } from "../../api/endpoints";
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

export default function DataPage() {
  const { dbId = "" } = useParams();
  const [table, setTable] = useState("");
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
    if (!table && tables.length > 0) {
      setTable(tables[0].name);
    }
  }, [table, tables]);

  useEffect(() => {
    setTable("");
    setPage(1);
    setSearch("");
    setSearchInput("");
  }, [dbId]);

  const dataQuery = useQuery({
    queryKey: ["database", dbId, "data", table, page, pageSize, search],
    queryFn: () => databaseApi.data(dbId, { table, page, page_size: pageSize, q: search || undefined }),
    enabled: Boolean(dbId && table),
  });

  const data = dataQuery.data;
  const visibleRows = search
    ? (data?.rows ?? []).filter((row) => JSON.stringify(row).toLowerCase().includes(search.toLowerCase()))
    : (data?.rows ?? []);

  const submitSearch = (event: FormEvent) => {
    event.preventDefault();
    setPage(1);
    setSearch(searchInput.trim());
  };

  const chooseTable = (name: string) => {
    setTable(name);
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
        title="No tables to browse"
        description="The catalog is empty or the connection does not expose any table to this role."
      />
    );
  }

  return (
    <div className="space-y-4">
      <div className="card p-3">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-end">
          <div className="lg:w-72">
            <label className="label" htmlFor="data-table">Table</label>
            <Select id="data-table" value={table} onChange={(event) => chooseTable(event.target.value)}>
              {tables.map((entry) => (
                <option key={entry.name} value={entry.name}>
                  {entry.schema}.{entry.name}
                </option>
              ))}
            </Select>
          </div>
          <form className="flex flex-1 flex-col gap-2 sm:flex-row sm:items-end" onSubmit={submitSearch}>
            <div className="flex-1">
              <label className="label" htmlFor="data-search">Filter rows</label>
              <SearchInput
                value={searchInput}
                onChange={setSearchInput}
                placeholder="Search visible values…"
              />
            </div>
            <button type="submit" className="btn btn-secondary sm:mb-0">
              <Search className="h-4 w-4" /> Search
            </button>
          </form>
          <div className="lg:w-32">
            <label className="label" htmlFor="data-page-size">Page size</label>
            <Select
              id="data-page-size"
              value={String(pageSize)}
              onChange={(event) => setPageSize(Number(event.target.value))}
            >
              {[10, 20, 50, 100].map((size) => (
                <option key={size} value={size}>{size} rows</option>
              ))}
            </Select>
          </div>
        </div>
      </div>

      <Callout tone="neutral" title={`${formatNumber(data?.total_rows ?? 0)} matching rows`}>
        Pagination is server-side. Wide tables scroll horizontally inside this card, never the whole
        page.
        {search ? (
          <>
            {" "}Filter active: <code className="font-mono text-xs">{search}</code>.
          </>
        ) : null}
      </Callout>

      {dataQuery.isError ? (
        <ErrorBanner error={dataQuery.error} onRetry={() => void dataQuery.refetch()} />
      ) : null}

      {dataQuery.isLoading && !data ? (
        <PageLoader label="Loading rows" />
      ) : (
        <div className="table-wrap scrollbar-thin">
          <table className="table-base min-w-full">
            <thead>
              <tr>
                {(data?.columns ?? []).map((column) => (
                  <th key={column.name} className="th whitespace-nowrap">
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
              {visibleRows.map((row, index) => (
                <tr key={index} className="tr-hover">
                  {(data?.columns ?? []).map((column) => {
                    const value = row[column.name];
                    const isNull = value === null || value === undefined;
                    return (
                      <td key={column.name} className="td max-w-[22rem] truncate font-mono text-xs" title={cellValue(value)}>
                        {isNull ? (
                          <span className="text-muted/50 italic">NULL</span>
                        ) : typeof value === "boolean" ? (
                          <span className={`badge ${value ? "badge-success" : "badge-neutral"}`}>
                            {value ? "true" : "false"}
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
          {data ? (
            <Pagination page={data.page} totalPages={data.total_pages} onChange={setPage} />
          ) : null}
        </div>
      )}

      <div className="flex items-center justify-between text-[11px] text-muted">
        <span>
          {dataQuery.isFetching ? "Refreshing…" : `Page ${data?.page ?? page} of ${data?.total_pages ?? 1}`}
        </span>
        <span className="inline-flex items-center gap-1">
          <ChevronLeft className="h-3 w-3" /> server-side pagination
          <ChevronRight className="h-3 w-3" />
        </span>
      </div>
    </div>
  );
}
