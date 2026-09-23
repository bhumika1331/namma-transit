import { HealthCard } from "./health-card";
import { TripPlanner } from "@/components/trip-planner";

export default function Home() {
  return (
    <main className="mx-auto flex min-h-screen w-full max-w-xl flex-col gap-6 px-4 py-8">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">namma-transit</h1>
        <p className="mt-1 text-sm text-neutral-600 dark:text-neutral-400">
          What a group pays, and how long it takes, across Namma Metro and BMTC.
        </p>
      </header>
      <TripPlanner />
      <details className="text-sm">
        <summary className="cursor-pointer text-neutral-500">Backend status</summary>
        <div className="mt-2">
          <HealthCard />
        </div>
      </details>
    </main>
  );
}
