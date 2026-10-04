// Shared rendering: colors, badges, tooltips and the modal. Everything renders
// inside a shadow root so the *Arr's CSS and ours never touch.

import type { FileVerdict } from "./types";

export const COLORS = {
  usenet: "#5d9cec",
  torrent: "#ff902b",
  private: "#f05050",
  unknown: "#909293",
  keep: "#f05050",
  "frees-nothing": "#a46bd6",
  safe: "#27c24c",
};

export const BASE_CSS = `
:host{all:initial}
*{box-sizing:border-box;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
.b{display:inline-flex;align-items:center;gap:4px;padding:1px 7px;border-radius:3px;font-size:11px;font-weight:600;color:#fff;white-space:nowrap;line-height:18px}
.b svg{width:10px;height:10px;fill:currentColor}
`;

const SVG = "http://www.w3.org/2000/svg";

function lockIcon(): SVGElement {
  const svg = document.createElementNS(SVG, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(SVG, "path");
  path.setAttribute("d", "M12 1a5 5 0 0 0-5 5v4H5v13h14V10h-2V6a5 5 0 0 0-5-5zm-3 9V6a3 3 0 0 1 6 0v4H9z");
  svg.appendChild(path);
  return svg;
}

export function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

export function formatDuration(s: number): string {
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  return d > 0 ? `${d}d ${h}h` : `${h}h ${Math.floor((s % 3600) / 60)}m`;
}

export function formatSize(n: number): string {
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i ? 1 : 0)} ${u[i]}`;
}

export function tooltip(f: FileVerdict): string {
  const lines = [`Verdict: ${f.verdict}`];
  if (f.indexer) lines.push(`Indexer: ${f.indexer}`);
  if (f.client) lines.push(`Client: ${f.client}`);
  if (f.tracker) lines.push(`Tracker: ${f.tracker}`);
  if (f.ratio != null) lines.push(`Ratio: ${f.ratio.toFixed(2)}`);
  if (f.seedingTimeSeconds != null) lines.push(`Seeding: ${formatDuration(f.seedingTimeSeconds)}`);
  if (f.relation && f.relation !== "unknown") lines.push(`Relation to seed: ${f.relation}`);
  return lines.concat(f.reasons || []).join("\n");
}

export function sourceBadge(f: FileVerdict): HTMLElement {
  const isPrivate = f.protocol === "torrent" && f.private;
  const color = isPrivate ? COLORS.private : COLORS[f.protocol] || COLORS.unknown;
  const label = f.protocol === "usenet" ? "Usenet" : f.protocol === "torrent" ? (isPrivate ? "Private" : "Torrent") : "Unknown";
  const b = el("span", "b");
  b.style.background = color;
  if (isPrivate) b.appendChild(lockIcon());
  b.appendChild(document.createTextNode(label));
  b.title = tooltip(f);
  return b;
}

const VERDICT_LABEL: Record<string, string> = {
  keep: "Keep",
  "frees-nothing": "Frees nothing",
  safe: "Safe to delete",
  unknown: "Unknown",
};

export function verdictBadge(f: FileVerdict): HTMLElement {
  const b = el("span", "b", VERDICT_LABEL[f.verdict] || f.verdict);
  b.style.background = COLORS[f.verdict] || COLORS.unknown;
  b.title = tooltip(f);
  return b;
}

// --- modal -------------------------------------------------------------------

const MODAL_CSS = `
.ov{position:fixed;inset:0;background:rgba(0,0,0,.6);display:flex;align-items:center;justify-content:center;z-index:2147483647}
.m{background:#2a2a2a;color:#e1e2e3;border-radius:4px;width:min(640px,94vw);max-height:86vh;display:flex;flex-direction:column;box-shadow:0 8px 32px rgba(0,0,0,.5)}
.h{padding:14px 18px;border-bottom:1px solid #3a3a3a;font-size:17px;display:flex;gap:10px;align-items:center}
.h .bar{width:4px;align-self:stretch;border-radius:2px}
.c{padding:14px 18px;overflow:auto;font-size:14px;line-height:1.45}
.c ul{margin:10px 0 0;padding:0;list-style:none}
.c li{padding:8px 0;border-top:1px solid #3a3a3a}
.c li .p{word-break:break-all;color:#c0c1c2;font-size:12px;margin-top:3px}
.c li .r{color:#909293;font-size:12px;margin-top:3px}
.c label{display:flex;gap:8px;align-items:flex-start;margin-top:14px;cursor:pointer}
.f{padding:12px 18px;border-top:1px solid #3a3a3a;display:flex;justify-content:flex-end;gap:10px}
button{border:0;border-radius:4px;padding:7px 14px;font-size:14px;cursor:pointer;color:#fff;background:#4a4b4c}
button.danger{background:#f05050}button.primary{background:#5d9cec}
button:disabled{opacity:.45;cursor:not-allowed}
`;

export interface ModalSpec {
  title: string;
  color: string;
  intro: string;
  files: FileVerdict[];
  confirmLabel: string;
  danger: boolean;
  requireAck?: string;
}

export function modal(spec: ModalSpec): Promise<boolean> {
  return new Promise((resolve) => {
    const host = el("div");
    host.setAttribute("data-airrbag", "modal");
    const root = host.attachShadow({ mode: "open" });
    const style = el("style");
    style.textContent = BASE_CSS + MODAL_CSS;
    root.appendChild(style);

    const ov = el("div", "ov");
    const m = el("div", "m");
    m.setAttribute("role", "alertdialog");
    m.setAttribute("aria-modal", "true");
    const h = el("div", "h");
    const bar = el("span", "bar");
    bar.style.background = spec.color;
    h.appendChild(bar);
    h.appendChild(el("span", undefined, spec.title));
    const c = el("div", "c");
    c.appendChild(el("div", undefined, spec.intro));
    const ul = el("ul");
    spec.files.slice(0, 50).forEach((f) => {
      const li = el("li");
      const top = el("div");
      top.appendChild(verdictBadge(f));
      top.appendChild(document.createTextNode(" "));
      top.appendChild(sourceBadge(f));
      if (f.tracker) top.appendChild(document.createTextNode(` ${f.tracker}`));
      if (f.seedingTimeSeconds != null) top.appendChild(document.createTextNode(`, seeding ${formatDuration(f.seedingTimeSeconds)}`));
      li.appendChild(top);
      li.appendChild(el("div", "p", f.path));
      li.appendChild(el("div", "r", (f.reasons || []).join("; ")));
      ul.appendChild(li);
    });
    if (spec.files.length > 50) ul.appendChild(el("li", undefined, `... and ${spec.files.length - 50} more`));
    c.appendChild(ul);

    const f = el("div", "f");
    const cancel = el("button", undefined, "Cancel");
    const ok = el("button", spec.danger ? "danger" : "primary", spec.confirmLabel);
    if (spec.requireAck) {
      const lab = el("label");
      const box = el("input");
      box.type = "checkbox";
      ok.disabled = true;
      box.onchange = () => (ok.disabled = !box.checked);
      lab.appendChild(box);
      lab.appendChild(el("span", undefined, spec.requireAck));
      c.appendChild(lab);
    }
    f.appendChild(cancel);
    f.appendChild(ok);
    m.appendChild(h);
    m.appendChild(c);
    m.appendChild(f);
    ov.appendChild(m);
    root.appendChild(ov);

    const done = (v: boolean) => {
      document.removeEventListener("keydown", onKey, true);
      host.remove();
      resolve(v);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        done(false);
      }
    };
    cancel.onclick = () => done(false);
    ok.onclick = () => done(true);
    ov.onclick = (e) => {
      if (e.target === ov) done(false);
    };
    document.addEventListener("keydown", onKey, true);
    document.body.appendChild(host);
    cancel.focus();
  });
}
