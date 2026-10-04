// Source badges on detail pages: a compact panel near the page header, plus
// best-effort badges next to any table cell showing a file name.

import { APPS, api, cfg } from "./config";
import type { FileVerdict } from "./types";
import { BASE_CSS, COLORS, el, formatSize, sourceBadge, verdictBadge } from "./ui";

const PANEL_CSS = `
.p{margin:10px 0;background:rgba(32,32,32,.92);border:1px solid #3a3a3a;border-left:4px solid var(--c);border-radius:4px;color:#e1e2e3;font-size:13px}
.p.float{position:fixed;right:16px;bottom:16px;z-index:2147483000;max-width:min(560px,92vw);margin:0}
.s{display:flex;gap:8px;align-items:center;flex-wrap:wrap;padding:8px 12px;cursor:pointer;user-select:none}
.s strong{font-weight:600;margin-right:4px}
.l{display:none;border-top:1px solid #3a3a3a;max-height:50vh;overflow:auto}
.p.open .l{display:block}
.row{display:flex;gap:6px;align-items:center;padding:6px 12px;border-top:1px solid #333}
.row:first-child{border-top:0}
.n{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#c0c1c2}
.z{color:#909293;font-size:12px;white-space:nowrap}
`;

const cache = new Map<string, { at: number; files: FileVerdict[]; parentId: number }>();
const TTL = 60_000;
let renderToken = 0;

async function load(path: string): Promise<{ parentId: number; files: FileVerdict[] } | null> {
  const hit = cache.get(path);
  if (hit && Date.now() - hit.at < TTL) return hit;
  const r = await api("/resolve?path=" + encodeURIComponent(path));
  if (!r.ok) return null;
  const { parentId } = (await r.json()) as { parentId: number };
  if (!parentId) return null;
  const f = await api("/files?parentId=" + parentId);
  if (!f.ok) return null;
  const { files } = (await f.json()) as { files: FileVerdict[] };
  const entry = { at: Date.now(), parentId, files: files || [] };
  cache.set(path, entry);
  return entry;
}

function summaryColor(files: FileVerdict[]): string {
  if (files.some((f) => f.verdict === "keep")) return COLORS.keep;
  if (files.some((f) => f.protocol === "torrent" && f.private)) return COLORS.private;
  if (files.some((f) => f.protocol === "torrent")) return COLORS.torrent;
  if (files.some((f) => f.protocol === "usenet")) return COLORS.usenet;
  return COLORS.unknown;
}

function buildPanel(files: FileVerdict[], floating: boolean): HTMLElement {
  const host = el("div");
  host.setAttribute("data-airrbag", "panel");
  const root = host.attachShadow({ mode: "open" });
  const style = el("style");
  style.textContent = BASE_CSS + PANEL_CSS;
  root.appendChild(style);

  const p = el("div", "p" + (floating ? " float" : ""));
  p.style.setProperty("--c", summaryColor(files));
  const s = el("div", "s");
  s.appendChild(el("strong", undefined, "Airrbag"));
  const counts: Record<string, number> = {};
  files.forEach((f) => (counts[f.verdict] = (counts[f.verdict] || 0) + 1));
  (["keep", "frees-nothing", "safe", "unknown"] as const).forEach((k) => {
    if (!counts[k]) return;
    const sample = files.find((f) => f.verdict === k)!;
    const b = verdictBadge(sample);
    b.textContent = `${counts[k]} ${b.textContent}`;
    s.appendChild(b);
  });
  if (files.length === 0) s.appendChild(el("span", "z", "no files on disk"));
  p.appendChild(s);

  const l = el("div", "l");
  files.forEach((f) => {
    const row = el("div", "row");
    row.appendChild(sourceBadge(f));
    row.appendChild(verdictBadge(f));
    const name = el("span", "n", f.relativePath || f.path);
    name.title = f.path;
    row.appendChild(name);
    row.appendChild(el("span", "z", formatSize(f.size)));
    l.appendChild(row);
  });
  p.appendChild(l);
  s.onclick = () => p.classList.toggle("open");
  root.appendChild(p);
  return host;
}

// Tag table cells whose text is a file's name with a small source badge.
function tagCells(files: FileVerdict[]): void {
  const byName = new Map<string, FileVerdict>();
  files.forEach((f) => {
    const name = (f.relativePath || f.path).split("/").pop();
    if (name) byName.set(name, f);
    if (f.relativePath) byName.set(f.relativePath, f);
  });
  document.querySelectorAll("td, [class*='TableRowCell']").forEach((cell) => {
    if (cell.querySelector("[data-airrbag='cell']")) return;
    const text = (cell.textContent || "").trim();
    const f = byName.get(text) || byName.get(text.split("/").pop() || "");
    if (!f) return;
    const host = el("span");
    host.setAttribute("data-airrbag", "cell");
    host.style.marginRight = "6px";
    const root = host.attachShadow({ mode: "open" });
    const style = el("style");
    style.textContent = BASE_CSS;
    root.appendChild(style);
    root.appendChild(sourceBadge(f));
    cell.insertBefore(host, cell.firstChild);
  });
}

function currentUI() {
  const uis = APPS[cfg.app] || [];
  return uis.find((u) => u.route.test(location.pathname));
}

async function render(): Promise<void> {
  const ui = currentUI();
  const token = ++renderToken;
  if (!ui) {
    document.querySelectorAll("[data-airrbag='panel']").forEach((n) => n.remove());
    return;
  }
  let data;
  try {
    data = await load(location.pathname);
  } catch {
    return;
  }
  if (!data || token !== renderToken) return;
  const existing = document.querySelector("[data-airrbag='panel']");
  if (existing && existing.getAttribute("data-path") === location.pathname) {
    tagCells(data.files);
    return;
  }
  if (existing) existing.remove();
  const anchor = ui.anchors.map((s) => document.querySelector(s)).find(Boolean);
  const panel = buildPanel(data.files, !anchor);
  panel.setAttribute("data-path", location.pathname);
  if (anchor && anchor.parentElement) {
    anchor.parentElement.insertBefore(panel, anchor.nextSibling);
  } else {
    document.body.appendChild(panel);
  }
  tagCells(data.files);
}

let timer: number | undefined;
function schedule(): void {
  if (timer !== undefined) window.clearTimeout(timer);
  timer = window.setTimeout(() => {
    timer = undefined;
    render().catch(() => undefined);
  }, 300);
}

export function startBadges(): void {
  for (const m of ["pushState", "replaceState"] as const) {
    const orig = history[m];
    history[m] = function (this: History, ...args: Parameters<History["pushState"]>) {
      const r = orig.apply(this, args);
      schedule();
      return r;
    } as History["pushState"];
  }
  window.addEventListener("popstate", schedule);
  // React re-renders can drop the panel; put it back when the DOM settles.
  new MutationObserver((muts) => {
    if (muts.some((m) => Array.from(m.addedNodes).some((n) => !(n instanceof HTMLElement && n.dataset.airrbag)))) {
      schedule();
    }
  }).observe(document.body, { childList: true, subtree: true });
  schedule();
}
