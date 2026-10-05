export type Verdict = "keep" | "frees-nothing" | "safe" | "unknown";

export interface FileVerdict {
  fileId: number;
  parentId: number;
  parentTitle?: string;
  path: string;
  relativePath?: string;
  size: number;
  verdict: Verdict;
  protocol: "usenet" | "torrent" | "unknown";
  private: boolean;
  relation: string;
  reasons: string[];
  indexer?: string;
  client?: string;
  tracker?: string;
  ratio?: number;
  requiredRatio?: number;
  requiredSeedTimeSeconds?: number;
  obligationMet?: boolean;
  seedingTimeSeconds?: number;
  torrentName?: string;
  torrentState?: string;
  reason?: string;
  evidence?: string[];
  severity?: "danger" | "warning" | "info" | "ok";
  needsConfirm?: boolean;
}

export interface CheckResponse {
  deletesFiles: boolean;
  files?: FileVerdict[];
  error?: string;
  failClosed?: boolean;
}
