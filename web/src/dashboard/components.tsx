import type { ComponentChildren, JSX } from "preact";
import { LOCK_PATH, SOURCE_LABEL, VERDICT_HELP, VERDICT_LABEL, sourceOf } from "../shared/tokens";
import { formatDuration } from "../shared/format";
import type { FileVerdict, Verdict } from "../types";
import { Icon } from "./icons";

// --- labels ------------------------------------------------------------------

export type LabelKind = "success" | "warning" | "danger" | "info" | "default";

export function Label(props: { kind?: LabelKind; outline?: boolean; title?: string; children: ComponentChildren }) {
  return (
    <span class={`ab-label ab-k-${props.kind || "default"}${props.outline ? " ab-outline" : ""}`} title={props.title}>
      {props.children}
    </span>
  );
}

export function VerdictLabel(props: { verdict: Verdict; count?: number }) {
  return (
    <span class={`ab-label ab-v-${props.verdict}`} title={VERDICT_HELP[props.verdict]}>
      {props.count != null ? `${props.count} ` : ""}
      {VERDICT_LABEL[props.verdict]}
    </span>
  );
}

function Lock() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d={LOCK_PATH} />
    </svg>
  );
}

export function SourceLabel(props: { file: Pick<FileVerdict, "protocol" | "private">; title?: string }) {
  const src = sourceOf(props.file);
  return (
    <span class={`ab-label ab-s-${src}`} title={props.title}>
      {src === "private" ? <Lock /> : null}
      {SOURCE_LABEL[src]}
    </span>
  );
}

export function StatusLabel(props: { status: string }) {
  const ok = props.status === "ok";
  return (
    <Label kind={ok ? "success" : "danger"} title={ok ? "reachable" : props.status}>
      {ok ? "OK" : "Error"}
    </Label>
  );
}

// SeedProgress shows seeding time against the tracker's requirement, the way
// the *Arr queue shows progress: a thin bar plus the numbers.
export function SeedProgress(props: { file: FileVerdict }) {
  const f = props.file;
  if (f.seedingTimeSeconds == null) return <span class="muted">-</span>;
  const req = f.requiredSeedTimeSeconds;
  const pct = req ? Math.min(100, (f.seedingTimeSeconds / req) * 100) : f.obligationMet ? 100 : 0;
  const kind = f.obligationMet ? "met" : f.private ? "owed" : "public";
  return (
    <div class="seed" title={req ? `${formatDuration(f.seedingTimeSeconds)} of ${formatDuration(req)}` : formatDuration(f.seedingTimeSeconds)}>
      <span class="seed-text">
        {formatDuration(f.seedingTimeSeconds)}
        {req ? <span class="muted"> / {formatDuration(req)}</span> : null}
      </span>
      {f.private ? (
        <span class={`seed-bar seed-${kind}`}>
          <span style={{ width: `${pct}%` }} />
        </span>
      ) : null}
    </div>
  );
}

// --- page chrome -------------------------------------------------------------

export function PageToolbar(props: { children: ComponentChildren; right?: ComponentChildren }) {
  return (
    <div class="toolbar" role="toolbar">
      <div class="toolbar-section">{props.children}</div>
      {props.right ? <div class="toolbar-section toolbar-right">{props.right}</div> : null}
    </div>
  );
}

export function ToolbarButton(props: { icon: string; label: string; onClick?: () => void; href?: string; spinning?: boolean; disabled?: boolean }) {
  const inner = (
    <>
      <span class={props.spinning ? "spin" : undefined}>
        <Icon name={props.icon} size={21} />
      </span>
      <span class="toolbar-label">{props.label}</span>
    </>
  );
  if (props.href) {
    return (
      <a class="toolbar-button" href={props.href} target="_blank" rel="noopener noreferrer">
        {inner}
      </a>
    );
  }
  return (
    <button type="button" class="toolbar-button" onClick={props.onClick} disabled={props.disabled}>
      {inner}
    </button>
  );
}

export function PageContent(props: { children: ComponentChildren }) {
  return <div class="content">{props.children}</div>;
}

export function FieldSet(props: { legend: string; children: ComponentChildren }) {
  return (
    <fieldset class="fieldset">
      <legend>{props.legend}</legend>
      {props.children}
    </fieldset>
  );
}

export function Alert(props: { kind: "info" | "warning" | "danger" | "success"; children: ComponentChildren }) {
  return (
    <div class={`alert alert-${props.kind}`} role={props.kind === "danger" ? "alert" : "status"}>
      {props.children}
    </div>
  );
}

export function Loading(props: { text?: string }) {
  return (
    <div class="loading" role="status">
      <span class="loading-ripple" aria-hidden="true" />
      <span>{props.text || "Loading"}</span>
    </div>
  );
}

export function ErrorState(props: { error: Error }) {
  return <Alert kind="danger">{props.error.message}</Alert>;
}

export function DescriptionList(props: { items: [string, ComponentChildren][] }) {
  return (
    <dl class="dl">
      {props.items.map(([k, v]) => (
        <div class="dl-row" key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

// --- table -------------------------------------------------------------------

export interface Column<T> {
  key: string;
  label: string;
  sortable?: boolean;
  class?: string;
  render: (row: T) => ComponentChildren;
}

export function Table<T>(props: {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  sortKey?: string;
  sortDir?: "ascending" | "descending";
  onSort?: (key: string) => void;
  empty?: string;
}): JSX.Element {
  return (
    <div class="table-wrap">
      <table class="table">
        <thead>
          <tr>
            {props.columns.map((c) => {
              const active = props.sortKey === c.key;
              const aria = active ? (props.sortDir === "ascending" ? "ascending" : "descending") : undefined;
              return (
                <th key={c.key} class={c.class} aria-sort={c.sortable ? aria || "none" : undefined}>
                  {c.sortable && props.onSort ? (
                    <button type="button" class={`th-sort${active ? " active" : ""}`} onClick={() => props.onSort!(c.key)}>
                      {c.label}
                      <span class="sort-caret" aria-hidden="true">
                        {active ? (props.sortDir === "ascending" ? "▲" : "▼") : ""}
                      </span>
                    </button>
                  ) : (
                    c.label
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {props.rows.length === 0 ? (
            <tr>
              <td class="table-empty" colSpan={props.columns.length}>
                {props.empty || "Nothing to show"}
              </td>
            </tr>
          ) : (
            props.rows.map((r) => (
              <tr key={props.rowKey(r)}>
                {props.columns.map((c) => (
                  <td key={c.key} class={c.class}>
                    {c.render(r)}
                  </td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}

export function Pager(props: { page: number; totalPages: number; totalRecords: number; onPage: (p: number) => void }) {
  const { page, totalPages } = props;
  const btn = (label: string, to: number, disabled: boolean, aria: string) => (
    <button type="button" class="pager-button" disabled={disabled} aria-label={aria} onClick={() => props.onPage(to)}>
      {label}
    </button>
  );
  return (
    <div class="pager">
      <div class="pager-controls">
        {btn("«", 1, page <= 1, "First page")}
        {btn("‹", page - 1, page <= 1, "Previous page")}
        <span class="pager-text">
          Page {page} of {totalPages}
        </span>
        {btn("›", page + 1, page >= totalPages, "Next page")}
        {btn("»", totalPages, page >= totalPages, "Last page")}
      </div>
      <div class="pager-total">Total records: {props.totalRecords.toLocaleString()}</div>
    </div>
  );
}
