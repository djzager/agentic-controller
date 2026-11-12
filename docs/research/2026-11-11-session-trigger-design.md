# AgentSession notification and decision mechanisms: research frame

Date: 2026-11-11. Working material for a research-and-grilling session, not an
accepted API design. The current [session ADR](../adr/0025-agent-sessions-and-event-routing.md)
is a draft. Its bespoke receiver, SQLite prototype, and GitHub-first experiment
must be compared with existing mechanisms before being treated as the direction.
No new CRDs or source registrations are implemented here.

## Supporting evidence

- [Wake-up scheduling and the DeepSeek routing prototype](2026-11-11-agent-wake-and-decisions.md): persisted timers, optional assessment, and execution responsibilities.
- [Kubernetes event mechanisms](2026-11-11-agent-kubernetes-event-mechanisms.md): real Argo/Knative CR examples, provider-registration ownership, dependencies, and delivery limits.
- [Advisory ingress](2026-11-11-agent-advisory-ingress.md): affected-repository alerts versus global advisories and the missing applicability step.
- [Provider webhook lifecycle](2026-11-11-agent-webhook-lifecycle.md): GitHub and Jira registration/authentication distinctions.
- [Kubernetes eventing conventions and Argo CD](2026-11-12-agent-kubernetes-eventing-standard.md): common event formats, reusable receivers/connectors, deployment notifications, and the unverified TPA integration.
- [CodeRabbit review policy](2026-11-12-agent-review-policy-coderabbit.md): configuration precedence, automatic rereviews, comment-history behavior, and the limits of documented publication guarantees.

Existing-resource examples are verified against documentation, not executed.
They are evidence for the design discussion, not a selected platform or a
claim that our future AgentInput admission service already exists.

## The product contract to establish

Applying configuration must lead to an observable path from source setup to
accepted AgentInput, session wake-up, a recorded decision, and its outcome.
The user must be able to see which part is configured, ready, pending, failed,
or intentionally ignored. A hand-created input that wakes a pod proves only
the last part of notification delivery.

Separate these responsibilities before choosing public resource names:

- Source configuration describes what to listen to and how: GitHub/Jira
  subscription or poller, credentials, endpoint, events/filter, provider identity,
  and recovery position. One source may serve multiple sessions.
- A binding describes which source events can reach which authorized session,
  their stable work identity, and how event data becomes input. Predicates and
  authorization are deterministic even when later assessment uses a model.
- A schedule records a future wake-up independently of session compute. It
  needs an absolute due time or recurrence with time zone, payload, target,
  cancellation/revision identity, and a missed-occurrence policy.
- The session owns continuing context and the decision about work. A configured
  recipient can itself assess the event and route work to other permitted
  sessions or finite invocations.

These are configuration responsibilities, not a decision to introduce three
new public CRDs. Existing eventing resources may satisfy some of them; our
contract must say what an adapter still has to provide.

## Concrete source paths to evaluate

GitHub PR events need a configured provider subscription, reachable receiver,
and input admission. Compare an Argo Events GitHub EventSource and Sensor, a
Knative GitHubSource and event sink, and a small owned source adapter. Provider
registration, public ingress, and durable admission are distinct responsibilities.

Jira needs a chosen subscription/authentication model or a cursor-based poller.
A generic webhook receiver does not install that subscription. In the sibling
Hub checkout, `internal/tracker/jira.go` creates migration tickets and refreshes
known ticket keys; the inspected path is not new-issue discovery. That finding
is a bounded source inspection, not a complete audit of every Hub integration.

An affected-repository alert is a smaller vulnerability experiment than a global
advisory feed. Global advisories additionally require package/version inventory
and applicability resolution before application fan-out. Hub's `/dependencies`
API, as inspected in `internal/api/dependency.go`, models application-to-application
edges; do not mistake it for software package/version inventory.

For a timed wake-up, persist the schedule before saying it is set. A scheduler
admits an input keyed by schedule identity/revision and intended occurrence,
not the retry wall-clock time. Restart scans and retained delivery state must
close the gap between a due timer and durable acceptance. A fired timer alone
is not completed work. A model may request the schedule through a bounded tool;
the scheduler and input admission remain deterministic.

## SBOM service candidates remain open

TPA/Trustify and Dependency-Track may supply the vulnerability-correlation step
before events reach an agent. They can consume application SBOMs and provide
findings or changes that an adapter maps to application/session inputs. This is
an alternative to building the advisory-to-package matcher in our event layer.

[TPA/Trustify research](2026-11-11-agent-tpa-trustify-notifications.md) verifies a
released upstream change stream with finite replay retention, while leaving
Red Hat product support unverified. The stream reports document changes; an
adapter still queries affectedness and maps artifacts to applications.
[Dependency-Track research](2026-11-11-agent-dependency-track-notifications.md)
identifies project-aware finding notifications and distinguishes current v5
contracts from legacy v4 documentation. Its exact deployed-version behavior
still requires an integration test.

Neither candidate is selected. Source ownership, required delivery semantics,
and public configuration remain open; the user has not committed to what those
must look like. A concrete notification experiment should inform the next design
question rather than turn these candidate capabilities into requirements.

## Decision order for the grilling session

**Direction (2026-11-12):** New PR commits normally trigger review. Automatic
review and treatment of older running reviews should be configurable per
repository/task. Repository-file settings and defaults on the event-to-execution
binding are candidates, with precedence and trusted configuration revision still
open. Separate repeated event execution, repeated findings across reviews, and
uncertain comment publication. Persistent review history can serve finite runs
without requiring an AgentSession.

**Direction (2026-11-12):** Broaden AgentInput beyond session delivery: one
durable request can invoke an Agent or AgentWorkflow, recording its resulting
run, or address an existing AgentSession. Start with a GitHub PR-opened webhook
producing a finite review AgentRun and a correlated published result. Event
matching and parameter mapping are configurable; source authentication and
allowed destinations remain deterministic. Exact public schemas and source
registration ownership still need decisions. This direction does
not select an eventing platform or require sessions for the first example.

**Research update (2026-11-12):** Multiple ingestion paths should be evaluated
against one durable input-admission contract. CloudEvents is an interoperability
option; Argo Events and Knative are optional installed platforms. Argo CD can
manage their configuration and emit deployment notifications. Camel's Jira
connector is another candidate. No eventing dependency, connector, or public
configuration shape has been selected.

**Resolved (2026-11-11):** These are composable paths rather than exclusive
architectures. An AgentSession can itself be the routing agent. Configured
delivery addresses either a specialist or a session that evaluates the event
and chooses subsequent work. The routing role does not require another resource
kind; source delivery and model-driven routing remain distinct responsibilities.

Next, examine shared source ownership versus session-local trigger
configuration, existing eventing dependencies versus a small built-in mechanism,
Jira registration/polling, schedule creation permissions and missed wakes, then
the concrete advisory source and applicability mapping. Work through these one
at a time, with actual resource examples and failure cases; do not resolve them
implicitly by writing a complete ADR or selecting a database.
