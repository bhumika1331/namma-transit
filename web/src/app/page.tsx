import { HealthCard } from "./health-card";

export default function Home() {
  return (
    <main className="mx-auto flex min-h-screen w-full max-w-xl flex-col gap-6 px-4 py-10">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">namma-transit</h1>
        <p className="mt-1 text-sm text-neutral-600 dark:text-neutral-400">
          Group travel cost and time across Namma Metro and BMTC.
        </p>
      </header>
      <HealthCard />
    </main>
  );
}
