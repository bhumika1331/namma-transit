"use client";

import { useEffect, useState } from "react";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { HealthResponse } from "@/gen/transit/v1/meta_pb";
import { apiBaseUrl, metaClient } from "@/lib/transport";

type State =
  | { status: "loading" }
  | { status: "ok"; health: HealthResponse }
  | { status: "error"; message: string };

export function HealthCard() {
  const [state, setState] = useState<State>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    metaClient
      .health({})
      .then((health) => {
        if (!cancelled) setState({ status: "ok", health });
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setState({
            status: "error",
            message: err instanceof Error ? err.message : String(err),
          });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <section className="rounded-lg border border-neutral-200 p-4 text-sm dark:border-neutral-800">
      <h2 className="font-medium">Backend</h2>
      <p className="mt-1 break-all text-neutral-500">{apiBaseUrl}</p>
      {state.status === "loading" && <p className="mt-2">Connecting…</p>}
      {state.status === "error" && (
        <p className="mt-2 text-red-600">Unreachable: {state.message}</p>
      )}
      {state.status === "ok" && (
        <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
          <dt className="text-neutral-500">Version</dt>
          <dd>{state.health.version}</dd>
          <dt className="text-neutral-500">Datasets</dt>
          <dd>
            {state.health.datasets.length === 0
              ? "none loaded"
              : state.health.datasets
                  .map(
                    (d) =>
                      `${d.name} (${d.source}): ${d.stops} stops, ${d.routes} routes`,
                  )
                  .join("; ")}
          </dd>
          {state.health.faresUpdatedAt && (
            <>
              <dt className="text-neutral-500">Fares updated</dt>
              <dd>
                {timestampDate(state.health.faresUpdatedAt).toLocaleString()}
              </dd>
            </>
          )}
        </dl>
      )}
    </section>
  );
}
