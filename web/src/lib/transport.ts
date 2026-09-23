import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { MetaService } from "@/gen/transit/v1/meta_pb";
import { PlaceService } from "@/gen/transit/v1/place_pb";
import { TripService } from "@/gen/transit/v1/trip_pb";

// Base URL of the Go Connect server. Set NEXT_PUBLIC_API in .env.local / Vercel.
export const apiBaseUrl =
  process.env.NEXT_PUBLIC_API ?? "http://localhost:8080";

const transport = createConnectTransport({ baseUrl: apiBaseUrl });

export const metaClient = createClient(MetaService, transport);
export const placeClient = createClient(PlaceService, transport);
export const tripClient = createClient(TripService, transport);
