import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { getJSON } from "./api";

export interface Resource<T> {
  data: T | null;
  error: Error | null;
  loading: boolean;
  reload: () => void;
}

// useResource fetches path and refetches when it changes. When poll returns a
// number of milliseconds for the current data, the resource refreshes itself
// after that delay (used while the library is still being evaluated).
export function useResource<T>(path: string, poll?: (d: T) => number | false): Resource<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const pollRef = useRef(poll);
  pollRef.current = poll;

  useEffect(() => {
    let alive = true;
    let timer: number | undefined;
    setLoading(true);
    getJSON<T>(path)
      .then((d) => {
        if (!alive) return;
        setData(d);
        setError(null);
        const next = pollRef.current ? pollRef.current(d) : false;
        if (next) timer = window.setTimeout(() => setTick((t) => t + 1), next);
      })
      .catch((e: Error) => alive && setError(e))
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [path, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, reload };
}

// useHashRoute returns the current "#/route?query" parts and a navigate helper.
export interface Route {
  name: string;
  params: URLSearchParams;
}

function parse(): Route {
  const h = location.hash.replace(/^#\/?/, "");
  const [name, q] = h.split("?");
  return { name: name || "overview", params: new URLSearchParams(q || "") };
}

export function useHashRoute(): Route {
  const [route, setRoute] = useState<Route>(parse);
  useEffect(() => {
    const on = () => setRoute(parse());
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}

export function href(name: string, params?: Record<string, string>): string {
  const q = params ? new URLSearchParams(Object.entries(params).filter(([, v]) => v !== "")).toString() : "";
  return `#/${name}${q ? `?${q}` : ""}`;
}
