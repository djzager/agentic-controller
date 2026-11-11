# Durable event ingress for AgentSession proposals

Research date: 2026-11-11. Primary documentation checked on this date. This is a design research note, not a description of implemented controller behavior. See the independent
[AgentSession proposal](../adr/0025-agent-sessions-and-event-routing.md) and
[spike](../spikes/agent-session-event-routing.md).

## What the providers actually guarantee

### GitHub delivery identity and acknowledgment

GitHub documents `X-GitHub-Delivery` as the delivery identifier and preserves it when a delivery is redelivered. A receiver must return a successful HTTP response within ten seconds; processing should happen asynchronously. Authenticate the webhook before accepting it. This supports a provider-specific stable receipt key, rather than deriving identity from a timestamp or the raw HTTP body. [GitHub webhook best practices](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks)

### GitHub failure recovery

GitHub does **not** automatically redeliver failed deliveries. Receivers can arrange scheduled recovery using delivery APIs and request redelivery for repository, organization, and GitHub App webhooks. Therefore an integration cannot assume the producer supplies eventual retry after ingress downtime. [GitHub handling failed deliveries](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries)

GitHub makes deliveries from the past three days available for redelivery. Recovery must operate within that upstream window; it is not an indefinite event archive. [GitHub redelivering webhooks](https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks)

### Jira Cloud delivery identity and retries

