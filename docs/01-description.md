# 01. Project Description

This document provides a high-level understanding of the project without requiring
knowledge of the implementation. Basic public-transport terminology is assumed;
see `02-glossary.md` for definitions.

## Goal

Build an **MCP server** that finds door-to-door public-transport itineraries
between an origin and a destination.

A route may combine multiple transport modes and operators, including walking
connections. The system selects suitable boarding and alighting points near the
requested locations and accounts for transfers and connection times.

Requests may come from an AI agent such as Claude through MCP. Users are
identified by API key and may have individual search preferences.

## Route Model

An itinerary is an ordered sequence of **legs** connecting the origin and
destination:

```text
origin
  → walking
  → stop 1
  → transit leg
  → stop 2
  → transfer
  → stop 3
  → transit leg
  → stop 4
  → walking
  → destination
```

A leg is one continuous part of the itinerary:

1. **Access / egress** — walking from the origin to the first boarding point and
   from the final alighting point to the destination.
2. **Transit legs** — travel by public transport such as bus, tram, train, or
   other supported modes.
3. **Transfers** — connections between transit legs that are valid only when the
   arrival time plus the required transfer time is no later than the next
   departure.
4. **Self-leg** — a gap where no supported transit connection exists. The user
   must cover the gap independently, for example by walking, cycling, taxi, or
   car. The itinerary remains continuous instead of being discarded.

An itinerary may contain one or multiple transit legs and may combine different
transport modes and data providers.

## Core Principles

### 1. Provider Independence

Each external data source is implemented as a separate provider or adapter.
Examples include GTFS feeds, government registries, carrier websites, and future
REST APIs.

All providers convert source-specific data into the **canonical domain model**.
The planning engine does not depend on provider-specific formats.

Adding, replacing, or disabling a provider must not require changes to the
planning core.

### 2. Source-Independent Planning Core

The planning engine operates only on the canonical model.

It is responsible for building the required transfer structures, finding
itineraries using algorithms such as CSA or RAPTOR, and identifying gaps in
available coverage.

Source-specific properties affect planning only through the data they provide,
not through provider-specific logic in the planner.

### 3. Runtime Configuration

Provider configuration, feature flags, and default search parameters must be
changeable without rebuilding the service.

User-specific settings are stored separately and override system defaults where
applicable.

### 4. Authentication and User Scope

Users access the service through API keys.

Each key belongs to a specific user, whose search preferences and other
user-scoped settings are isolated from those of other users.

User registration is self-service and may require subsequent administrator
moderation.

### 5. Honest Results

The system must not fabricate connectivity where source data does not provide
it.

When coverage is incomplete, the missing section is represented explicitly as a
self-leg rather than silently inventing a transit connection or discarding the
rest of the itinerary.

Provider health must be observable, including availability, latency, data
freshness, and remaining quotas where applicable.

### 6. Extensible Cost Model

Each leg has an extensible cost field.

The domain model must support future fare calculation based on real or estimated
prices without requiring structural changes to the itinerary model.

Fare calculation itself is outside the initial implementation scope.

## Primary Use Case

### City-to-City Route

The initial target scenario is a journey between two cities.

Example request:

```text
Origin address: city N
Destination address: city M
Departure time: T
```

The resulting itinerary may look like:

```text
origin address
  → walking
  → local stop in city N
  → city transit
  → intercity terminal
  → intercity service
  → terminal in city M
  → walking transfer
  → local transit
  → nearest stop to destination
  → walking
  → destination address
```

The intercity connection may come from a government registry, GTFS feed, or
another supported provider.

When no supported intercity connection exists, that section becomes a self-leg,
while the system still constructs the surrounding parts of the journey where
sufficient data is available.

## Future Use Cases

The following scenarios are planned for later phases:

* city-wide address-to-address routing using local GTFS data;
* journeys to a specific settlement using multiple transfers and transport
  modes;
* arrival-by-time search in addition to departure-after-time search.

## Initial Scope

The first implementation does **not** include:

* ticket booking or ticket sales;
* fare calculation beyond the extensible data model;
* real-time transit data such as GTFS-RT delays and cancellations;
* payment integrations;
* end-user mobile or web applications;
* commercial international APIs.

The initial system focuses on route planning through MCP and administrative
interfaces using open and freely accessible data sources.

## Non-Functional Requirements

### Configuration

Service and user configuration must be changeable without rebuilding the
application.

### Observability

The service must provide observability from the beginning, including:

* health and readiness endpoints;
* metrics for external providers and service load;
* structured logging;
* tracing where applicable.

### Provider Isolation

Failure or degradation of one provider must not bring down the service as a
whole.

Provider failures must remain visible through metrics and explicit errors where
they affect a request.

### MCP Interface

Communication between the system and AI agents uses MCP over stateless
Streamable HTTP.

## Initial Technology Stack

* **Go** — primary implementation language and `travelmcp` module.
* **MCP** — official Go SDK (`modelcontextprotocol/go-sdk`) with
  `mark3labs/mcp-go` where required for HTTP transport.
* **PostgreSQL + PostGIS** — users, API keys, configuration, schedule cache,
  quota/usage counters, and spatial queries such as nearest-stop and transfer
  calculations.

### Initial Data Providers

The first provider set includes:

* synthetic provider for deterministic testing;
* Russian Ministry of Transport interregional bus-route registry (CSV);
* GTFS data for Moscow and Saint Petersburg;
* OSM data for stops and pedestrian-network information;
* carrier-site scraping as a later, targeted integration with explicit health
  monitoring.

## Terminology

Domain terminology such as `leg`, `transfer`, `gap`, `self-leg`, `GTFS`, `CSA`,
and `RAPTOR` is defined in [`02-glossary.md`](02-glossary.md).
