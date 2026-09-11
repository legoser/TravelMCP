# 02. Glossary of Domain and Technology Terms

Project glossary for `travelmcp`. Terms are grouped by domain. Established English
terminology is used where possible so the glossary can be used consistently in
code, architecture documentation, APIs, and technical discussions.

## A. Routing and Journey Concepts

| Term | Definition |
|---|---|
| **Itinerary / Journey** | A complete door-to-door result from origin to destination, including all legs, transfers, timings, and gaps. |
| **Leg** | A single continuous movement between two points without a passenger transfer. For transit, this is typically one ride on a trip from boarding stop to alighting stop. It is a primary unit of the routing model. |
| **Segment** | A generic portion of a journey. In this project, `segment` is normally equivalent to `leg`; use `leg` in domain models unless a distinction is explicitly required. |
| **Connection** | A scheduled movement between two stops at a specific time, typically represented as a departure/arrival event for a trip. It is the fundamental input unit for CSA. |
| **Trip** | One operational instance of a route, with an ordered sequence of stop-times and a service calendar. A `trip` is not the same as a passenger journey. |
| **Transfer** | A transition between two transit legs. A transfer is feasible when `arrival + minimum transfer time <= next departure`. |
| **Access leg / Egress leg** | Non-transit movement between the requested location and the transit network: typically walking to the first boarding point and walking from the final alighting point. |
| **Footpath** | A traversable connection between stops or platforms, usually representing a walkable transfer. In GTFS this may be represented through `transfers.txt`; in routing engines it may be generated from pedestrian-network data. |
| **Boarding / Alighting** | Entering and leaving a transit vehicle. These terms are preferred when describing the two endpoints of a transit leg. |
| **Nearest-stop search** | Spatial lookup for candidate boarding/alighting points near an input coordinate. Implemented using spatial indexing such as PostGIS and, where useful, H3/S2. |
| **Transfer feasibility** | Validation that a passenger can physically and temporally move from one leg to the next. It may include minimum transfer time, walking distance, and station topology. |
| **Interlining** | Continuation of service in the same physical vehicle across operationally distinct trips or route patterns. It is not a passenger transfer because the passenger remains on board. |
| **Gap** | A portion of an otherwise valid journey for which the system has no machine-readable supported transit connection. |
| **Self-leg** | A gap explicitly assigned to the user, for example walking, cycling, taxi, or private car. The itinerary remains continuous while clearly marking that the system does not provide the transit connection. |
| **Coverage** | The portion of geography, stops, routes, operators, or travel relations for which the system has usable machine-readable data. |
| **Departure-at / Departure-after** | Search semantics where the requested departure constraint is interpreted as an exact departure time or an earliest acceptable departure time. |
| **Arrival-by** | Reverse-time search mode where the itinerary must arrive no later than a requested time. |
| **Time window** | A range of acceptable departure or arrival times used to generate multiple candidate itineraries. |
| **Minimum transfer time** | The minimum time required to complete a transfer between two transit legs. It may depend on the station, platforms, accessibility, or transfer type. |
| **Transfer buffer** | Additional slack added on top of the physical minimum transfer time to increase robustness against operational uncertainty. |
| **Journey time / Generalized travel time** | Total journey duration from origin to destination. `Generalized travel time` may additionally incorporate penalties for transfers, walking, waiting, or other user preferences. |
| **Wait time** | Time spent waiting for the next leg after arriving at a transfer point or boarding point. |
| **Transfer count** | Number of passenger transfers in an itinerary. It is commonly used as an optimization criterion. |
| **Pareto-optimal / Multi-criteria routing** | Routing against multiple objectives such as arrival time, travel time, transfers, walking, and cost. The result may be a Pareto set rather than a single scalar optimum. |
| **Dominance / Pareto dominance** | A candidate dominates another when it is no worse on every tracked criterion and strictly better on at least one. Dominated labels can be discarded. |
| **Label / State** | A partial routing state maintained by a multi-criteria algorithm, containing at least the reached stop/time and potentially additional resource dimensions. |
| **Isochrone** | A spatial region reachable from a source within a given travel-time or generalized-cost budget. |
| **Reachability** | Whether a location, stop, or destination can be reached under the active network, schedule, and constraints. |
| **Temporal feasibility** | Whether the chronological sequence of departures, arrivals, and transfers is valid. |
| **Multimodality** | Use of multiple transport modes within one itinerary, for example walking + bus + train + walking. |
| **MaaS (Mobility-as-a-Service)** | A system-level approach that integrates multiple mobility services into a single planning and user-facing experience. `travelmcp` implements a focused journey-planning component within this broader concept. |

