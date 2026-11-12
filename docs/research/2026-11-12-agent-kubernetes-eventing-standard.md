# Kubernetes event-driven work: Argo CD's place

Checked 2026-11-12. Bounded primary-source research, building on the 2026-11-11 event-mechanisms note. No ADR/API decisions or external changes.

## Answer to “what is the Kubernetes standard?”

There is no single built-in Kubernetes external-event router assumed present on every cluster. The useful distinction is **event interoperability** versus **an installed eventing implementation**. CloudEvents specifies a common event envelope; it does not supply persistence, replay, provider registration, or exactly-once work. Argo Events and Knative Eventing are concrete optional systems with their own Kubernetes APIs, controllers, and delivery infrastructure. Selecting one is a deployment/product choice, not a requirement imposed by Kubernetes. [CloudEvents specification project](https://cloudevents.io/), [Knative Eventing overview](https://knative.dev/docs/eventing/).

Knative's documented shape is Source → Broker → Trigger → HTTP sink. The sink can be an ordinary Kubernetes Service; using Eventing does not require converting the agent session into a Knative Serving workload. This permits a thin input-admission service to receive CloudEvents while our controller continues reconciling AgentInput and AgentSession resources. That final integration is a proposal, not implemented behavior. [Knative Eventing overview](https://knative.dev/docs/eventing/).

Argo Events directly matches the requested multi-source, declarative automation shape: EventSource → EventBus → Sensor → trigger. Sources include generic webhooks, GitHub, schedules, and message systems. Triggers include HTTP and arbitrary Kubernetes resources. This does not require Argo CD, nor does the generic HTTP/Kubernetes path require launching an Argo Workflow. [Argo Events project](https://github.com/argoproj/argo-events), [architecture](https://argoproj.github.io/argo-events/concepts/architecture/), [Kubernetes object trigger](https://argoproj.github.io/argo-events/sensors/triggers/k8s-object-trigger/).

## Argo CD has three relevant, distinct roles

1. **Incoming repository change notification.** Argo CD's `/api/webhook` accepts supported Git/OCI provider events to accelerate application refresh instead of waiting for repository polling. Its documented Git setup has an operator create the provider webhook. ApplicationSet uses separate webhook configuration. This is evidence of a repository refresh mechanism, not a generic Jira/CVE event-routing API. [Argo CD webhook configuration](https://argo-cd.readthedocs.io/en/stable/operator-manual/webhook/).
2. **Outgoing application notifications.** Argo CD Notifications can send templated generic HTTP requests. Configuration lives in `argocd-notifications-cm`: `service.webhook.<name>`, `template.<name>`, and `trigger.<name>`; Application annotations subscribe to a trigger/service. Authentication headers/basic auth are configurable. Documented webhook retries cover network errors and HTTP 5xx, defaulting to three retries; the send returns an error on exhaustion. These settings do not establish a durable external-event replay ledger. [Webhook service](https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/services/webhook/).
3. **GitOps management of the event layer.** Argo CD deploys Kubernetes manifests/Helm/Kustomize from version-controlled desired state. Therefore an Argo CD Application can manage Argo Events resources or Knative resources once their CRDs/controllers and permissions exist. This is an architectural inference from its ordinary reconciliation behavior. Argo CD would manage subscriptions/configuration; the eventing system would handle occurrences. Avoid representing each ephemeral AgentInput as desired Git state unless that behavior is explicitly intended. [Argo CD operating model](https://argo-cd.readthedocs.io/en/stable/).

The resulting possible integration is: Argo CD deployment-status notification → the same event ingress used by GitHub/Jira → durable AgentInput admission → session decision. This adds deployment context without making Argo CD the general router. Official examples demonstrate notifying on successful healthy deployment and using `oncePer` revision; that revision suppression still differs from our retained semantic admission contract. [Notification example](https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/examples/).

## Own webhook endpoints and connector limits

Argo's generic webhook EventSource creates an HTTP-serving pod/service. Its walkthrough exposes this using Ingress, OpenShift Route, or port forwarding. Multiple sources/endpoints can feed Sensors. This is a ready-made endpoint mechanism; provider-specific authentication, registration/renewal, source identity, and recovery must still be qualified. [Argo quick start](https://argoproj.github.io/argo-events/quick_start/).

The existing GitHub EventSource is a concrete connector with repository/event configuration and credentials. Prior inspected implementation registers hooks. No Jira-specific EventSource appeared in the inspected API/catalog; generic HTTP reception alone does not implement Jira registration or authentication. Check the chosen integration rather than assuming every named provider has equivalent lifecycle support. [GitHub source](https://argoproj.github.io/argo-events/eventsources/setup/github/), [EventSource API](https://argoproj.github.io/argo-events/APIs/).

Apache Camel also offers an existing Jira Source Kamelet, documented as producing new-issue data as JSON with a JQL filter. It is another connector candidate, not evidence that Jira support is built into Argo Events. The selected Jira deployment's authentication, restart cursor, replay, and duplicate behavior still need qualification. This research did not execute the connector or verify durable discovery after downtime. Avoid copying the documentation's Kubernetes binding example until its compatibility with the chosen Camel K release is checked. [Camel Jira Source catalog](https://camel.apache.org/camel-kamelets/4.22.x/jira-source.html).

## TPA / Trustify tie-in: bounded finding

Official Trusted Application Pipeline 1.2 documentation lists both Trusted Profile Analyzer and Argo CD, assigning them different roles: SBOM/security analysis versus application deployment/lifecycle. This establishes ecosystem coexistence, not a finding-notification integration. [RHTAP technologies, official PDF](https://docs.redhat.com/en-us/documentation/red_hat_trusted_application_pipeline/1.2/pdf/about_red_hat_trusted_application_pipeline/Red_Hat_Trusted_Application_Pipeline-1.2-About_Red_Hat_Trusted_Application_Pipeline-en-US.pdf).

Targeted searches of Red Hat documentation and the Trustify repository did not locate an explicit supported TPA/Trustify vulnerability-event → Argo CD integration. This is a bounded search result, not proof of absence. Keep three questions separate: installing TPA using GitOps, using TPA in a supply-chain pipeline, and emitting new affected-application findings into an agent event system. Only the last supplies the requested agent input source; an adapter or scheduled reconciliation remains possible pending the TPA API/event evidence.

## Proposed direction to discuss, not select yet

Use one narrow durable input-admission contract as the agent-system boundary. Compare an Argo Events integration and an existing Knative installation against it; keep a direct authenticated webhook adapter possible for smaller deployments. Preserve source identity and routing intent, compare retries against existing immutable intent, and retain deduplication records after input cleanup. Event platform reuse can supply connectors, filtering, scheduling, buffering, and retries; it does not by itself decide whether repeated provider events represent one unit of agent work.

The next product question is which configuration surface users should own: native eventing CRs, a higher-level configuration generated by Hub, or a small direct integration. Establish that before mandating a particular eventing stack or creating another general-purpose router CRD.

A useful comparison experiment would deliver the same event through a direct
webhook adapter and one eventing platform, repeat it after a receiver restart,
and verify that both paths return the same accepted AgentInput. A separate
updated event must remain eligible for new work. Repeat fan-out to two sessions
and crash between deliveries to test recovery of the remaining recipient. This
tests the shared admission boundary without committing to public CRD names or
claiming that transport-level deduplication supplies our work identity.
