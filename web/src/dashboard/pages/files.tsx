import { useEffect, useRef, useState } from "preact/hooks";
import type { FileRow, FilesPage, Overview } from "../api";
import { Column, ErrorState, Loading, PageContent, PageToolbar, Pager, SeedProgress, SourceLabel, Table, ToolbarButton, VerdictLabel } from "../components";
import { Icon } from "../icons";
import { href, useResource, type Route } from "../hooks";
import { formatSize, relativeTime } from "../../shared/format";
import { VERDICT_LABEL } from "../../shared/tokens";

const columns: Column<FileRow>[] = [
  { key: "verdict", label: "Verdict", sortable: true, render: (f) => <VerdictLabel verdict={f.verdict} /> },
  { key: "source", label: "Source", sortable: true, render: (f) => <SourceLabel file={f} /> },
  {
    key: "title",
    label: "Title",
    sortable: true,
    class: "cell-title",
    render: (f) => (
      <div>
        <div class="cell-strong">{f.parentTitle || "-"}</div>
        <div class="cell-path" title={f.path}>
          {f.relativePath || f.path}
        </div>
      </div>
    ),
  },
  { key: "instance", label: "Instance", sortable: true, render: (f) => f.instance },
  { key: "size", label: "Size", sortable: true, class: "num", render: (f) => formatSize(f.size) },
  { key: "indexer", label: "Indexer", sortable: true, render: (f) => f.indexer || <span class="muted">-</span> },
  { key: "tracker", label: "Tracker", sortable: true, render: (f) => f.tracker || <span class="muted">-</span> },
  {
    key: "ratio",
    label: "Ratio",
    sortable: true,
    class: "num",
    render: (f) =>
      f.ratio != null ? (
        <span title={f.requiredRatio ? `required ${f.requiredRatio}` : undefined}>{f.ratio.toFixed(2)}</span>
      ) : (
        <span class="muted">-</span>
      ),
  },
  { key: "seeding", label: "Seeding", sortable: true, render: (f) => <SeedProgress file={f} /> },
  {
    key: "why",
    label: "Why",
    class: "cell-why",
    render: (f) => <span title={(f.reasons || []).join("\n")}>{(f.reasons || [])[(f.reasons || []).length - 1] || ""}</span>,
  },
];

function query(route: Route): Record<string, string> {
  const p = route.params;
  return {
    instance: p.get("instance") || "",
    verdict: p.get("verdict") || "",
    source: p.get("source") || "",
    q: p.get("q") || "",
    sort: p.get("sort") || "size",
    dir: p.get("dir") || "",
    page: p.get("page") || "1",
  };
}

function go(next: Record<string, string>) {
  location.hash = href("files", next);
}

export function FilesPage(props: { route: Route }) {
  const q = query(props.route);
  const api = new URLSearchParams(Object.entries({ ...q, pageSize: "50" }).filter(([, v]) => v !== "")).toString();
  const r = useResource<FilesPage>(`files?${api}`, (d) => (d.computing ? 4000 : false));
  const ov = useResource<Overview>("overview");
  const [text, setText] = useState(q.q);
  const debounce = useRef<number | undefined>(undefined);

  useEffect(() => setText(q.q), [q.q]);

  const setFilter = (k: string, v: string) => go({ ...q, [k]: v, page: "1" });
  const onSearch = (v: string) => {
    setText(v);
    window.clearTimeout(debounce.current);
    debounce.current = window.setTimeout(() => setFilter("q", v), 250);
  };
  const onSort = (key: string) => {
    const same = q.sort === key;
    const dir = same ? (r.data?.sortDirection === "ascending" ? "desc" : "asc") : "";
    go({ ...q, sort: key, dir, page: "1" });
  };
  const hasFilter = !!(q.instance || q.verdict || q.source || q.q);
  const d = r.data;

  return (
    <>
      <PageToolbar>
        <ToolbarButton icon="refresh" label="Refresh" onClick={r.reload} spinning={r.loading} />
        {hasFilter ? <ToolbarButton icon="clear" label="Clear filters" onClick={() => go({ sort: q.sort, dir: q.dir, page: "1" })} /> : null}
      </PageToolbar>
      <PageContent>
        <div class="filters" role="search">
          <label class="input-icon">
            <Icon name="search" size={16} />
            <input
              type="search"
              placeholder="Search title, path, indexer, tracker"
              aria-label="Search files"
              value={text}
              onInput={(e) => onSearch((e.target as HTMLInputElement).value)}
            />
          </label>
          <select aria-label="Instance" value={q.instance} onChange={(e) => setFilter("instance", (e.target as HTMLSelectElement).value)}>
            <option value="">All instances</option>
            {(ov.data?.instances || []).map((i) => (
              <option key={i.name} value={i.name}>
                {i.name}
              </option>
            ))}
          </select>
          <select aria-label="Verdict" value={q.verdict} onChange={(e) => setFilter("verdict", (e.target as HTMLSelectElement).value)}>
            <option value="">All verdicts</option>
            {(["keep", "frees-nothing", "safe", "unknown"] as const).map((v) => (
              <option key={v} value={v}>
                {VERDICT_LABEL[v]}
              </option>
            ))}
          </select>
          <select aria-label="Source" value={q.source} onChange={(e) => setFilter("source", (e.target as HTMLSelectElement).value)}>
            <option value="">All sources</option>
            <option value="usenet">Usenet</option>
            <option value="torrent">Torrent (public)</option>
            <option value="private">Torrent (private)</option>
            <option value="unknown">Unknown</option>
          </select>
        </div>
        {r.error ? <ErrorState error={r.error} /> : null}
        {!d && !r.error ? <Loading /> : null}
        {d ? (
          <>
            {d.computing ? <p class="hint">Evaluating the library; results fill in as instances finish.</p> : null}
            <Table
              columns={columns}
              rows={d.records}
              rowKey={(f) => `${f.instance}:${f.fileId}`}
              sortKey={d.sortKey}
              sortDir={d.sortDirection}
              onSort={onSort}
              empty={d.computing ? "Still evaluating..." : "No file matches these filters"}
            />
            <Pager page={d.page} totalPages={d.totalPages} totalRecords={d.totalRecords} onPage={(p) => go({ ...q, page: String(p) })} />
            {d.computedAt ? <p class="hint">Evaluated {relativeTime(d.computedAt)}.</p> : null}
          </>
        ) : null}
      </PageContent>
    </>
  );
}
