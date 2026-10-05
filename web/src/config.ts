import { arrApiKey } from "./shared/arrkey";

// Configuration read from the injected <script> tag, plus the per-app UI
// knowledge. Everything that depends on the *Arr's DOM lives in APPS.

const me = (document.currentScript as HTMLScriptElement | null) ||
  (document.querySelector("script[data-airrbag-base]") as HTMLScriptElement | null);

export const cfg = {
  base: (me && me.dataset.airrbagBase) || "",
  app: (me && me.dataset.airrbagApp) || "",
};

export interface AppUI {
  // Detail page route, matched against location.pathname.
  route: RegExp;
  // Where to put the panel, first match wins. CSS-module class names carry a
  // hash suffix, hence the [class*=] prefix matches.
  anchors: string[];
}

const movie: AppUI = {
  route: /\/movie\/[^/]+\/?$/,
  anchors: ['[class*="MovieDetails-header"]', '[class*="MovieDetails-contentContainer"]'],
};
const series: AppUI = {
  route: /\/series\/[^/]+\/?$/,
  anchors: ['[class*="SeriesDetails-header"]', '[class*="SeriesDetails-contentContainer"]'],
};

export const APPS: Record<string, AppUI[]> = {
  radarr: [movie],
  sonarr: [series],
  whisparr: [movie, series],
  lidarr: [
    {
      route: /\/artist\/[^/]+\/?$/,
      anchors: ['[class*="ArtistDetails-header"]', '[class*="ArtistDetails-contentContainer"]'],
    },
    {
      // Album pages: /album/<foreignAlbumId>. The server resolves the album
      // and returns only its tracks.
      route: /\/album\/[^/]+\/?$/,
      anchors: ['[class*="AlbumDetails-header"]', '[class*="AlbumDetails-contentContainer"]'],
    },
  ],
  readarr: [{
    route: /\/author\/[^/]+\/?$/,
    anchors: ['[class*="AuthorDetails-header"]', '[class*="AuthorDetails-contentContainer"]'],
  }],
};

export async function api(path: string, init?: RequestInit): Promise<Response> {
  // Use the original fetch even after patching, and always the same origin.
  // The *Arr's own key (see shared/arrkey.ts) authenticates the call the way
  // the *Arr UI authenticates its own.
  const key = await arrApiKey(cfg.base.replace(/\/__airrbag$/, ""), nativeFetch);
  const headers = new Headers((init && init.headers) || undefined);
  if (key) headers.set("X-Api-Key", key);
  // Airrbag refuses state-changing calls without this header (CSRF guard):
  // a cross-site page cannot add it without a CORS preflight Airrbag never answers.
  headers.set("X-Airrbag-Request", "1");
  return nativeFetch(cfg.base + "/api" + path, Object.assign({ credentials: "same-origin" }, init || {}, { headers }));
}

const nativeFetch: typeof fetch = window.fetch.bind(window);