## B. GTFS — Schedule Data Model

**GTFS (General Transit Feed Specification)** is a widely used open specification
for representing public-transit schedules as a collection of CSV files packaged
as a ZIP archive.

| File | Purpose |
|---|---|
| `agency.txt` | Transit agencies and operators represented in the feed. |
| `stops.txt` | Stops, stations, platforms, entrances, and related geographic entities. |
| `routes.txt` | Public-transit routes or lines. |
| `trips.txt` | Operational trips belonging to routes and service calendars. |
| `stop_times.txt` | Arrival/departure times and stop sequence for each trip. |
| `calendar.txt` / `calendar_dates.txt` | Recurring service calendars and date-specific additions/removals. |
| `frequencies.txt` | Frequency-based service definitions for services without explicit per-trip times. |
| `shapes.txt` | Geographic geometry of vehicle movement. |
| `transfers.txt` | Explicit transfer rules and transfer timing constraints. |
| `fare_attributes.txt` / `fare_rules.txt` | Fare products and rules for applying fares; relevant to a future cost engine. |
| `feed_info.txt` | Feed metadata, including publisher and feed version information. |

### GTFS Extensions and Related Ecosystem

- **GTFS-RT (GTFS Realtime)** — realtime data extension covering `TripUpdates`,
  `VehiclePositions`, and `ServiceAlerts`. Not used in the initial phase.
- **GTFS-Flex** — extension for demand-responsive and flexible transit services.
- **MobilityData / Mobility Database** — ecosystem and catalog for GTFS feeds and
  related transit-data tooling. Feed identifiers may be used as external source
  references, not as canonical domain identifiers.
- **GTFS Validator** — feed validation tooling used to detect structural and
  semantic data-quality problems before ingestion.
- **Feed version** — immutable or otherwise traceable version of an imported feed.
  Feed provenance and versioning must remain distinct from canonical entity IDs.

## C. Data Standards and Transit Formats

- **NeTEx (Network Timetable Exchange)** — European XML-based standard for
  exchanging public-transport network, timetable, and related operational data.
- **SIRI (Service Interface for Real Time Information)** — European standard for
  exchanging realtime public-transport information.
- **HAFAS** — proprietary timetable and journey-planning ecosystem associated with
  HaCon and used by multiple transport organizations. It is an external source
  format, not a canonical project model.
- **OSM Public Transport** — OpenStreetMap tagging conventions for public
  transport infrastructure and route relations, including `stop_position`,
  `platform`, `route`, and `route_master`. OSM provides network and geographic
  structure but does not by itself provide a complete schedule.
- **Open Data** — publicly released datasets that permit use under their
  respective licenses. In this project this includes government registries and
  other openly accessible transit datasets.
- **Provenance** — metadata describing where a fact originated, which source and
  version supplied it, and how it was transformed before entering the canonical
  model.
- **Canonical model** — normalized internal representation that is independent of
  source-specific schemas and is used by downstream business logic and routing.

## D. Routing Algorithms

- **CSA (Connection Scan Algorithm)** — schedule-based routing algorithm that
  processes time-ordered connections sequentially. Well suited to timetable
  networks and particularly straightforward for earliest-arrival searches.
- **RAPTOR (Round-Based Public Transit Optimized Router)** — routing algorithm
  organized around transfer rounds and transit routes rather than individual
  connections. Suitable for transfer-aware and multi-criteria transit routing.
- **Trip-Based Routing** — route-planning approach operating primarily on trips
  and transfer relationships. It can be highly performant on large static
  networks but requires more complex preprocessing and implementation.
- **Transfer Patterns** — precomputed origin/destination transfer structures for
  fast queries on large, static transit networks. Effective when the network is
  relatively stable and preprocessing cost is acceptable.
- **Label-setting / Label-correcting** — general families of shortest-path and
  multi-criteria algorithms in which partial states (`labels`) are propagated and
  pruned according to dominance rules.
- **Time-dependent routing** — routing where edge cost or availability depends on
  departure time, as is normal for scheduled transit.
- **Profile routing** — computes a set or profile of optimal journeys over a range
  of departure or arrival times rather than for a single timestamp.
- **Contraction / Preprocessing** — offline transformations used by some routing
  engines to accelerate repeated queries on large networks.

### External Routing Engines

These are potential future adapters or specialized components rather than the
project's core routing model:

- **OpenTripPlanner 2 (OTP2)** — Java-based multimodal routing engine using GTFS,
  OSM, and additional data sources.
- **MOTIS** — high-performance C++ multimodal routing engine with a comparatively
  complex operational footprint.
- **Valhalla / OSRM / GraphHopper** — road-network routing engines that may be used
  for walking, cycling, or driving legs where appropriate.

