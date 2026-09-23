import type { Money } from "@/gen/transit/v1/common_pb";
import { ServiceClass } from "@/gen/transit/v1/common_pb";
import { ItineraryKind } from "@/gen/transit/v1/trip_pb";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { timestampDate } from "@bufbuild/protobuf/wkt";

export function rupees(m?: Money): string {
  if (!m) return "₹0";
  const r = m.paise / 100;
  return Number.isInteger(r) ? `₹${r}` : `₹${r.toFixed(2)}`;
}

export function clock(ts?: Timestamp): string {
  if (!ts) return "";
  return timestampDate(ts).toLocaleTimeString("en-IN", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    timeZone: "Asia/Kolkata",
  });
}

export function classLabel(c: ServiceClass): string {
  switch (c) {
    case ServiceClass.ORDINARY:
      return "Ordinary";
    case ServiceClass.VAJRA:
      return "AC Vajra";
    case ServiceClass.VAYU_VAJRA:
      return "Vayu Vajra";
    case ServiceClass.METRO_FEEDER:
      return "Metro feeder";
    case ServiceClass.METRO:
      return "Metro";
    default:
      return "";
  }
}

export function kindLabel(k: ItineraryKind): string {
  switch (k) {
    case ItineraryKind.METRO_ONLY:
      return "Metro";
    case ItineraryKind.DIRECT_BUS:
      return "Direct bus";
    case ItineraryKind.BUS_METRO:
      return "Bus + metro";
    case ItineraryKind.BUS_BUS:
      return "Two buses";
    default:
      return "Trip";
  }
}

/** Local datetime-input value for "now" in IST, minute precision. */
export function nowIST(): string {
  const d = new Date();
  const ist = new Date(d.getTime() + (330 + d.getTimezoneOffset()) * 60000);
  return ist.toISOString().slice(0, 16);
}

/** Interpret a datetime-local string as IST and return a Date. */
export function fromIST(local: string): Date {
  return new Date(`${local}:00+05:30`);
}
