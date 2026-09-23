"use client";

import { useEffect, useId, useRef, useState } from "react";
import type { Place } from "@/gen/transit/v1/common_pb";
import { PlaceKind } from "@/gen/transit/v1/common_pb";
import { placeClient } from "@/lib/transport";

type Props = {
  label: string;
  value: Place | null;
  onChange: (p: Place | null) => void;
};

export function PlaceInput({ label, value, onChange }: Props) {
  const id = useId();
  const [text, setText] = useState(value?.name ?? "");
  const [options, setOptions] = useState<Place[]>([]);
  const [open, setOpen] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (value) setText(value.name);
  }, [value]);

  function onInput(q: string) {
    setText(q);
    onChange(null);
    if (timer.current) clearTimeout(timer.current);
    if (q.trim().length < 2) {
      setOptions([]);
      return;
    }
    timer.current = setTimeout(async () => {
      try {
        const res = await placeClient.suggest({ query: q, limit: 6 });
        setOptions(res.places);
        setOpen(true);
      } catch {
        setOptions([]);
      }
    }, 250);
  }

  return (
    <div className="relative">
      <label htmlFor={id} className="block text-xs font-medium text-neutral-600 dark:text-neutral-400">
        {label}
      </label>
      <input
        id={id}
        value={text}
        onChange={(e) => onInput(e.target.value)}
        onFocus={() => options.length && setOpen(true)}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        autoComplete="off"
        placeholder="Stop or station name"
        className="mt-1 w-full rounded-md border border-neutral-300 bg-white px-3 py-2 text-sm outline-none focus:border-neutral-900 dark:border-neutral-700 dark:bg-neutral-900 dark:focus:border-neutral-100"
      />
      {open && options.length > 0 && (
        <ul className="absolute z-10 mt-1 max-h-64 w-full overflow-auto rounded-md border border-neutral-200 bg-white shadow-lg dark:border-neutral-700 dark:bg-neutral-900">
          {options.map((p) => (
            <li key={p.id}>
              <button
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => {
                  onChange(p);
                  setText(p.name);
                  setOpen(false);
                }}
                className="flex w-full items-start gap-2 px-3 py-2 text-left text-sm hover:bg-neutral-100 dark:hover:bg-neutral-800"
              >
                <span className="mt-0.5 text-xs" aria-hidden>
                  {p.kind === PlaceKind.METRO_STATION ? "Ⓜ" : "🚌"}
                </span>
                <span>
                  <span className="block">{p.name}</span>
                  {p.subLabel && (
                    <span className="block text-xs text-neutral-500">{p.subLabel}</span>
                  )}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
