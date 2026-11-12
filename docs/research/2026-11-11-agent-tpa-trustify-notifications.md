# RHTPA / Trustify as a source and sink for AgentSessions

Research date: 2026-11-11. Bounded primary-source investigation; no integration, external writes, or architectural decisions. The notification implementation and REST specification below were read from the **Trustify v0.7.0 tag**, not inferred from current main or marketing descriptions.

## Findings

**There is a concrete open-source notification capability.** Trustify v0.7.0, released October 15, 2026, includes a database change log and WebSocket endpoint. The old `trustification/trustification` repository identifies `guacsec/trustify` as its successor. This supports considering Trustify as both an SBOM/advisory sink and a change source, with an adapter translating relevant changes into AgentInputs. [v0.7.0 release](https://github.com/guacsec/trustify/releases/tag/v0.7.0), [superseded repository](https://github.com/trustification/trustification)

**Downstream qualification matters.** Red Hat's current product-documentation root redirects to **RHTPA 3.1**. The detailed 3.1 release/administration pages could not be retrieved during this bounded check. Therefore, inclusion and supported configuration of the Trustify WebSocket feature in RHTPA 3.1 remain **unverified**. An upstream release is not proof of a downstream supported feature, and the documentation landing page does not establish every supported lifecycle or deployed patch level. [Current RHTPA documentation](https://docs.redhat.com/en/documentation/red_hat_trusted_profile_analyzer/3.1)

## What the released stream actually does

`GET /api/v3/notifications?after=<cursor>` upgrades to WebSocket. Messages contain a cursor, entity kind (`sbom` or `advisory`), entity ID (nullable for bulk operations), and operation (`added` or `deleted`). A first connection without `after` receives a control message holding the current cursor; reconnecting with `after` requests stored events after it. This is a document-change stream, not a payload identifying newly vulnerable applications, packages, or all impacted SBOMs. An advisory notification therefore still requires querying relationships and deciding which applications/sessions it concerns. [Tagged protocol README](https://github.com/guacsec/trustify/blob/v0.7.0/modules/notification/README.md)

The tagged authentication middleware accepts an Authorization bearer token, a query token, or the `Sec-WebSocket-Protocol: access_token, <jwt>` convention. The endpoint requires at least one of `read.sbom` or `read.advisory` and filters entity kinds according to those permissions. For a server-side adapter, the Authorization header is the straightforward option. [Tagged token middleware](https://github.com/guacsec/trustify/blob/v0.7.0/modules/notification/src/inject_token.rs), [tagged endpoint](https://github.com/guacsec/trustify/blob/v0.7.0/modules/notification/src/endpoints.rs)

The implementation persists records in `change_log`, uses PostgreSQL LISTEN/NOTIFY with a polling fallback, and describes recording changes within the caller's transaction. Replay selects records after the supplied UUID cursor. Defaults are **one day of retention**, a 30-second fallback sweep, and cleanup every five minutes; these settings are configurable. This is materially stronger than an ephemeral live signal, but retention is finite. [Tagged change-log implementation](https://github.com/guacsec/trustify/blob/v0.7.0/common/src/db/change.rs), [tagged configuration](https://github.com/guacsec/trustify/blob/v0.7.0/modules/notification/src/config.rs)

**Do not upgrade that into an exactly-once delivery claim.** In the inspected endpoint, replay-query failures are logged and execution continues; a lagged in-memory broadcast receiver also logs a warning and continues. No consumer acknowledgement is part of the shown protocol. The code examined does not establish a client-visible expired-cursor error. The README describes gap-free reconnects, but the bounded code review indicates reconciliation and duplicate handling are still appropriate. No runtime fault-injection tests were performed. [Tagged endpoint implementation](https://github.com/guacsec/trustify/blob/v0.7.0/modules/notification/src/endpoints.rs)

## Sink, queries, and application identity

The tagged REST specification exposes SBOM and advisory uploads, SBOM listing/detail, SBOM package queries, advisories for an SBOM, SBOMs by package, and vulnerability detail/analysis. Advisory queries support `ingested` and `modified` filters plus sorting and pagination. These are concrete building blocks for a polling adapter as well as enrichment after a stream notification. Document identifiers can be UUIDs/digests; SBOM labels can hold integration metadata. The WebSocket endpoint is absent from the inspected OpenAPI document, so searching only that specification would miss the feature. [v0.7.0 OpenAPI](https://github.com/guacsec/trustify/blob/v0.7.0/openapi.yaml)

Relevant tagged paths include:

- `POST /api/v3/sbom` and `POST /api/v3/advisory`
- `GET /api/v3/sbom/{id}/advisory`
- `GET /api/v3/sbom/by-package`
- `GET /api/v3/advisory` and `GET /api/v3/vulnerability/{id}`

**Integration interpretation, not a requirement:** a Hub-side mapping would still connect an SBOM/artifact digest and build/commit identity to one or more applications and their sessions. A document name alone is a weak identity. A shared package can legitimately affect several SBOMs, while deployment status and application ownership remain outside a generic advisory notification. Trustify can provide correlation evidence; it does not establish the controller's application inventory or routing policy.

An illustrative flow is: CI publishes a build SBOM to Trustify; importers ingest advisory data; an adapter sees an advisory/SBOM change, queries relevant findings, maps them to applications, and records pending AgentInput deliveries. A future result could publish a new build SBOM or an authorized VEX document back to the system. That would not make Trustify an AgentSession transcript store or action-completion journal.

## What this suggests investigating next

A bounded experiment could compare the tagged WebSocket plus periodic reconciliation with REST polling alone, using two explicitly mapped SBOMs. Persist receipt/cursor and pending fanout before advancing the local checkpoint; deduplicate repeated observations; reconcile after retention gaps. Stream events should be treated as reasons to reassess state, not pre-resolved instructions to start remediation. For RHTPA, verify the deployed build and exposed endpoint/support scope before choosing this path. These remain research options; no source, transport, schema, or routing policy is selected here.
