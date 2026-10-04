// Data access for the dashboard. Every call goes to the same origin as the
// page (the Airrbag listener in front of one *Arr) and carries the browser's
// *Arr session, which is how the API authenticates.

import type { FileVerdict, Verdict } from "../types";
import type { Source } from "../shared/tokens";

const root = document.getElementById("app") as HTMLElement;

export const env = {
  base: root.dataset.base || "",
  instance: root.dataset.instance || "",
  app: root.dataset.app || "",
  version: root.dataset.version || "",
  arrHome: root.dataset.arr || "/",
};

export const appName = (a: string): string => (a ? a.charAt(0).toUpperCase() + a.slice(1) : "the *Arr");

export class AuthError extends Error {}

export async function getJSON<T>(path: string): Promise<T> {
  const r = await fetch(`${env.base}/api/dashboard/${path}`, {
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (r.status === 401) throw new AuthError(`Sign in to ${appName(env.app)} in this browser, then reload.`);
  let body: unknown = null;
  try {
    body = await r.json();
  } catch {
    // fall through to the status check
  }
  if (!r.ok) {
    const msg = body && typeof body === "object" && "error" in body ? String((body as { error: unknown }).error) : r.statusText;
    throw new Error(msg || `HTTP ${r.status}`);
  }
  return body as T;
}

export type Counts = Partial<Record<Verdict, number>>;
export type Bytes = Partial<Record<Verdict, number>>;

export interface InstanceSummary {
  name: string;
  app: string;
  appVersion: string;
  listen: string;
  current: boolean;
  computing: boolean;
  computedAt?: string;
  files: number;
  counts: Counts;
  bytes: Bytes;
  errors?: string[];
  indexedFiles: number;
  indexedAt?: string;
  indexError?: string;
  clients: Record<string, string>;
}

export interface Overview {
  instance: string;
  version: string;
  guard: boolean;
  dryRun: boolean;
  failClosed: boolean;
  instances: InstanceSummary[];
  totals: { counts: Counts; bytes: Bytes; files: number };
}

export interface FileRow extends FileVerdict {
  instance: string;
  app: string;
}

export interface FilesPage {
  page: number;
  pageSize: number;
  totalRecords: number;
  totalPages: number;
  sortKey: string;
  sortDirection: "ascending" | "descending";
  computing: boolean;
  computedAt?: string | null;
  records: FileRow[];
}

export type Decision = "blocked" | "would-block" | "overridden" | "error-closed" | "error-open";

export interface GuardEvent {
  time: string;
  instance: string;
  method: string;
  path: string;
  decision: Decision;
  reason: string;
  files: number;
  titles?: string[];
  override?: string;
}

export interface GuardLog {
  guard: boolean;
  dryRun: boolean;
  failClosed: boolean;
  counts: Partial<Record<Decision, number>>;
  events: GuardEvent[];
}

export interface Settings {
  instances: { name: string; listen: string; upstream: string; app: string; apiKey: string }[];
  clients: { name: string; type: string; url: string; username: string; password: string; apiKey: string }[];
  pathMappings: { from: string; to: string; source: string }[];
  trackers: {
    file: string;
    private: string[] | null;
    rules: { domains: string[]; minSeedTime: string; minRatio: number; requireBoth: boolean }[] | null;
    default: { domains: string[] | null; minSeedTime: string; minRatio: number; requireBoth: boolean } | null;
  };
  guard: { enabled: boolean; dryRun: boolean; failClosed: boolean; grantTtl: string };
  cacheTtl: string;
  historyLimit: number;
  logLevel: string;
}

export interface ClientsHealth {
  checkedAt: string;
  instances: Record<string, Record<string, string>>;
}

export interface SystemInfo {
  version: string;
  goVersion: string;
  os: string;
  arch: string;
  startedAt: string;
  uptimeSeconds: number;
  instance: string;
  metricsPath: string;
  healthPath: string;
  instances: { name: string; app: string; appVersion: string; listen: string; indexedFiles: number; indexedAt?: string; indexError?: string }[];
}

export type SourceFilter = Source | "";