Jira Cloud documents up to five retries with randomized 5–15 minute delays after the preceding failure, for specified response codes (408, 409, 425, 429, 5xx), connection failures, and timeouts. Its prolonged-failure behavior can reduce attempts. `X-Atlassian-Webhook-Identifier` is unique within a tenant and remains unchanged across retries. `X-Atlassian-Webhook-Retry` counts retries. Duplicate deliveries remain possible. Scope the stable identifier by the configured tenant. [Jira Cloud webhooks](https://developer.atlassian.com/cloud/jira/platform/webhooks/)

The REST operation for retrieving failed webhooks is marked Experimental, describes access as Connect-app-only, and says failures may disappear after 72 hours. Do not promise this recovery mechanism for every Jira integration type; verify the actual authentication and registration model in the spike. [Jira REST Get failed webhooks](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-webhooks/#api-rest-api-3-webhook-failed-get)

### CloudEvents identity

CloudEvents 1.0.2 requires each distinct event to have a unique combination of `source` and `id`. Consumers may treat identical combinations as duplicates, but duplicate resends **may**, rather than must, reuse the original ID. One occurrence can generate several events. Consequently, choosing a CloudEvents envelope does not itself establish retry-stable delivery identity or semantic work uniqueness. Our source adapter must define that contract. [CloudEvents 1.0.2 specification](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md)

### Kubernetes object identity

An object name is unique within its resource and namespace while the object exists. A deleted name can be reused; a new object receives a new UID. `generateName` asks the server to append a generated suffix. These properties support deterministic named creation and require explicit handling of deletion: a name alone is not a permanent deduplication record. [Kubernetes object names and IDs](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/)

## Proposed admission contract — design inference

The useful promise is: **for a specified work identity and destination, repeated admission converges on one logical AgentInput throughout the declared retention horizon.** This is narrower and testable. It does not mean exactly-once network delivery, uninterrupted provider availability, or exactly-once external effects.

Keep three identities separate:

1. **Transport event identity:** the authenticated source namespace plus its retry-stable event identifier. This detects delivery retries.
2. **Work identity:** the desired operation, if the route deliberately coalesces distinct events into one operation. This is route-specific policy.
3. **Execution attempt identity:** a particular processing attempt. Recovery attempts belong to the same logical input; an intentional new operation requires an explicit new work identity.

An issue ID alone cannot identify every issue update. A raw payload hash can collapse two intentionally identical requests, or fail to recognize a retry if transport fields changed. Define canonical event fields and test the adapter against provider fixtures.

## Proposed flow

```text
authenticated source adapter
  -> durable receipt/inbox
  -> persisted routing decision
  -> deterministic AgentInput creation for each destination
  -> session wake/reconciliation
  -> harness claims input and prepares context (optional AgentLearnings)
  -> agent processing, child runs, durable completion
```

These are responsibilities, not a recommendation to create one public CRD for every box. AgentRouter could initially name a separately deployed component plus route configuration. Inventory lookup and application-specific matching belong above the domain-agnostic execution controller.

### Receipt before acknowledgment

Validate authentication, tenant/source binding, payload size, and supported event type. Store the normalized event and its immutable identity before acknowledging it. If persistence fails, return a failure. If the response is lost after persistence, the duplicate request resolves to the same receipt. Do not hold the webhook request open while an agent works.

Receipt persistence can initially use Kubernetes resources if volume, payload limits, retention, and API access fit. A durable broker or database can replace this implementation behind the same contract. An in-memory channel is insufficient. Object storage for large payloads needs a defined write/commit protocol so a receipt never refers to unavailable content.

### Freeze the routing decision before fan-out

Resolve event-to-destination matching asynchronously and record the selected target UIDs, route identity, routing configuration revision, and normalized intent before creating inputs. If inventory changes during a retry, reuse the committed decision. An explicit re-evaluation request can create a new decision. If a classifier is added later, commit its decision before delivery as well; inference must not rerun unpredictably after partial delivery.

For a CVE affecting twenty applications, partial progress is expected: the router may create eight inputs and crash. Recovery resumes the remaining destinations and recognizes the already-created ones. Delivery progress should be reconstructible from deterministic input identity, not solely from a status update that might have been lost after creation.

### Deterministic creation

For illustration, use a versioned canonical encoding:

```text
workKey = sourceEventKey
          OR an explicitly configured route-specific semantic key

inputKey = encode(v1, workKey, targetSessionUID, routePurpose, replayEpoch)
inputName = "input-" + sha256(inputKey)[0:56]
```

Persist the full identity and intent digest on the object; the shortened name is an index, not proof of identity. Create by that exact `metadata.name`. If it already exists, fetch it and verify the full immutable identity, target UID, and intent digest. A mismatch is a visible conflict, never an overwrite. If the create response is ambiguous, repeat this lookup/create procedure using the same key.

The hash length and field schema are implementation choices for review. Hashing requires unambiguous serialization. Include a destination incarnation identifier so delayed work cannot silently target a replacement session with the same human-readable name. Route configuration changes should not automatically change work identity; an explicit replay epoch authorizes intentional repetition.

The proposal additionally requires an admission gate: an input must not execute
or be normally garbage-collected before its creation/admission is recorded in
the durable ledger. Without that gate, an input could complete and disappear
before the router saved its receipt, allowing a retry to recreate it. This is a
design requirement to test, not a guarantee Kubernetes adds automatically.

### Semantic work deduplication

A PR review route might define work as `(repository identity, PR identity, head SHA, review purpose)` and use a separate explicit re-review request identity. Distinct provider events concerning the same head can then converge deliberately. This policy is different from suppressing webhook retries.

A CVE route needs enough identity to distinguish advisory revisions and relevant application revisions if those should trigger new work. Defining only “one input per CVE” could suppress valid later remediation. The proposal should specify concrete route contracts for its first use cases instead of claiming universal duplicate detection.

## Retention and failure boundaries

- **After input garbage collection:** retain a receipt/delivery tombstone that records the completed admission. Otherwise an old delivery can create the same named object again. Do not make the tombstone a dependent object that disappears with the input.
- **During fan-out:** retain the committed target set until delivery settles; surface destinations that were deleted or became unauthorized. Do not silently redirect to replacement sessions.
- **After deduplication retention expires:** state that old deliveries can be treated as new, or reject events older than a declared cutoff. Indefinite deduplication requires indefinite identity retention or another durable authoritative ledger.
- **During upstream gaps:** run integration-specific recovery and report coverage gaps. Source-state reconciliation can restore current desired work, but does not necessarily reconstruct every historical event.
- **After input admission:** claims, fencing, checkpointing, and action recovery are separate concerns. Duplicate suppression at ingress does not make a tool call or external side effect idempotent.
- **Across stages:** accepted receipt, completed routing, admitted input, completed input, and completed delegated run are distinct milestones. Expose their relationships so users can locate the stalled stage.

## What the spike should prove

Use one source adapter and deterministic routes first. Demonstrate authenticated receipt, durable acknowledgment, duplicate delivery, two distinct events intentionally mapping to one work item, partial fan-out recovery, lost create response, deleted input with retained tombstone, destination recreation, explicit replay, and visible failure when identities conflict. Test ingress restart separately from worker restart. Include a documented provider-recovery path and its limitations.

Also compare `webhook -> router -> AgentRun` with `webhook -> router -> AgentSession -> AgentRun`. A one-off review may need only the former. A session earns its additional primitive by coordinating several inputs over time, retaining pending delegated work and decisions, and handling subsequent human or external input in that context. Event ingress is useful infrastructure for either path; it should not force every task into a permanent session.
