---
id: ddd-artifacts
title: DDD artifacts (ddd-crew)
sidebar_label: DDD artifacts index
sidebar_position: 10
---

# DDD artifacts (ddd-crew)

The strategic and tactical design of `checkout`, drawn with the
[ddd-crew](https://github.com/ddd-crew) tools plus UML and ER diagrams. Every
diagram is Mermaid, checked against the code and ADRs on `develop`, and carries
a `Source:` line naming the files it came from.

| Artifact | ddd-crew tool / notation | What it shows |
| --- | --- | --- |
| [Core domain chart](/docs/ddd/core-domain-chart) | [Core Domain Charts](https://github.com/ddd-crew/core-domain-charts) | Where this context sits on business differentiation vs model complexity, and why it is Supporting although its complexity is the highest in the storefront. |
| [Bounded context canvas](/docs/ddd/bounded-context-canvas) | [Bounded Context Canvas v5](https://github.com/ddd-crew/bounded-context-canvas) | Purpose, classification, roles, every inbound and outbound message, business decisions. |
| [Context map](/docs/ddd/context-map) | [Context Mapping](https://github.com/ddd-crew/context-mapping) | This context's slice of the storefront map: patterns per edge, including the Anti-Corruption Layer to `customer-ordering`. |
| [Aggregate design canvas](/docs/ddd/aggregate-design-canvas) | [Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas) | `CheckoutSession`: eight states, invariants, commands, events, concurrency. |
| [Domain message flow](/docs/ddd/domain-message-flow) | [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling) | Five scenarios as numbered command / event / query flows across contexts. |
| [EventStorming](/docs/ddd/eventstorming) | [EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) | Six design-level boards with the sticky-note colours, plus the hotspots and how the code settled them. |
| [Ubiquitous language](/docs/ddd/ubiquitous-language) | Glossary | Every term with the code identifier that implements it. |
| [Class diagrams](/docs/ddd/class-diagram) | UML class diagram | The aggregate and its value objects, the seven events, the use cases and ports, and the hexagonal layout with the adapters that exist. |
| [Entity-relationship diagram](/docs/ddd/entity-relationship) | ER diagram | The Postgres schema created by `0001_checkout_schema.up.sql`: three tables, keys and the partial indexes of the policy scans. |
| [Sequence diagrams](/docs/ddd/sequence-diagrams) | UML sequence diagram | Open, confirm (authorize, then place), the in-doubt resolver and expiry, with error branches. |
| [Domain events](/docs/ddd/domain-events) | Event catalogue | Every event published (and the statement that none is consumed): full CloudEvents type, topic, key, payload, producer, consumers. |

Related pages outside the pack: [Use cases](/docs/ddd/use-cases),
[Subdomain classification](/docs/ddd/subdomain-classification),
[Integration](/docs/ecosystem/integration) and the
[event catalogue](/docs/api-reference/events).

:::note[Sources of truth]
The code wins. Domain rules come from `internal/domain/checkoutsession`,
behaviour from `internal/application/usecases`, the envelope from
`internal/adapters/kafka/cloudevents`, the schema from
`internal/adapters/outbound/postgres/migrations`, the REST contract from
`apis/openapi.yaml`, the event contract from `apis/asyncapi.yaml`, and
decisions from the ADRs in `docs/adr/`. Where a page and the code disagree, the
page is wrong.
:::

:::info Not in this pack
The MCP server ([ADR 0005](/docs/adr/0005-mcp-server)) and the A2A agent
([ADR 0007](/docs/adr/0007-a2a-inbound-adapter)) are built, but they are read
surfaces over the same `GetSession` use case and add no aggregate, entity or
event of their own, so the pack has no pages for them; they appear in the
class diagram and in [MCP tools](/docs/mcp/tools). There is no web remote and
no analytical read side. The application layer, the HTTP and Kafka adapters,
the outbox relay and the database schema are covered by the pages above; where
an edge to another context is only built on one side, the page says so.
:::
