import type { Itinerary, Leg } from "@/gen/transit/v1/trip_pb";
import { LegMode, ServiceClass } from "@/gen/transit/v1/common_pb";
import { classLabel, clock, kindLabel, rupees } from "@/lib/format";

function LegChip({ leg }: { leg: Leg }) {
  if (leg.mode === LegMode.WALK) {
    return (
      <span className="rounded bg-neutral-100 px-1.5 py-0.5 text-xs text-neutral-600 dark:bg-neutral-800 dark:text-neutral-400">
        🚶 {leg.durationMin} min
      </span>
    );
  }
  if (leg.mode === LegMode.METRO_RAIL) {
    const color = leg.lineColor ? `#${leg.lineColor}` : "#666";
    return (
      <span
        className="rounded px-1.5 py-0.5 text-xs font-medium text-white"
        style={{ backgroundColor: color }}
      >
        Ⓜ {leg.routeShortName}
      </span>
    );
  }
  const ac = leg.serviceClass === ServiceClass.VAJRA || leg.serviceClass === ServiceClass.VAYU_VAJRA;
  return (
    <span
      className={`rounded px-1.5 py-0.5 text-xs font-medium ${
        ac ? "bg-sky-600 text-white" : "bg-emerald-600 text-white"
      }`}
    >
      🚌 {leg.routeShortName}
    </span>
  );
}

export function ItineraryCard({ it, total }: { it: Itinerary; total: number }) {
  const rides = it.legs.filter((l) => l.mode !== LegMode.WALK);
  return (
    <article className="rounded-lg border border-neutral-200 p-4 dark:border-neutral-800">
      <header className="flex items-baseline justify-between gap-3">
        <div>
          <span className="text-xs font-medium uppercase tracking-wide text-neutral-500">
            {kindLabel(it.kind)}
          </span>
          <div className="mt-0.5 flex flex-wrap gap-1">
            {it.tags.map((t) => (
              <span
                key={t}
                className="rounded-full border border-neutral-300 px-2 py-0.5 text-[11px] text-neutral-700 dark:border-neutral-700 dark:text-neutral-300"
              >
                {t}
              </span>
            ))}
          </div>
        </div>
        <div className="text-right">
          <div className="text-xl font-semibold">{rupees(it.cost?.groupTotal)}</div>
          <div className="text-xs text-neutral-500">
            {it.totalMin} min · {clock(it.depart)}–{clock(it.arrive)}
          </div>
        </div>
      </header>

      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        {it.legs.map((l, i) => (
          <LegChip key={i} leg={l} />
        ))}
      </div>

      <ol className="mt-3 space-y-1.5 text-sm">
        {rides.map((l, i) => (
          <li key={i} className="flex justify-between gap-3">
            <span>
              <span className="font-medium">{l.routeShortName}</span>
              <span className="text-neutral-500">
                {" "}
                {l.from?.name} → {l.to?.name}
                {l.intermediateStops.length > 0 && ` · ${l.intermediateStops.length + 1} stops`}
                {l.headwayMin > 0 && ` · every ~${l.headwayMin} min`}
              </span>
            </span>
            <span className="whitespace-nowrap text-neutral-600 dark:text-neutral-400">
              {l.farePerPerson && l.farePerPerson.paise > 0
                ? `${rupees(l.farePerPerson)}/head`
                : l.mode === LegMode.METRO_RAIL
                  ? "same ticket"
                  : ""}
              {l.womenFree && <span className="ml-1 text-emerald-700 dark:text-emerald-400">· women free</span>}
            </span>
          </li>
        ))}
      </ol>

      {it.cost && (
        <footer className="mt-3 border-t border-neutral-100 pt-2 text-xs text-neutral-600 dark:border-neutral-800 dark:text-neutral-400">
          {it.cost.byClass.map((c) => (
            <div key={c.serviceClass} className="flex justify-between">
              <span>
                {classLabel(c.serviceClass)} · {c.payingRiders} of {total} pay {rupees(c.perPerson)}
              </span>
              <span>{rupees(c.group)}</span>
            </div>
          ))}
          {it.cost.smartCardSaving && it.cost.smartCardSaving.paise > 0 && (
            <div className="flex justify-between">
              <span>Smart card saving</span>
              <span>−{rupees(it.cost.smartCardSaving)}</span>
            </div>
          )}
          {it.cost.dailyPassHint && (
            <div className="mt-1 text-amber-700 dark:text-amber-400">
              A BMTC daily pass for the group would be {rupees(it.cost.dailyPassGroupTotal)}.
            </div>
          )}
        </footer>
      )}
    </article>
  );
}
