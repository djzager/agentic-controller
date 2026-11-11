---
marp: true
theme: konveyor
size: 16:9
paginate: true
title: AgentSession and durable event routing
description: Independent session proposal with event-admission and value gates for ADR 0025
author: David Zager
footer: 'ADR 0025 · Proposed'
style: |
  section {
    font-family: Arial, Helvetica, sans-serif;
    font-size: 30px;
    line-height: 1.35;
    padding: 48px 64px 72px;
  }
  h1 { font-size: 44px; margin: 0 0 28px; line-height: 1.15; }
  h2 { font-size: 31px; margin: 12px 0 10px; }
  p { margin: 0 0 22px; }
  li { margin-bottom: 16px; }
  footer, section::after { font-size: 18px; color: #a0a0a0; }
  section.lead { background: #1a1a2e; }
  section.lead h1 { font-size: 56px; max-width: 1040px; margin: 24px 0; }
  section.lead p { font-size: 30px; }
  section.lead img { width: 200px; height: auto; }
  table { display: table; font-size: 27px; line-height: 1.25; }
  table th, table td { padding: 15px 17px; }
  section.compact table { font-size: 25px; }
  section.compact li { margin-bottom: 12px; }
  section.references { font-size: 26px; }
  section.references li { margin-bottom: 17px; }
  pre { font-size: 25px; line-height: 1.5; }
  code { font-size: 0.88em; }
  blockquote { margin: 22px 0 0; font-size: 29px; border-radius: 0; }
---

<!-- _class: lead -->
<!-- _paginate: false -->
<!-- _footer: '' -->

![Konveyor](konveyor-logo.svg)

# AgentSession and event routing

Prove the value of continuing ownership

Exploratory proposal · November 11, 2026

<!--
ADR 0025 is separate from AgentLearning. The original proposal did not establish
the event-to-input path or enough session-specific value. This version makes
both explicit decision gates rather than assuming a new primitive is justified.
Source: docs/adr/0025-agent-sessions-and-event-routing.md. Existing repository logo.
-->

---

# When a finite run is enough

- Review one pull-request revision.
- Start a task when a Jira issue is created.
- Fan out one advisory into independent remediation workflows.

**Webhook → durable router → AgentRun / AgentWorkflowRun**

Receiving an event or sharing learnings does not itself require a session.

<!--
These are control-path use cases, not claims about implemented integrations.
Event receipt, deduplication, routing, and recovery are useful infrastructure
for finite work too. ADR 0024 already makes shared learning available to runs.
The spike must compare against this simpler baseline.
Source: ADR 0025, Use cases: when a session adds value.
-->

---

# When continuing ownership may help

| Use case | State that spans inputs |
| --- | --- |
| **PR review** | Earlier decisions, human feedback, revisions, pending reviews |
| **CVE remediation** | Overlapping advisories, active fixes, exceptions, verification |
| **Jira work** | Comments, approvals, delegated outcomes, later instructions |

**Hypothesis:** one continuing owner makes this coordination simpler.

<!--
The hypothesis needs evidence. Conversation memory alone does not stop an old
review from publishing against a new head; a source-version/action policy is
also required. If explicit task state and finite runs are simpler and sufficient,
use them rather than declaring every event-driven workload a session.
Source: ADR 0025, Use cases: when a session adds value.
-->

---

# The missing ingress layer

```text
Authenticated webhook / poll adapter / human request
  → durable event receipt
  → committed route and destination plan
  → recorded delivery and AgentInput admission
  → controller wake-up and harness worker
```

**AgentRouter stays available while session compute sleeps.**

It is a component plus route configuration; a public router CRD is not selected.

<!--
Propose a separate receiver/router workload alongside the execution controller,
with ordinary Service and Ingress/OpenShift Route access. A Hub adapter/resolver
supplies inventory context. The execution controller stays domain-agnostic and
outside ACP. Start with deterministic routes, not a required model classifier.
Source: ADR 0025, Proposed responsibilities.
-->

---

# Commit a receipt before acknowledging

1. Authenticate the source and normalize its identity and intent.
2. Persist the receipt and pinned routing configuration.
3. Return success; process routing and execution asynchronously.

**GitHub does not automatically retry failed deliveries.**
Jira retries are bounded. Each adapter needs a qualified recovery path.

<!--
A successful response means durable receipt, not routing or task completion.
On persistence failure return failure. A lost response after commit resolves to
the same receipt on retry. Identity/payload conflict is visible, not an overwrite.
GitHub recovery uses delivery APIs/requested redelivery with the required access
and retention window. Jira recovery depends on registration/authentication type.
Sources: https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries
https://developer.atlassian.com/cloud/jira/platform/webhooks/
Full caveats: docs/research/2026-11-11-agent-event-ingress.md.
-->

---

# Event identity and work identity

| Identity | Purpose |
| --- | --- |
| **Provider event key** | Recognize retries of one authenticated event |
| **Semantic work key** | Coalesce distinct events requesting the same operation |
| **Replay / request ID** | Intentionally ask for new work |

PR example: repository + PR + head SHA + review purpose.

An issue ID or raw payload hash is not a universal deduplication key.

<!--
Scope provider IDs by configured source/tenant. Jira's webhook identifier is
retry-stable within a tenant; GitHub preserves its delivery ID on redelivery.
CloudEvents source/id is useful but alone does not guarantee retry-stable IDs.
Keep receipt provenance separate: several receipts can map to one canonical
intent. Comments and issue updates require their own identities, not just issue ID.
Sources: ADR 0025 section 3; provider/spec links in the ingress research note.
-->

---

# Commit the route before fan-out

Example: an advisory should reach **20 sessions**.

- Persist the configuration revision, intent, and target UIDs.
- Create 8 inputs, then the router crashes.
- Recover the existing 8 and finish the remaining 12.

Reuse the committed plan even if inventory or routing configuration changed.

<!--
Numbers are an illustrative failure scenario, not measured results. A plan must
be committed before any delivery. Competing planners use an atomic/CAS winner.
A deleted or unauthorized target becomes a visible failure, not a redirect to a
replacement session with the same name. Explicit replay can re-evaluate routing.
Source: ADR 0025, Commit a routing plan before fan-out.
-->

---

# Deterministic AgentInput creation

```text
inputKey = encode(workKey, targetSessionUID,
                  stableRoute, purpose, replayEpoch, ...)
inputName = "input-" + hash(inputKey)
```

Create by the exact name. On **AlreadyExists**, compare the full identity,
canonical intent digest, and target UID.

A match reuses the input. A mismatch is a conflict, never an overwrite.

<!--
The ADR specifies versioned unambiguous encoding, tenant/source scope, and a
bounded hash suffix. The shortened name is only an index. Do not use generateName.
After a timeout/lost create response, use the same lookup/create procedure.
Stable route identity is distinct from config revision; edits do not accidentally
turn a retry into new work. New destination incarnations have different UIDs.
Sources: ADR 0025 section 4; https://kubernetes.io/docs/concepts/overview/working-with-objects/names/
-->

---

# Close the create-and-record gap

1. Reserve the unique delivery in the durable ledger.
2. Create the input behind an admission gate.
3. Record its resource UID and commit admission.
4. Publish readiness for the session worker.

**The input cannot execute or normally disappear before admission is committed.**

<!--
If the router creates an input, loses its receipt, and the input executes and is
then garbage-collected, a retry can recreate work despite deterministic naming.
The gate and cleanup protocol close that gap. Recovery repairs a lost readiness
update after ledger commit. Exact CR conditions/finalizers and authorization are
spike outputs; current controller behavior does not already provide this protocol.
Source: ADR 0025, Make Kubernetes creation converge on one logical input.
-->

---

# Late retries, replay, and ordering

- Keep delivery records after AgentInput cleanup.
- Give intentional replay a new explicit identity and lineage.
- State the retention horizon for duplicate suppression.
- Handle out-of-order source revisions with an explicit policy.

**One logical input per work identity and destination, within that horizon.**

<!--
Names can be reused after Kubernetes object deletion, so a name alone is not a
permanent deduplication ledger. Do not blindly recreate a missing admitted input.
After dedup retention expires, reject old work or document it can be treated as
new. A PR's newer head is not a retry; check source version before publishing a
version-specific result. None of this promises exactly-once external effects.
Sources: ADR 0025 sections 4 and 5; Kubernetes names/IDs documentation.
-->

---

# Session worker responsibilities

**Claim → restore context → invoke → persist → complete**

- Retain earlier decisions and outstanding child references.
- Process a later input while delegated runs continue.
- Correlate child outcomes through durable admission.
- Reuse recorded results; expose uncertain external effects.

Sleep between completed inputs. AgentLearning is an optional integration.

<!--
The controller wakes compute. The deterministic worker belongs to the harness;
the model does not claim inputs or refresh approved membership. With learnings,
select before each new input and retain the same set on retry. Without them,
use existing skills/static context. One active input initially, with ownership
fencing, explicit scheduling, and recovery rather than blind whole-prompt replay.
Source: ADR 0025, Session execution and recovery.
-->

---

# Two gates for the spike

**Gate 1 — Reliable event admission**
One source adapter, committed routing, duplicate-safe direct-run delivery,
partial fan-out recovery, retained ledger, and provider recovery.

**Gate 2 — Useful session continuity**
Compare finite runs with a session handling a new revision, human follow-up,
and out-of-order child results across a restart.

Ingress can succeed independently. Session adoption needs evidence from both.

<!--
For a bounded prototype, use a single active router with retained SQLite and
transactional uniqueness. This is not a production HA/storage decision. Test
source/store/Kubernetes failure boundaries, not just a happy-path webhook.
Use deterministic fixtures first, then a pinned real runtime for continuity.
A publication fixture must enforce expected revision and stable operation ID;
a conversation is not a publication fence.
Source: docs/spikes/agent-session-event-routing.md.
-->

---

# Decisions still needed

**Review now:** session-specific use cases and the ingress/delivery contract.

**Spike returns:** route/work-key policies, admission mechanics, storage/HA,
provider coverage, retention, runtime recovery, and operational ownership.

If finite runs with explicit task state are sufficient, use that path.

No dependency on AgentLearning, IDE integration, or VM suspension.

<!--
Agent Sandbox remains a hard dependency. The session proposal does not require
microVM snapshots; ordinary persisted state supports sleeping between completed
inputs. Future backends and IDE/CLI work remain separate qualifications. The
router may ultimately reuse platform infrastructure, but identity, recovery,
retention, and delivery-state contracts remain necessary.
Source: ADR 0025, Concrete first implementation to investigate; Review and decision gate.
-->

---

<!-- _class: references -->

# Review material and primary sources

- ADR 0025: `docs/adr/0025-agent-sessions-and-event-routing.md`
- Spike: `docs/spikes/agent-session-event-routing.md`
- [GitHub failed webhook deliveries](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries)
- [Jira Cloud webhook delivery and retries](https://developer.atlassian.com/cloud/jira/platform/webhooks/)
- [Kubernetes object names and identities](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/)

Exact provider limits and limitations are in the ingress research note.

<!--
Research: docs/research/2026-11-11-agent-event-ingress.md, checked 2026-11-11.
GitHub recovery has a finite redelivery window. Jira's failed-webhook API is
Experimental and not generally available for all integration types. CloudEvents
v1.0.2 permits, rather than mandates, reuse of IDs on duplicate resend. None of
the documentation or this proposal is evidence that our implementation has run.
-->