## E. Geospatial Data and Spatial Indexing

- **PostGIS** — PostgreSQL extension for geometry/geography types, spatial
  indexing, and geospatial queries such as nearest-neighbor search and proximity
  filtering.
- **H3 / S2** — hierarchical spatial indexing systems that partition the Earth
  into cells and support efficient spatial aggregation and locality queries.
- **Overpass API** — query API for extracting selected OpenStreetMap objects.
- **Geofabrik** — provider of regional OpenStreetMap extracts, commonly distributed
  as `.osm.pbf` files.
- **Geocoding** — conversion from a human-readable address or place description to
  geographic coordinates.
- **Reverse geocoding** — conversion from coordinates to a human-readable address
  or place hierarchy.
- **Spatial nearest-neighbor query** — query that retrieves the closest indexed
  geographic features to a given point or geometry.
- **Spatial containment** — determination of whether a point or feature belongs to
  a geographic area or administrative hierarchy.

## F. Data Ingestion, Quality, and Verification

- **Source adapter / Provider** — integration component that reads an external
  source and exposes it through a stable internal interface.
- **Connector** — source-facing component responsible for retrieval and initial
  parsing. A connector may feed an adapter or ingestion pipeline.
- **Ingestion pipeline** — deterministic sequence that transforms external data
  into canonical data.
- **Normalization** — conversion of source-specific representations into common
  field semantics and types.
- **Enrichment** — adding derived or externally verified attributes to a record.
- **Deduplication / Entity Resolution** — detection and consolidation of multiple
  source records referring to the same real-world entity.
- **Reconciliation** — comparison of records from multiple sources to produce a
  consistent canonical representation.
- **Verification** — validation of identity, location, schedule, or other facts
  against one or more trusted sources.
- **Trust score / Source trust** — explicit representation of how reliable a source
  or source pair is for a particular verification task.
- **Identity confidence** — confidence that two records refer to the same real-world
  entity. This is distinct from confidence in the entity's coordinates or schedule.
- **Geometry confidence** — confidence in the correctness and precision of the
  stored geographic position.
- **Freshness** — how recently a source or canonical record was successfully
  retrieved or verified.
- **Staleness** — condition where data is older than the acceptable freshness
  threshold for its use case.
- **Review queue** — explicit workflow state for records or conflicts that require
  human review before becoming authoritative.
- **Manual verification** — human-confirmed data that must not be silently
  overwritten by automated re-verification.
- **Hard validator** — deterministic validation rule that rejects structurally or
  semantically impossible data, for example non-monotonic stop times or impossible
  travel speeds.
- **Soft validation** — validation that produces a warning, score, or review signal
  without necessarily rejecting the record.
- **AdaptedRecord** — common intermediate representation emitted by connectors before
  verification and canonicalization, including identifiers, names, geometry,
  validity, provenance, and raw source data.

### Provider vs Carrier

- **Provider** — source of data, such as `mintrans`, `yandex`, `gtfs`, or `osm`.
  Providers describe provenance and source identity.
- **Carrier** — real transport operator responsible for operating a service. A
  carrier is a domain entity and must not be conflated with a data provider.
- **`provider != carrier`** — these concepts remain separate throughout the canonical
  model, even when one provider publishes information about a single carrier.

## G. Canonical Location and Hierarchy Model

- **`places.level`** — canonical hierarchy depth used by the project. The current
  scale is 0–5; `admin_level` is a denormalized source-compatible attribute used
  for filtering.

| `level` | `admin_level` | Meaning | Example |
|---:|---:|---|---|
| 0 | 2 | Country | Russia |
| 1 | 3 | Federal district | Siberian Federal District |
| 2 | 4 | Region | Kemerovo Oblast |
| 3 | 6 | District / municipal district | Kemerovo Municipal District |
| 4 | 8 | City / locality | Kemerovo |
| 5 | 9–10 | City district | Central District |

- **Place hierarchy query** — a typical terminal-to-city lookup resolves the
  ancestor with `places.level = 4` through `place_closure`.
- **`place_closure`** — closure-table representation of the place hierarchy,
  combined with an adjacency-list parent relation. It supports efficient ancestor
  and descendant queries.
- **Closure table** — denormalized hierarchy table containing `(ancestor,
  descendant, depth)` relationships to avoid recursive traversal on hot paths.
- **Adjacency list** — direct parent reference (`parent_id`) used alongside the
  closure table as the canonical immediate hierarchy relation.
- **Deferred foreign-key constraint** — constraint checked at transaction commit,
  useful when maintaining hierarchical structures that must be updated atomically.

