---
id: index
title: Architecture decision records
sidebar_label: ADR index
slug: /
sidebar_position: 0
---

# Architecture decision records

The decisions of `checkout`, in the order they were taken. The ADR bodies are
immutable; a changed decision gets a new ADR that supersedes the old one. The
*Status* column is copied from each ADR's own status line.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](/docs/adr/0001-checkout-bounded-context) | Checkout is a bounded context of the storefront domain | Accepted (bootstrap). Design source: warehouse-docs `storefront/model/model.yaml`, context `checkout`. |
| [0002](/docs/adr/0002-checkout-assumptions) | Aggregate, invariants and assumptions | Accepted (2026-10-25). |
| [0003](/docs/adr/0003-synchronous-contracts-and-the-anti-corruption-layer) | Synchronous contracts and the Anti-Corruption Layer | Accepted (2026-10-25). |
| [0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue) | CloudEvents envelope and type catalogue | Accepted (2026-10-25). |
| [0005](/docs/adr/0005-mcp-server) | A read-only MCP server | Accepted (2026-10-25). |
| [0006](/docs/adr/0006-fleet-observability-standard) | Adopt the fleet observability standard (logs, metrics, traces) | Accepted (2026-10-26). |
| [0007](/docs/adr/0007-a2a-inbound-adapter) | A2A inbound adapter | Accepted (2026-10-30). |
| [0008](/docs/adr/0008-requoted-dates-in-the-destination-time-zone) | Requoted delivery dates in the destination's time zone | Accepted (2026-10-30). |
