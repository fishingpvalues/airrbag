import { useState } from "preact/hooks";
import { getJSON, type ClientsHealth, type Settings } from "../api";
import { Alert, DescriptionList, ErrorState, FieldSet, Label, Loading, PageContent, PageToolbar, StatusLabel, Table, ToolbarButton } from "../components";
import { useResource } from "../hooks";
import { relativeTime } from "../../shared/format";

function Secret(props: { value: string }) {
  return props.value ? <Label kind="success">Set</Label> : <Label kind="default" outline>Not set</Label>;
}

export function SettingsPage() {
  const r = useResource<Settings>("settings");
  const [health, setHealth] = useState<ClientsHealth | null>(null);
  const [testing, setTesting] = useState(false);
  const [testErr, setTestErr] = useState<Error | null>(null);
  const test = () => {
    setTesting(true);
    setTestErr(null);
    getJSON<ClientsHealth>("clients?fresh=1")
      .then(setHealth)
      .catch(setTestErr)
      .finally(() => setTesting(false));
  };
  const d = r.data;
  return (
    <>
      <PageToolbar>
        <ToolbarButton icon="refresh" label="Refresh" onClick={r.reload} spinning={r.loading} />
        <ToolbarButton icon="plug" label="Test clients" onClick={test} spinning={testing} disabled={testing} />
      </PageToolbar>
      <PageContent>
        <Alert kind="info">Read-only. Airrbag is configured in airrbag.yml; secrets are never sent to the browser.</Alert>
        {r.error ? <ErrorState error={r.error} /> : null}
        {!d && !r.error ? <Loading /> : null}
        {testErr ? <ErrorState error={testErr} /> : null}
        {health ? (
          <FieldSet legend={`Client connectivity (checked ${relativeTime(health.checkedAt)})`}>
            <Table
              columns={[
                { key: "instance", label: "Instance", render: (x: [string, string, string]) => x[0] },
                { key: "client", label: "Client", render: (x: [string, string, string]) => x[1] },
                { key: "status", label: "Status", render: (x: [string, string, string]) => <StatusLabel status={x[2]} /> },
                { key: "detail", label: "Detail", class: "cell-why", render: (x: [string, string, string]) => (x[2] === "ok" ? "" : x[2]) },
              ]}
              rows={Object.entries(health.instances).flatMap(([i, cs]) => Object.entries(cs).map(([c, s]) => [i, c, s] as [string, string, string]))}
              rowKey={(x) => `${x[0]}:${x[1]}`}
              empty="No download client is configured or discovered"
            />
          </FieldSet>
        ) : null}
        {d ? (
          <>
            <FieldSet legend="Instances">
              <Table
                columns={[
                  { key: "name", label: "Name", render: (i: Settings["instances"][number]) => <span class="cell-strong">{i.name}</span> },
                  { key: "listen", label: "Listen", render: (i: Settings["instances"][number]) => <code>{i.listen}</code> },
                  { key: "upstream", label: "Upstream", render: (i: Settings["instances"][number]) => <code>{i.upstream}</code> },
                  { key: "app", label: "App", render: (i: Settings["instances"][number]) => i.app || "auto" },
                  { key: "key", label: "API key", render: (i: Settings["instances"][number]) => <Secret value={i.apiKey} /> },
                ]}
                rows={d.instances}
                rowKey={(i) => i.name}
              />
            </FieldSet>
            <FieldSet legend="Download clients">
              <Table
                columns={[
                  { key: "name", label: "Name", render: (c: Settings["clients"][number]) => <span class="cell-strong">{c.name || "(any)"}</span> },
                  { key: "type", label: "Type", render: (c: Settings["clients"][number]) => c.type },
                  { key: "url", label: "URL", render: (c: Settings["clients"][number]) => (c.url ? <code>{c.url}</code> : <span class="muted">discovered</span>) },
                  { key: "user", label: "Username", render: (c: Settings["clients"][number]) => c.username || <span class="muted">-</span> },
                  { key: "pw", label: "Password", render: (c: Settings["clients"][number]) => <Secret value={c.password} /> },
                  { key: "key", label: "API key", render: (c: Settings["clients"][number]) => <Secret value={c.apiKey} /> },
                ]}
                rows={d.clients}
                rowKey={(c) => c.name + c.type}
                empty="None configured: clients are discovered from the *Arrs"
              />
            </FieldSet>
            <FieldSet legend="Path mappings">
              <Table
                columns={[
                  { key: "source", label: "Source", render: (m: Settings["pathMappings"][number]) => m.source || "any" },
                  { key: "from", label: "From", render: (m: Settings["pathMappings"][number]) => <code>{m.from}</code> },
                  { key: "to", label: "To", render: (m: Settings["pathMappings"][number]) => <code>{m.to}</code> },
                ]}
                rows={d.pathMappings}
                rowKey={(m) => `${m.source}:${m.from}`}
                empty="No mappings: paths are used as the *Arrs and clients report them"
              />
            </FieldSet>
            <FieldSet legend="Private trackers">
              <DescriptionList
                items={[
                  ["Roster file", d.trackers.file ? <code>{d.trackers.file}</code> : <span class="muted">none</span>],
                  ["Extra private domains", (d.trackers.private || []).join(", ") || <span class="muted">none</span>],
                  [
                    "Default obligation",
                    d.trackers.default
                      ? `seed ${d.trackers.default.minSeedTime}${d.trackers.default.minRatio ? ` or ratio ${d.trackers.default.minRatio}` : ""}`
                      : "none: an unmatched private torrent is never considered done",
                  ],
                ]}
              />
              {(d.trackers.rules || []).length ? (
                <Table
                  columns={[
                    { key: "domains", label: "Domains", class: "cell-title", render: (x: NonNullable<Settings["trackers"]["rules"]>[number]) => x.domains.join(", ") },
                    { key: "seed", label: "Min seed time", render: (x: NonNullable<Settings["trackers"]["rules"]>[number]) => x.minSeedTime },
                    { key: "ratio", label: "Min ratio", class: "num", render: (x: NonNullable<Settings["trackers"]["rules"]>[number]) => x.minRatio || "-" },
                    { key: "both", label: "Both required", render: (x: NonNullable<Settings["trackers"]["rules"]>[number]) => (x.requireBoth ? "yes" : "no") },
                  ]}
                  rows={d.trackers.rules || []}
                  rowKey={(x) => x.domains.join(",")}
                />
              ) : null}
            </FieldSet>
            <FieldSet legend="Guard and general">
              <DescriptionList
                items={[
                  ["Guard", d.guard.enabled ? "on" : "off"],
                  ["Dry run", d.guard.dryRun ? "yes" : "no"],
                  ["Fail closed", d.guard.failClosed ? "yes" : "no"],
                  ["Dialog grant valid for", d.guard.grantTtl],
                  ["Cache TTL", d.cacheTtl],
                  ["History limit", d.historyLimit ? d.historyLimit.toLocaleString() : "default"],
                  ["Log level", d.logLevel || "info"],
                ]}
              />
            </FieldSet>
          </>
        ) : null}
      </PageContent>
    </>
  );
}
