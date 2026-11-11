# Kubernetes-native event mechanisms for AgentSession

Research checked 2026-11-11. This note compares existing mechanisms; it does not select an architecture or change ADR 0025. No resources or external subscriptions were created. Configuration below is illustrative, with existing resource fields verified against primary documentation; it has not been applied to a cluster.

## Finding

We should evaluate an existing event platform before designing a new general-purpose receiver/router. **Argo Events is particularly close to the requested Kubernetes configuration model:** source CRs, a message bus, and rule/trigger CRs that can create custom resources. Knative offers a similar source/broker/subscription model with HTTP delivery to an application sink. Neither removes the need to define our durable admission, semantic work identity, retention, or agent-specific completion contracts.

The outstanding product question remains separate: should a deterministic rule address a named session, which then decides what work to perform, or should evaluation select destinations before any session receives an input? Neither Sensors nor Triggers inherently perform model-based reasoning. Do not turn that unanswered question into an implicit AgentRouter implementation decision.

## Argo Events: existing resources and actual source ownership

- **EventSource** configures one or more adapters. Its GitHub adapter programmatically registers webhooks; its Calendar adapter emits scheduled events; its Resource adapter watches Kubernetes resources; its generic Webhook adapter exposes an HTTP receiver. [GitHub source](https://argoproj.github.io/argo-events/eventsources/setup/github/), [Calendar source](https://argoproj.github.io/argo-events/eventsources/setup/calendar/), [Resource source](https://argoproj.github.io/argo-events/eventsources/setup/resource/)
- **EventBus** transports events between sources and sensors. A JetStream-backed EventBus can provision a NATS StatefulSet or connect to an existing service. Persistence and stream retention require explicit configuration; they are not supplied merely by naming a bus. [JetStream EventBus](https://argoproj.github.io/argo-events/eventbus/jetstream/)
- **Sensor** declares source dependencies, payload filters, Boolean conditions, and actions. Actions include HTTP requests and creation of arbitrary Kubernetes resources, including custom resources. Argo Workflows is not required for the generic Kubernetes/HTTP path. [Kubernetes object trigger](https://argoproj.github.io/argo-events/sensors/triggers/k8s-object-trigger/), [HTTP trigger](https://argoproj.github.io/argo-events/sensors/triggers/http-trigger/)

### GitHub EventSource shape

This uses existing Argo fields, with an illustrative repository and endpoint:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: EventSource
metadata:
  name: repository-events
  namespace: agent-events
spec:
  eventBusName: default
  service:
    ports:
      - name: github
        port: 12000
        targetPort: 12000
  github:
    reviews:
      repositories:
        - owner: example-org
          names: [example-app]
      webhook:
        endpoint: /github/reviews
        port: "12000"
        method: POST
        url: https://events.example.com
      events: [pull_request]
      apiToken:
        name: github-events
        key: token
      webhookSecret:
        name: github-events
        key: signing-secret
      insecure: false
      active: true
      contentType: json
```

The operator supplies the Secret and a reachable Ingress/OpenShift Route to the generated service. Providing API credentials enables registration; omitting them allows a manually registered webhook. The existing alternative `githubApp` fields are `privateKey` (Secret selector), `appID`, and `installationID`. Registering/installing that credential App is still an operator task. [Official GitHub EventSource example](https://raw.githubusercontent.com/argoproj/argo-events/stable/examples/event-sources/github.yaml)

Inspected source matches an existing hook or creates one and optionally deletes hooks on shutdown using `deleteHookOnFinish`. It explicitly describes races that can create duplicate hooks with multiple replicas and delete a hook during rolling updates. This is evidence of automation, not proof of our ownership/recovery contract. Audit the selected release's matching, pagination, rotation, deletion, and lost-response behavior. [GitHub adapter implementation, stable branch](https://raw.githubusercontent.com/argoproj/argo-events/stable/pkg/eventsources/sources/github/start.go)

### Sensor to a narrow admission adapter

An illustrative fixed-route experiment can send relevant PR events to a service that admits inputs for a configured named session. The path below is our proposed endpoint, not an Argo API or a settled new CRD:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Sensor
metadata:
  name: review-inputs
  namespace: agent-events
spec:
  eventBusName: default
  dependencies:
    - name: pr
      eventSourceName: repository-events
      eventName: reviews
      filters:
        data:
          - path: body.action
            type: string
            value: ["^(opened|reopened|synchronize)$"]
  triggers:
    - template:
        name: admit-review-input
        http:
          url: http://input-admission.agent-system.svc/admit/routes/review-main
          method: POST
          payload:
            - src:
                dependencyName: pr
                dataKey: body
                useRawData: true
              dest: payload
            - src:
                dependencyName: pr
                dataKey: headers
                useRawData: true
              dest: providerHeaders
      atLeastOnce: true
      retryStrategy:
        steps: 5
        duration: 2s
      policy:
        status:
          allow: [200, 202]
```

Payload filters are deterministic, with string matching interpreted as regular expressions. HTTP payload mappings and allowed response codes are existing capabilities. `useRawData` preserves objects instead of stringifying them. The adapter must authenticate its caller, bind the route/source, persist responsibility before success, compare duplicate intent, and retain delivery records after AgentInput deletion. Configure a durable failure destination and replay procedure before treating this sketch as reliable. [Data filters](https://argoproj.github.io/argo-events/sensors/filters/data/), [HTTP trigger schema and policy](https://argoproj.github.io/argo-events/sensors/triggers/http-trigger/), [Argo API reference](https://argoproj.github.io/argo-events/APIs/)

### Could the Sensor create AgentInput directly?

Yes, mechanically: install the future AgentInput CRD, grant the Sensor service account access, use `k8s.operation: create`, supply the resource under `source.resource`, and parameterize `metadata.name`. A generated name is unsuitable for repeated admission of the same work. [Resource triggering and parameterization](https://argoproj.github.io/argo-events/tutorials/02-parameterization/), [Sensor permissions](https://argoproj.github.io/argo-events/service-accounts/)

That does **not** implement the proposed durable contract. Inspected Kubernetes trigger code directly returns the create result; it does not turn `AlreadyExists` into a comparison of full immutable intent. Its update/patch operations can create missing objects and mutate existing ones, which is not equivalent to immutable input admission. [Kubernetes trigger implementation, master branch](https://raw.githubusercontent.com/argoproj/argo-events/master/pkg/sensors/triggers/standard-k8s/standard-k8s.go)

Therefore compare two prototypes: direct named creation to expose exact behavior, and HTTP/custom-trigger admission to enforce retained identities, protected admission, and recovery. An adapter here is substantially narrower than rebuilding provider sources, scheduling, filtering, and a bus.

### Important Argo limits

The current API defaults trigger execution to at-most-once and no retries; explicitly choose the intended settings. Argo's documented short-lived in-memory event-ID cache is not our permanent semantic ledger. With `A && B`, Sensors use the latest event from each dependency: earlier A events can be dropped while waiting for B. Do not use those conditions as a substitute for every accepted AgentInput being processed. [Trigger semantics API](https://argoproj.github.io/argo-events/APIs/), [Sensor delivery and dependency behavior](https://argoproj.github.io/argo-events/sensors/more-about-sensors-and-triggers/)

Calendar catch-up uses a ConfigMap holding the last successful event time and a configured lookback duration. That provides a concrete scheduling mechanism to test; define whether missed occurrences should replay individually or coalesce. [Calendar catch-up](https://argoproj.github.io/argo-events/eventsources/calendar-catch-up/)

No Jira-specific source appears in the inspected EventSource API. A generic receiver is possible, but its built-in static bearer/basic authentication does not automatically implement Jira's integration-specific signature/token verification or webhook registration/renewal. Jira needs a qualified adapter or an external integration that supplies verified events. [EventSource types](https://raw.githubusercontent.com/argoproj/argo-events/stable/pkg/apis/events/v1alpha1/eventsource_types.go), [Webhook authentication](https://argoproj.github.io/argo-events/eventsources/webhook-authentication/)

## Knative Eventing: concrete CR story

Sources produce CloudEvents; a **Broker** receives them; **Triggers** select and deliver them to addressable HTTP sinks. Ordinary Kubernetes Services can be sinks. Trigger filters cover CloudEvents attributes/extensions, not arbitrary event data; supported filter dialects depend on the Broker implementation. Payload classification/enrichment can happen in an explicit processor. [Source catalog](https://knative.dev/docs/eventing/sources/), [Triggers](https://knative.dev/docs/eventing/triggers/)

Assuming an explicitly configured durable Broker called `agent-events`, these are existing resource shapes:

```yaml
apiVersion: sources.knative.dev/v1
kind: PingSource
metadata:
  name: review-maintenance
spec:
  schedule: "0 * * * *"
  contentType: application/json
  data: '{"purpose":"check-pending-reviews"}'
  sink:
    ref:
      apiVersion: eventing.knative.dev/v1
      kind: Broker
      name: agent-events
---
apiVersion: eventing.knative.dev/v1
kind: Trigger
metadata:
  name: scheduled-review-inputs
spec:
  broker: agent-events
  filter:
    attributes:
      type: dev.knative.sources.ping
  subscriber:
    ref:
      apiVersion: v1
      kind: Service
      name: input-admission
    uri: /admit/routes/review-maintenance
```

PingSource has a cron schedule and fixed payload. This sketch does not establish complete catch-up after downtime; scheduled occurrence identity and missed-occurrence policy require qualification. A Trigger addresses a sink; it does not itself create arbitrary AgentInput resources. Our admission service performs that operation. [PingSource](https://knative.dev/docs/eventing/sources/ping-source/)

### Actual GitHub registration

Knative's separately installed **GitHubSource** extension registers repository/organization webhooks. Its documented CR fields include `eventTypes`, `ownerAndRepository`, `accessToken.secretKeyRef`, `secretToken.secretKeyRef`, and `sink.ref`; deleting the source removes its webhook. The documented deployment also requires Knative Serving and public domain configuration. Credentials and App/account access remain operator-provided. [GitHubSource walkthrough](https://github.com/knative/docs/tree/main/code-samples/eventing/github-source)

Treat maturity/version as an open qualification: the current source catalog labels GitHubSource Beta, while the extension README labels it Alpha, and the walkthrough contains older API examples. Pin a compatible extension release and inspect its actual installed CRD/controller before committing to its lifecycle guarantees. [GitHubSource repository](https://github.com/knative-extensions/eventing-github)

The source catalog does not supply a native Jira registration lifecycle. Camel-based or custom/ContainerSource integrations are possible but introduce their own configuration, packaging, auth, and recovery work; a Broker cannot register a Jira webhook merely from a Trigger.

### Delivery and dependencies

Broker delivery supports configurable retries, backoff, and dead-letter destinations, with implementation-dependent support. A dead-letter sink must itself persist responsibility. These delivery options do not guarantee unique AgentInput creation or external-effect uniqueness. [Knative failure delivery](https://knative.dev/docs/eventing/event-delivery/)

Knative Eventing requires installation of CRDs/controllers and the selected Broker data plane. Kafka-backed options additionally need Kafka; the documented in-memory channel is unsuitable for production durability. Eventing core can operate with ordinary Services, while a chosen source extension may add Serving requirements. [Eventing installation](https://knative.dev/docs/install/yaml-install/eventing/install-eventing-with-yaml/), [Eventing project](https://github.com/knative/eventing)

## Ordinary Kubernetes and OpenShift implications

Neither stack is an assumed cluster feature. Argo adds controllers/CRDs, source and Sensor workloads, and a bus/storage choice. Its install guide includes OpenShift-specific SCC/RBAC adjustments; qualify selected images and restricted-security compatibility rather than assuming the historical example is appropriate unchanged. Knative adds its own platform and Broker/source dependencies. Neither needs microVM suspension to enqueue inputs or wake ordinary session compute. [Argo installation](https://argoproj.github.io/argo-events/installation/)

A small owned integration remains an option if avoiding these dependencies is a product requirement. But then we own the notification/scheduling configuration API, provider lifecycle, queue/store, decision rules, failure recovery, and upgrades. For simple schedules, a Kubernetes CronJob can invoke the same admission contract; Kubernetes explicitly warns that schedule execution can duplicate or miss Jobs, so occurrence identity and catch-up remain application concerns. [CronJob limitations](https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/)

## What to grill before an ADR decision

1. **Where does the decision live?** Fixed rules deliver to a named session and that session reasons, versus a prior evaluation choosing destinations. A model classifier is an additional workload with a durable output, not a property of Sensor/Trigger filters.
2. **Which dependencies can we require?** Bundle Argo Events; integrate with an existing Knative platform; or support a small standalone path. Optional integrations can share one admission contract without pretending their guarantees are identical.
3. **Who authors the resources?** Users can directly manage platform CRs, or Hub can generate them from higher-level configuration. Decide whether platform-specific details are exposed before inventing wrapper CRDs.
4. **What must every event preserve?** Every occurrence, only latest desired state, or route-specific coalescing. This governs Sensor conditions, scheduling catch-up, deduplication keys, and replay.
5. **What is ours despite reuse?** Immutable input intent, destination incarnation, semantic work identity, admission gating, retained tombstones, partial fan-out responsibility, source recovery coverage, child-result correlation, and publication receipts.

A bounded comparison should prove one GitHub subscription, one timer, one Kubernetes child-completion source, and the same admission service. Exercise duplicate delivery, adapter/bus/consumer restart, exhausted delivery, input cleanup, hook recreation, and schedule downtime. Distinguish configured rule routing from session reasoning throughout. Select the platform only after observing these concrete paths; do not use a generic webhook happy path to imply the whole lifecycle is solved.
