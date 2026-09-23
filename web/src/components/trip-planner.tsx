"use client";

import { useState } from "react";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Place } from "@/gen/transit/v1/common_pb";
import type { PlanTripResponse } from "@/gen/transit/v1/trip_pb";
import { tripClient } from "@/lib/transport";
import { fromIST, nowIST, rupees } from "@/lib/format";
import { PlaceInput } from "./place-input";
import { ItineraryCard } from "./itinerary-card";

export function TripPlanner() {
  const [origin, setOrigin] = useState<Place | null>(null);
  const [dest, setDest] = useState<Place | null>(null);
  const [when, setWhen] = useState(nowIST());
  const [total, setTotal] = useState(1);
  const [women, setWomen] = useState(0);
  const [cards, setCards] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<PlanTripResponse | null>(null);

  async function plan(e: React.FormEvent) {
    e.preventDefault();
    if (!origin || !dest) {
      setError("Pick both places from the suggestions.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const res = await tripClient.planTrip({
        origin: { ref: { case: "placeId", value: origin.id } },
        destination: { ref: { case: "placeId", value: dest.id } },
        departure: timestampFromDate(fromIST(when)),
        party: { total, women: Math.min(women, total), smartCardHolders: Math.min(cards, total) },
        maxItineraries: 5,
      });
      setResult(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const num = (v: string) => Math.max(0, Math.min(99, Number(v) || 0));

  return (
    <div className="space-y-6">
      <form onSubmit={plan} className="space-y-4 rounded-lg border border-neutral-200 p-4 dark:border-neutral-800">
        <PlaceInput label="From" value={origin} onChange={setOrigin} />
        <PlaceInput label="To" value={dest} onChange={setDest} />
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <label className="text-xs font-medium text-neutral-600 dark:text-neutral-400 col-span-2 sm:col-span-1">
            Leave at
            <input
              type="datetime-local"
              value={when}
              onChange={(e) => setWhen(e.target.value)}
              className="mt-1 w-full rounded-md border border-neutral-300 bg-white px-2 py-2 text-sm dark:border-neutral-700 dark:bg-neutral-900"
            />
          </label>
          <label className="text-xs font-medium text-neutral-600 dark:text-neutral-400">
            People
            <input type="number" min={1} max={99} value={total} onChange={(e) => setTotal(Math.max(1, num(e.target.value)))}
              className="mt-1 w-full rounded-md border border-neutral-300 bg-white px-2 py-2 text-sm dark:border-neutral-700 dark:bg-neutral-900" />
          </label>
          <label className="text-xs font-medium text-neutral-600 dark:text-neutral-400">
            Women
            <input type="number" min={0} max={total} value={women} onChange={(e) => setWomen(num(e.target.value))}
              className="mt-1 w-full rounded-md border border-neutral-300 bg-white px-2 py-2 text-sm dark:border-neutral-700 dark:bg-neutral-900" />
          </label>
          <label className="text-xs font-medium text-neutral-600 dark:text-neutral-400">
            Metro cards
            <input type="number" min={0} max={total} value={cards} onChange={(e) => setCards(num(e.target.value))}
              className="mt-1 w-full rounded-md border border-neutral-300 bg-white px-2 py-2 text-sm dark:border-neutral-700 dark:bg-neutral-900" />
          </label>
        </div>
        <p className="text-xs text-neutral-500">
          Women ride non-AC BMTC buses free under Shakti; AC buses and metro charge everyone.
        </p>
        <button
          type="submit"
          disabled={busy}
          className="w-full rounded-md bg-neutral-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50 dark:bg-neutral-100 dark:text-neutral-900"
        >
          {busy ? "Planning…" : "Compare options"}
        </button>
        {error && <p className="text-sm text-red-600">{error}</p>}
      </form>

      {result && (
        <section className="space-y-3">
          {result.warnings.map((w) => (
            <p key={w} className="text-sm text-amber-700 dark:text-amber-400">{w}</p>
          ))}
          {result.itineraries.map((it) => (
            <ItineraryCard key={it.id} it={it} total={total} />
          ))}
          {result.auto && (
            <article className="rounded-lg border border-dashed border-neutral-300 p-4 text-sm dark:border-neutral-700">
              <div className="flex justify-between">
                <span>
                  🛺 {result.auto.autos} auto{result.auto.autos > 1 ? "s" : ""} by meter · ~{result.auto.roadKm} km · ~{result.auto.estMin} min
                </span>
                <span className="font-semibold">{rupees(result.auto.groupTotal)}</span>
              </div>
            </article>
          )}
          <p className="text-[11px] text-neutral-400">data {result.dataVersion}</p>
        </section>
      )}
    </div>
  );
}
