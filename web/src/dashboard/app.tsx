import { useEffect, useState } from "preact/hooks";
import { appName, env } from "./api";
import { Icon } from "./icons";
import { href, useHashRoute } from "./hooks";
import { FilesPage } from "./pages/files";
import { GuardPage } from "./pages/guard";
import { OverviewPage } from "./pages/overview";
import { SettingsPage } from "./pages/settings";
import { SystemPage } from "./pages/system";

const NAV = [
  { name: "overview", label: "Overview", icon: "overview" },
  { name: "files", label: "Files", icon: "files" },
  { name: "guard", label: "Guard", icon: "guard" },
  { name: "settings", label: "Settings", icon: "settings" },
  { name: "system", label: "System", icon: "system" },
];

export function App() {
  const route = useHashRoute();
  const [open, setOpen] = useState(false);

  useEffect(() => {
    setOpen(false);
    const page = NAV.find((n) => n.name === route.name);
    document.title = `${page ? page.label : "Airrbag"} - Airrbag`;
  }, [route.name]);

  let page;
  switch (route.name) {
    case "files":
      page = <FilesPage route={route} />;
      break;
    case "guard":
      page = <GuardPage />;
      break;
    case "settings":
      page = <SettingsPage />;
      break;
    case "system":
      page = <SystemPage />;
      break;
    default:
      page = <OverviewPage />;
  }

  return (
    <div class="layout">
      <a class="skip-link" href="#main">
        Skip to content
      </a>
      <header class="header">
        <button type="button" class="header-toggle" aria-label="Toggle navigation" aria-expanded={open} onClick={() => setOpen(!open)}>
          <Icon name="bars" size={20} />
        </button>
        <a class="header-logo" href={href("overview")} aria-label="Airrbag overview">
          <img src={`${env.base}/static/logo.svg`} width={32} height={32} alt="" />
          <span>Airrbag</span>
        </a>
        <div class="header-spacer" />
        <span class="header-context" title={`This dashboard runs in front of ${env.instance}`}>
          {env.instance} <span class="muted">{appName(env.app)}</span>
        </span>
        <a class="header-action" href={env.arrHome} title={`Back to ${appName(env.app)}`}>
          <Icon name="external" size={18} />
          <span class="sr-only">Back to {appName(env.app)}</span>
        </a>
      </header>
      <div class="body">
        <nav class={`sidebar${open ? " sidebar-open" : ""}`} aria-label="Airrbag">
          <ul>
            {NAV.map((n) => {
              const active = route.name === n.name || (n.name === "overview" && !NAV.some((x) => x.name === route.name));
              return (
                <li key={n.name}>
                  <a class={`nav-item${active ? " active" : ""}`} href={href(n.name)} aria-current={active ? "page" : undefined}>
                    <Icon name={n.icon} size={18} />
                    <span>{n.label}</span>
                  </a>
                </li>
              );
            })}
          </ul>
          <div class="sidebar-footer">airrbag {env.version}</div>
        </nav>
        {open ? <div class="sidebar-backdrop" onClick={() => setOpen(false)} /> : null}
        <main class="main" id="main" tabIndex={-1}>
          {page}
        </main>
      </div>
    </div>
  );
}
