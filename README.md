# namma-transit

Group travel cost and time estimator for Bengaluru public transport.

Enter a start point, an end point, a departure time, how many people are
travelling and how many of them are women. Get ranked options across
Namma Metro, direct BMTC buses (ordinary vs. AC Vajra) and one-transfer
bus + metro / bus + bus combinations, each with the total group fare and an
estimated travel time, plus an auto-rickshaw baseline for comparison.

The fare split matters more than it looks: Karnataka's Shakti scheme makes
non-AC BMTC buses free for women, AC buses cost two to three times the
ordinary fare, and metro pricing is by distance slab. Doing that arithmetic
by hand for a group is the problem this project removes.

## Stack

- `web/` — Next.js (App Router, TypeScript), deployed on Vercel.
- `backend/` — Go. Protobuf contract served with [Connect](https://connectrpc.com)
  (gRPC, gRPC-Web and Connect on one HTTP port). Transit network is held in
  memory; a hand-written RAPTOR router plans trips.
- `proto/` — single source of truth for the API; `buf` generates Go and TypeScript.
- SQLite (pure Go driver) stores scraped raw responses, normalised tables and fare data.

## Data

- Routes, stops, timetables and fare samples are scraped by `backend/cmd/scraper`
  from the Namma BMTC app's API at a polite rate, and BMRCL pages for metro
  stations and timings.
- Bootstrap and fallback data comes from the community GTFS feeds
  [Vonter/bmtc-gtfs](https://github.com/Vonter/bmtc-gtfs) and
  [Vonter/bmrcl-gtfs](https://github.com/Vonter/bmrcl-gtfs), both licensed
  under the Open Database License (ODbL). Derived data in this project
  keeps that attribution.
- Fare charts (BMTC ordinary and Vajra stage fares, Namma Metro distance
  slabs, Bengaluru auto meter rates) live as versioned JSON in
  `backend/internal/fare/charts/` with their effective dates and are embedded in the binary.

## Disclaimer

This is a personal project. Fares and times are estimates derived from
public and community data, not official quotes. BMTC and BMRCL are not
affiliated with this project. Timetable data from the Namma BMTC app is
known to be approximate; the app shows headways ("every ~N min") rather
than exact departures where that is the case.

## Status

Early development. See the task list in the project plan; each task lands
as its own commit.

## License

Code is MIT licensed (see `LICENSE`). Transit data derived from the Vonter
GTFS feeds is ODbL.
