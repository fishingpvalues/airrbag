import type { Decision, GuardEvent, GuardLog } from "../api";
import { Alert, Column, DescriptionList, ErrorState, FieldSet, Label, Loading, PageContent, PageToolbar, Table, ToolbarButton, type LabelKind } from "../components";
import { useResource } from "../hooks";
import { relativeTime } from "../../shared/format";

const DECISION: Record<Decision, [string, LabelKind, string]> = {
  blocked: ["Blocked", "danger", "Answered 409: a private seed is still owed"],
  "would-block": ["Would block", "warning", "Dry run: logged and let through"],
  overridden: ["Overridden", "info", "Confirmed in the Airrbag dialog or with X-Airrbag-Override"],
  "error-closed": ["Failed closed", "danger", "Could not verify the delete; refused"],
  "error-open": ["Failed open", "warning", "Could not verify the delete; let through"],
};

const columns: Column<GuardEvent>[] = [
  {
    key: "time",
    label: "Time",
    render: (e) => <span title={new Date(e.time).toLocaleString()}>{relativeTime(e.time)}</span>,
  },
  { key: "instance", label: "Instance", render: (e) => e.instance },
  {
    key: "decision",
    label: "Decision",
    render: (e) => {
      const [label, kind, help] = DECISION[e.decision] || [e.decision, "default", ""];
      return (
        <Label kind={kind} title={help}>
          {label}
        </Label>
      );
    },
  },
  {
    key: "request",
    label: "Request",
    class: "cell-title",
    render: (e) => (
      <code class="request">
        {e.method} {e.path}
      </code>
    ),
  },
  { key: "files", label: "Files", class: "num", render: (e) => e.files || <span class="muted">-</span> },
  {
    key: "what",
    label: "Affected",
    class: "cell-title",
    render: (e) => (e.titles && e.titles.length ? e.titles.join(", ") : <span class="muted">-</span>),
  },
  {
    key: "reason",
    label: "Reason",
    class: "cell-why",
    render: (e) => (
      <>
        {e.reason}
        {e.override ? <div class="muted">override: {e.override}</div> : null}
      </>
    ),
  },
];

export function GuardPage() {
  const r = useResource<GuardLog>("guard");
  const d = r.data;
  return (
    <>
      <PageToolbar>
        <ToolbarButton icon="refresh" label="Refresh" onClick={r.reload} spinning={r.loading} />
      </PageToolbar>
      <PageContent>
        {r.error ? <ErrorState error={r.error} /> : null}
        {!d && !r.error ? <Loading /> : null}
        {d ? (
          <>
            {d.dryRun ? <Alert kind="warning">Dry run: decisions are recorded below, no delete is blocked.</Alert> : null}
            <FieldSet legend="Guard">
              <DescriptionList
                items={[
                  ["State", d.guard ? <Label kind="success">On</Label> : <Label kind="danger">Off</Label>],
                  ["Mode", d.dryRun ? <Label kind="warning">Dry run</Label> : <Label kind="success">Blocking</Label>],
                  ["When a client is unreachable", d.failClosed ? "Private torrents are kept (fail closed)" : "Deletes pass (fail open)"],
                  [
                    "Since start",
                    <span class="label-list">
                      {(Object.keys(DECISION) as Decision[]).map((k) => (
                        <Label key={k} kind={DECISION[k][1]} outline>
                          {`${d.counts[k] || 0} ${DECISION[k][0].toLowerCase()}`}
                        </Label>
                      ))}
                    </span>,
                  ],
                ]}
              />
            </FieldSet>
            <FieldSet legend="Recent decisions">
              <Table
                columns={columns}
                rows={d.events}
                rowKey={(e) => `${e.time}:${e.path}:${e.decision}`}
                empty="No delete has touched a kept file since Airrbag started"
              />
              <p class="hint">Kept in memory (last 500); the same decisions are in the log and in /metrics.</p>
            </FieldSet>
          </>
        ) : null}
      </PageContent>
    </>
  );
}
