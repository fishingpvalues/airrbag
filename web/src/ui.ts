// Shared rendering: colors, badges, tooltips and the modal. Everything renders
// inside a shadow root so the *Arr's CSS and ours never touch.

import labelsCss from "./shared/labels.css";
import { ACCENT, COLORS, LOCK_PATH, LOGO_BAG, LOGO_SHIELD, SOURCE_LABEL, VERDICT_LABEL, sourceOf } from "./shared/tokens";
import { formatDuration, formatSize } from "./shared/format";
import type { FileVerdict } from "./types";

export { COLORS, formatDuration, formatSize };

export const BASE_CSS = `
:host{all:initial}
*{box-sizing:border-box;font-family:Roboto,"open sans","Helvetica Neue",Helvetica,Arial,sans-serif}
${labelsCss}
`;

const SVG = "http://www.w3.org/2000/svg";

function lockIcon(): SVGElement {
  const svg = document.createElementNS(SVG, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(SVG, "path");
  path.setAttribute("d", LOCK_PATH);
  svg.appendChild(path);
  return svg;
}

// The Airrbag mark, for the panel and dialog headers.
export function logoIcon(size = 16): SVGElement {
  const svg = document.createElementNS(SVG, "svg");
  svg.setAttribute("viewBox", "0 0 512 512");
  svg.setAttribute("width", String(size));
  svg.setAttribute("height", String(size));
  svg.setAttribute("aria-hidden", "true");
  const shield = document.createElementNS(SVG, "path");
  shield.setAttribute("d", LOGO_SHIELD);
  shield.setAttribute("fill", ACCENT);
  const bag = document.createElementNS(SVG, "path");
  bag.setAttribute("d", LOGO_BAG);
  bag.setAttribute("fill", "#fff");
  svg.appendChild(shield);
  svg.appendChild(bag);
  return svg;
}

export function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

export function tooltip(f: FileVerdict): string {
  const lines = [`Verdict: ${VERDICT_LABEL[f.verdict] || f.verdict}`];
  if (f.indexer) lines.push(`Indexer: ${f.indexer}`);
  if (f.client) lines.push(`Client: ${f.client}`);
  if (f.tracker) lines.push(`Tracker: ${f.tracker}`);
  if (f.ratio != null) lines.push(`Ratio: ${f.ratio.toFixed(2)}${f.requiredRatio ? ` of ${f.requiredRatio}` : ""}`);
  if (f.seedingTimeSeconds != null) {
    const req = f.requiredSeedTimeSeconds ? ` of ${formatDuration(f.requiredSeedTimeSeconds)}` : "";
    lines.push(`Seeding: ${formatDuration(f.seedingTimeSeconds)}${req}`);
  }
  if (f.relation && f.relation !== "unknown") lines.push(`Relation to seed: ${f.relation}`);
  return lines.concat(f.reasons || []).join("\n");
}

export function sourceBadge(f: FileVerdict): HTMLElement {
  const src = sourceOf(f);
  const b = el("span", `ab-label ab-s-${src}`);
  if (src === "private") b.appendChild(lockIcon());
  b.appendChild(document.createTextNode(SOURCE_LABEL[src]));
  b.title = tooltip(f);
  return b;
}

export function verdictBadge(f: FileVerdict): HTMLElement {
  const b = el("span", `ab-label ab-v-${f.verdict}`, VERDICT_LABEL[f.verdict] || f.verdict);
  b.title = tooltip(f);
  return b;
}

// --- modal -------------------------------------------------------------------

// Mirrors the Servarr ModalContent: 18px header with a close button, padded
// body, footer with right-aligned buttons in the *Arr button styles.
const MODAL_CSS = `
.ov{position:fixed;inset:0;background:rgba(0,0,0,.6);display:flex;align-items:flex-start;justify-content:center;z-index:2147483647;padding:60px 16px;overflow:auto}
.m{position:relative;background:#2a2a2a;color:#ccc;border-radius:6px;width:min(680px,100%);display:flex;flex-direction:column;box-shadow:0 5px 15px rgba(0,0,0,.5);font-size:14px;line-height:1.43}
.h{display:flex;gap:10px;align-items:center;padding:15px 50px 15px 30px;border-bottom:1px solid #393f45;font-size:18px;color:#fff}
.h .bar{width:4px;align-self:stretch;border-radius:2px}
.x{position:absolute;top:8px;right:10px;width:40px;height:40px;border:0;background:none;color:#909293;font-size:22px;line-height:1;cursor:pointer;padding:0}
.x:hover{color:#fff}
.c{padding:24px 30px;overflow:auto;max-height:60vh}
.c ul{margin:14px 0 0;padding:0;list-style:none}
.c li{padding:9px 0;border-top:1px solid rgba(255,255,255,.07)}
.c li .p{word-break:break-all;color:#e1e2e3;font-size:13px;margin-top:4px}
.c li .r{color:#909293;font-size:12px;margin-top:3px}
.c label{display:flex;gap:8px;align-items:flex-start;margin-top:18px;cursor:pointer;color:#e1e2e3}
.f{padding:15px 30px;border-top:1px solid #393f45;display:flex;justify-content:flex-end;gap:10px}
button.b{border:1px solid #393f45;border-radius:4px;padding:6px 16px;font-size:14px;line-height:1.52;cursor:pointer;color:#ccc;background:#333}
button.b:hover{background:#3a3a3a}
button.b.danger{background:#f05050;border-color:#f05050;color:#fff}
button.b.danger:hover{background:#ec2626;border-color:#ec2626}
button.b.primary{background:#5d9cec;border-color:#5d9cec;color:#fff}
button.b:disabled{opacity:.5;cursor:not-allowed}
button:focus-visible,input:focus-visible{outline:2px solid #1db79e;outline-offset:2px}
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
    h.appendChild(logoIcon(18));
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
    const cancel = el("button", "b", "Cancel");
    const ok = el("button", spec.danger ? "b danger" : "b primary", spec.confirmLabel);
    const close = el("button", "x", "\u00D7");
    close.setAttribute("aria-label", "Close");
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
    m.appendChild(close);
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
    close.onclick = () => done(false);
    ok.onclick = () => done(true);
    ov.onclick = (e) => {
      if (e.target === ov) done(false);
    };
    document.addEventListener("keydown", onKey, true);
    document.body.appendChild(host);
    cancel.focus();
  });
}