## H. Persistence, Versioning, and Data Lifecycle

- **SCD2 (Slowly Changing Dimension Type 2)** — versioning pattern that preserves
  historical records by storing validity intervals such as `valid_from` and
  `valid_to` instead of overwriting the previous state.
- **`last_verified_at`** — timestamp indicating when the current record was last
  successfully verified against the relevant source(s).
- **Validity interval** — time period during which a canonical record is considered
  valid according to the source and verification state.
- **Partitioning** — physically splitting large database tables into partitions,
  for example by time range, to improve maintenance and query locality at scale.
- **Additive migration** — schema migration that adds structures without destroying
  populated data. Destructive changes require explicit approval and a migration
  strategy.
- **Immutable identifier** — identifier whose value is never reused for a different
  canonical entity or feed version.

## I. MCP and Application Architecture

- **MCP (Model Context Protocol)** — open protocol for connecting AI agents to
  tools, resources, and prompt templates.
- **Tool** — MCP operation invoked by the client to perform an action or query.
- **Resource** — MCP-exposed data or contextual content that can be read by the
  client.
- **Prompt** — reusable MCP prompt template provided by a server.
- **Streamable HTTP** — MCP transport for remote clients over HTTP.
- **stdio** — local MCP transport using standard input/output.
- **Stateless transport** — server does not depend on persistent protocol sessions
  between requests; request authentication and application state are handled
  independently.
- **JSON-RPC 2.0** — RPC message protocol used by MCP.
- **Official Go MCP SDK** — `modelcontextprotocol/go-sdk`, used for typed MCP
  integration and protocol-level functionality.
- **`mark3labs/mcp-go`** — community Go MCP library used where its transport or
  integration capabilities are required by the project.

## J. Infrastructure and Observability

- **OpenTelemetry** — vendor-neutral framework for instrumentation and telemetry
  signals such as traces, metrics, and logs.
- **Prometheus** — metrics collection and time-series monitoring system.
- **Grafana** — visualization and dashboarding platform for operational metrics.
- **Structured logging** — machine-readable logs represented as fields rather than
  unstructured text, typically correlated by request or entity identifiers.
- **Trace / Span** — distributed-tracing concepts representing a complete operation
  and its individual timed sub-operations.
- **Correlation ID** — identifier used to connect logs, metrics, and traces belonging
  to the same request or logical operation.
- **Health check** — liveness signal indicating that the process is running.
- **Readiness check** — signal indicating that the service is ready to accept
  traffic and required dependencies are available.
- **`/healthz`** — liveness endpoint: process is alive.
- **`/readyz`** — readiness endpoint: required dependencies such as the database or
  cache are usable.

## K. Russian and CIS Transit Data Ecosystem

- **Russian Ministry of Transport interregional bus-route registry** — open datasets
  describing interregional bus routes, stops, operators, tariffs, and related
  registry information. The registry does not provide a complete stop-by-stop
  timetable equivalent to GTFS schedules.
- **Regional open-data portals** — fragmented regional datasets in CSV, JSON, HTML,
  and other formats containing transport schedules or registries.
- **Yandex Timetables** — public timetable/search infrastructure historically used
  as a source for Russian rail and bus information. External availability and API
  conditions must be treated as operationally variable; it is not the canonical
  source of truth.
- **2GIS / Yandex Maps APIs** — mapping and routing services that may be used as
  future integrations but are outside the initial provider set.
- **Web scraping** — extraction of structured information from public web pages.
  It is inherently brittle because markup, client-side behavior, rate limits, and
  anti-bot controls may change without notice.
- **Dynamic web application / JS-rendered source** — website where the required
  data is assembled client-side or fetched through browser APIs rather than being
  present in the initial HTML response.
- **Source health** — operational state of an external source, including
  availability, latency, freshness, parsing failures, and quota state.
- **152-FZ** — Russian Federal Law No. 152-FZ on personal data. The project must
  account for its requirements when processing user accounts, API keys, and logs.

## L. Project-Specific Domain Terms

- **Digitized transit connection** — a transit connection represented by
  machine-readable schedule/network data that the system can actually route over.
- **User-responsibility section** — presentation term for a `self-leg`; explicitly
  identifies a journey section for which the system has no supported transit data.
- **Multimodal route** — itinerary combining two or more transport modes.
- **Schedule aggregator** — system that ingests schedules from multiple sources and
  exposes them through one normalized model.
- **Canonical entity** — a project-owned representation of a real-world entity,
  independent of any particular source record.
- **Source record** — raw or adapted record originating from one external provider.
- **Provenance chain** — traceable sequence from source data through adaptation,
  verification, reconciliation, and canonical persistence.
