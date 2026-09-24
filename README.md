# namma-transit

Group travel cost and time estimator for Bengaluru public transport.

Enter a start point, an end point, a departure time, how many people are
travelling and how many of them are women. Get ranked options across
Namma Metro, direct BMTC buses (ordinary vs. AC Vajra) and one-transfer
bus + metro / bus + bus combinations, each with the total group fare and an
estimated travel time, plus an auto-rickshaw baseline for comparison.

The fare split matters more than it looks: Karnataka's Shakti scheme makes
non-AC BMTC buses free for women, AC buses cost two to three times the
ordinary fare, and metro pricing is by distance slab. For five people with
three women, Majestic to Indiranagar is ₹46 by direct bus and ₹250 by metro.
Doing that arithmetic by hand for a group is the problem this project removes.

## Stack

- `web/` Next.js (App Router, TypeScript, Tailwind) on Vercel.
- `backend/` Go. Protobuf contract served with [Connect](https://connectrpc.com)
  (gRPC, gRPC-Web and Connect on one HTTP port, no proxy). The transit
  network is held in memory; a hand-written RAPTOR router plans trips.
- `proto/` single source of truth for the API; `buf generate` emits Go and
  TypeScript into `backend/gen` and `web/src/gen` (both committed).
- SQLite (pure-Go `modernc.org/sqlite`) holds the network, scraped raw
  responses and fare data as one file baked into the container image.

## Run it locally

Prerequisites: Go 1.25+, Node 22+, [buf](https://buf.build), [task](https://taskfile.dev).

```sh
task feeds          # download community GTFS zips into backend/data/gtfs
task dev:api        # Go API on :8080, loads the zips directly
task dev:web        # Next.js on :3000 (copy web/.env.example to web/.env.local)
```

To run from the SQLite store instead (what the container does):

```sh
task db             # import feeds into backend/data/transit.db
task dev:api:sqlite
```

`task test` runs the Go tests; the planner and router tests use the real
feeds when present in `backend/data/gtfs` and skip otherwise.

### Server environment

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | listen port |
| `DATA_SOURCE` | `gtfs` | `gtfs` reads zips from `GTFS_DIR`; `sqlite` reads `DB_PATH` |
| `GTFS_DIR` | `data/gtfs` | directory with `bmrcl.zip` and `bmtc.zip` |
| `DB_PATH` | `data/transit.db` | SQLite file (opened immutable) |
| `ALLOWED_ORIGINS` | `http://localhost:3000` | comma-separated CORS origins |
| `OLA_MAPS_API_KEY` | unset | enables Ola Maps geocoding before the keyless Photon fallback |

The web app reads `NEXT_PUBLIC_API` (the API base URL).

## Data

- Bootstrap and fallback data comes from the community GTFS feeds
  [Vonter/bmtc-gtfs](https://github.com/Vonter/bmtc-gtfs) and
  [Vonter/bmrcl-gtfs](https://github.com/Vonter/bmrcl-gtfs), both licensed
  under the Open Database License (ODbL). Derived data keeps that attribution.
- `backend/cmd/scraper` fills the same SQLite tables from the Namma BMTC
  app's API at 2 requests/second: `core` (service types, routes, stops with
  shape distances, timetables), `fares` (first stop to every stop per route,
  weekly and shardable, deriving fare stage boundaries) and `bmrcl` (metro
  station cross-check with goquery). Every response is archived raw so
  normalisation can be replayed after a decoder fix.
- Fare charts (BMTC ordinary and Vajra stage fares from January 2025, Namma
  Metro distance slabs from February 2025, Bengaluru auto meter rates from
  August 2025) live as versioned JSON in `backend/internal/fare/charts/`
  and are embedded in the binary. A fare revision is a data change.

The scheduled workflow in `.github/workflows/scrape.yml` runs the core job
nightly and the fare job weekly, publishing `transit.db` as the `data`
GitHub release asset. `deploy.yml` builds the image with that database and
deploys it to Cloud Run (needs `GCP_PROJECT`, `GCP_REGION`, `GCP_SA_KEY`
secrets and an `ALLOWED_ORIGINS` variable).

## How estimates are made

- Metro: explicit timetable from the feed; ~1.7 min per station; 6 min
  interchange at Majestic and RV Road. Fare by distance slab, one ticket
  across an interchange; smart-card holders get 5% (peak) or 10% (off-peak).
- Bus: timetable departures where the feed has them, else headway plus a
  speed model (14 km/h in weekday peaks 8–11 and 17–20, 21 km/h otherwise,
  30 s per stop). Fare by scraped stage boundaries when available, else by
  distance on the 2 km stage chart. Women ride ordinary and metro-feeder
  buses free; AC buses charge everyone.
- Routing: RAPTOR with at most one transfer, run for everything, bus-only
  and metro-only so the slower mode still appears for comparison, at the
  requested time and 10 and 20 minutes later. Results are ranked by minutes
  plus rupees per head and tagged fastest / cheapest.
- Auto baseline: ceil(people / 3) autos at ₹36 for 2 km then ₹18/km over
  1.3× the straight-line distance, 1.5× at night.

## Known limitations

- Vayu Vajra airport buses are excluded from routing until their route-fixed
  fares are scraped; pricing them on the Vajra chart would understate cost.
- Namma BMTC timetables are approximate; legs show "every ~N min" where the
  schedule is thin. Live ETAs are not used yet: `internal/timeprovider`
  defines the `Provider` interface that a `VehicleTripDetails_v2`-backed
  implementation can satisfy without touching the router.
- The BMTC API returned HTTP 403 for every request from the development
  network even with the app's headers; the scraper is tested against a
  recorded-shape fixture server. Run it from another network or CI first.
- Children and senior concessions, Kannada stop-name matching and the Pink
  and Blue metro lines are not handled yet.

## Disclaimer

This is a personal project. Fares and times are estimates derived from
public and community data, not official quotes. BMTC and BMRCL are not
affiliated with this project.

## License

Code is MIT licensed (see `LICENSE`). Transit data derived from the Vonter
GTFS feeds is ODbL.
