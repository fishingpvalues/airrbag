import { appName, env, type InstanceSummary, type Overview } from "../api";
import { Alert, Column, ErrorState, FieldSet, Loading, PageContent, PageToolbar, StatusLabel, Table, ToolbarButton, VerdictLabel } from "../components";
import { href, useResource } from "../hooks";
import { formatSize, relativeTime } from "../../shared/format";
import { VERDICT_HELP, VERDICT_LABEL } from "../../shared/tokens";
import type { Verdict } from "../../types";

const ORDER: Verdict[] = ["keep", "frees-nothing", "safe", "unknown"];

function Tile(props: { verdict: Verdict; count: number; bytes: number }) {
  const v = props.verdict;
  const sub =
    v === "safe"
      ? `${formatSize(props.bytes)} freed by deleting`
      : v === "frees-nothing"
        ? `${formatSize(props.bytes)} held by seeds`
        : formatSize(props.bytes);
  return (
    <a class={`tile tile-${v}`} href={href("files", { verdict: v })}>
      <span class="tile-title">{VERDICT_LABEL[v]}</span>
      <span class="tile-value">{props.count.toLocaleString()}</span>
      <span class="tile-sub">{sub}</span>
      <span class="tile-help">{VERDICT_HELP[v]}</span>
    </a>
  );
}

const columns: Column<InstanceSummary>[] = [
  {
    key: "name",
    label: "Instance",
    render: (i) => (
      <span class="cell-strong">
        {i.name}
        {i.current ? <span class="muted"> (this)</span> : null}
      </span>
    ),
  },
  { key: "app", label: "App", render: (i) => `${appName(i.app)} ${i.appVersion}` },
  { key: "files", label: "Files", class: "num", render: (i) => (i.computing && !i.computedAt ? "..." : i.files.toLocaleString()) },
  ...ORDER.map(
    (v): Column<InstanceSummary> => ({
      key: v,
      label: VERDICT_LABEL[v],
      class: "num",
      render: (i) => {
        const n = i.counts[v] || 0;
        return n ? (
          <a href={href("files", { instance: i.name, verdict: v })}>
            <VerdictLabel verdict={v} count={n} />
          </a>
        ) : (
          <span class="muted">0</span>
        );
      },
    }),
  ),
  { key: "free", label: "Freeable", class: "num", render: (i) => formatSize(i.bytes.safe || 0) },
  {
    key: "clients",
    label: "Clients",
    render: (i) => {
      const names = Object.keys(i.clients || {});
      if (!names.length) return <span class="muted">none</span>;
      return (
        <span class="label-list">
          {names.map((n) => (
            <span key={n} class="client">
              <StatusLabel status={i.clients[n]} /> {n}
            </span>
          ))}
        </span>
      );
    },
  },
  {
    key: "updated",
    label: "Evaluated",
    render: (i) => (i.computing ? <span class="muted">evaluating...</span> : relativeTime(i.computedAt)),
  },
];

export function OverviewPage() {
  const r = useResource<Overview>("overview", (d) => (d.instances.some((i) => i.computing) ? 4000 : false));
  const d = r.data;
  return (
    <>
      <PageToolbar
        right={<ToolbarButton icon="external" label={appName(env.app)} href={env.arrHome} />}
      >
        <ToolbarButton icon="refresh" label="Refresh" onClick={r.reload} spinning={r.loading} />
      </PageToolbar>
      <PageContent>
        {r.error ? <ErrorState error={r.error} /> : null}
        {!d && !r.error ? <Loading /> : null}
        {d ? (
          <>
            {d.dryRun ? (
              <Alert kind="warning">
                The guard runs in dry-run mode: deletes of kept files are logged on the Guard page, nothing is blocked.
              </Alert>
            ) : !d.guard ? (
              <Alert kind="danger">The guard is off. Deletes are not checked.</Alert>
            ) : null}
            <div class="tiles">
              {ORDER.map((v) => (
                <Tile key={v} verdict={v} count={d.totals.counts[v] || 0} bytes={d.totals.bytes[v] || 0} />
              ))}
            </div>
            <FieldSet legend="Instances">
              <Table columns={columns} rows={d.instances} rowKey={(i) => i.name} empty="No instance is ready yet" />
              {d.instances.some((i) => i.computing) ? (
                <p class="hint">Evaluating the library. Large libraries take a minute; this page refreshes itself.</p>
              ) : null}
              {d.instances.flatMap((i) => (i.indexError ? [<Alert key={i.name} kind="danger">{`${i.name}: ${i.indexError}`}</Alert>] : []))}
            </FieldSet>
          </>
        ) : null}
      </PageContent>
    </>
  );
}
