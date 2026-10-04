import { appName, env, type SystemInfo } from "../api";
import { DescriptionList, ErrorState, FieldSet, Loading, PageContent, PageToolbar, Table, ToolbarButton } from "../components";
import { useResource } from "../hooks";
import { formatDuration, relativeTime } from "../../shared/format";

export function SystemPage() {
  const r = useResource<SystemInfo>("system");
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
            <FieldSet legend="About">
              <DescriptionList
                items={[
                  ["Version", d.version],
                  ["Go", d.goVersion],
                  ["Platform", `${d.os}/${d.arch}`],
                  ["Started", new Date(d.startedAt).toLocaleString()],
                  ["Uptime", formatDuration(d.uptimeSeconds)],
                  ["This listener", `${d.instance} (${appName(env.app)})`],
                  [
                    "Endpoints",
                    <span class="label-list">
                      <a href={d.healthPath} target="_blank" rel="noopener noreferrer">
                        health
                      </a>
                      <a href={d.metricsPath} target="_blank" rel="noopener noreferrer">
                        metrics
                      </a>
                      <a href="https://github.com/fishingpvalues/airrbag" target="_blank" rel="noopener noreferrer">
                        source
                      </a>
                    </span>,
                  ],
                ]}
              />
            </FieldSet>
            <FieldSet legend="Instances">
              <Table
                columns={[
                  { key: "name", label: "Name", render: (i: SystemInfo["instances"][number]) => <span class="cell-strong">{i.name}</span> },
                  { key: "app", label: "App", render: (i: SystemInfo["instances"][number]) => `${appName(i.app)} ${i.appVersion}` },
                  { key: "listen", label: "Listen", render: (i: SystemInfo["instances"][number]) => <code>{i.listen || "-"}</code> },
                  { key: "indexed", label: "History index", class: "num", render: (i: SystemInfo["instances"][number]) => i.indexedFiles.toLocaleString() },
                  { key: "at", label: "Indexed", render: (i: SystemInfo["instances"][number]) => relativeTime(i.indexedAt) },
                  { key: "err", label: "Error", class: "cell-why", render: (i: SystemInfo["instances"][number]) => i.indexError || "" },
                ]}
                rows={d.instances}
                rowKey={(i) => i.name}
              />
            </FieldSet>
          </>
        ) : null}
      </PageContent>
    </>
  );
}
