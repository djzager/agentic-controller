/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	konveyoriov1alpha1 "github.com/konveyor/agentic-controller/api/v1alpha1"
)

const (
	// verificationJobPrefix is the prefix for verification Job names.
	verificationJobPrefix = "gw-verify-"

	// DefaultVerificationImage is the default image used for Gateway
	// verification when no override is configured. In production, the
	// controller should use the agentic-controller-agent image from
	// this repository.
	DefaultVerificationImage = "quay.io/konveyor/agentic-controller-agent:latest"

	// verificationHTTPCodePattern requires a 2xx status from the probe.
	verificationHTTPCodePattern = "^2"

	// anthropicAPIVersion is sent as the anthropic-version header when
	// probing native Anthropic endpoints, which reject Authorization: Bearer.
	anthropicAPIVersion = "2023-06-01"

	// endpointModelsProbe is the models endpoint the connectivity probe
	// requests. All currently-supported providers expose an
	// OpenAI-compatible /v1/models under $LLM_ENDPOINT.
	endpointModelsProbe = "$LLM_ENDPOINT/v1/models"

	// Provider identifiers in normalized form (see normalizeProvider), kept
	// in sync with the harness providerEnv switch.
	providerAnthropic  = "anthropic"
	providerOpenAI     = "openai"
	providerXAI        = "xai"
	providerGCPVertex  = "gcp_vertex_ai"
	providerAWSBedrock = "aws_bedrock"

	// verificationDeadline bounds one verification Job. The probe caps curl at
	// 10s; the deadline covers image pull and, crucially, turns a pod that is
	// never admitted (restricted Pod Security, an unpullable image) into a
	// Failed Job instead of leaving the Gateway stuck on Verifying.
	verificationDeadline = int64(120)

	// verifyContainerName is the name of the verification Job's container,
	// whose termination message carries the probe diagnostic.
	verifyContainerName = "verify"

	// Ready-condition reasons for connectivity verification outcomes.
	reasonConnectionVerified   = "ConnectionVerified"
	reasonConnectionFailed     = "ConnectionFailed"
	reasonAuthenticationFailed = "AuthenticationFailed"
	reasonEndpointUnreachable  = "EndpointUnreachable"
)

// GatewayReconciler reconciles a Gateway object.
type GatewayReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// apiReader reads verification pods directly from the API server,
	// bypassing the cache. Two reasons the cache can't serve this read: the
	// manager's Pod cache is restricted to sandbox pods (see
	// SandboxPodCacheOptions), so verification pods are never cached; and even
	// if they were, the probe's termination message is written moments before
	// its Job flips to Complete/Failed, so the cached pod could still lack the
	// terminated container status. Set from mgr.GetAPIReader() in
	// SetupWithManager.
	apiReader client.Reader

	// VerificationImage overrides the container image used for
	// connectivity verification Jobs. Defaults to DefaultVerificationImage.
	VerificationImage string
}

// +kubebuilder:rbac:groups=konveyor.io,resources=gateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=konveyor.io,resources=gateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=konveyor.io,resources=gateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile handles Gateway reconciliation.
//
// The controller verifies gateway connectivity by:
//  1. Checking that the referenced credential Secret exists
//  2. Creating a verification Job that tests the endpoint using the
//     agent base image
//  3. Updating status based on the Job result
func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var gateway konveyoriov1alpha1.Gateway
	if err := r.Get(ctx, req.NamespacedName, &gateway); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("Reconciling Gateway", "name", gateway.Name)

	original := gateway.DeepCopy()
	gateway.Status.ObservedGeneration = gateway.Generation

	// Step 1: Check the credential Secret exists.
	secretKey := types.NamespacedName{
		Namespace: gateway.Namespace,
		Name:      gateway.Spec.CredentialRef.SecretName,
	}
	var secret corev1.Secret
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		if errors.IsNotFound(err) {
			gateway.Status.ConnectionVerified = false
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: gateway.Generation,
				Reason:             "CredentialSecretNotFound",
				Message:            fmt.Sprintf("Secret %q not found", gateway.Spec.CredentialRef.SecretName),
			})
			return r.patchStatus(ctx, &gateway, original)
		}
		return ctrl.Result{}, err
	}

	// Check the expected key exists in the Secret. A keyless credentialRef
	// means the whole Secret is the credential (multi-variable, e.g. AWS
	// SigV4) - then it just must not be empty.
	if gateway.Spec.CredentialRef.Key != "" {
		if _, ok := secret.Data[gateway.Spec.CredentialRef.Key]; !ok {
			gateway.Status.ConnectionVerified = false
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: gateway.Generation,
				Reason:             "CredentialKeyNotFound",
				Message: fmt.Sprintf("Key %q not found in Secret %q",
					gateway.Spec.CredentialRef.Key, gateway.Spec.CredentialRef.SecretName),
			})
			return r.patchStatus(ctx, &gateway, original)
		}
	} else if len(secret.Data) == 0 {
		gateway.Status.ConnectionVerified = false
		meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: gateway.Generation,
			Reason:             "CredentialSecretEmpty",
			Message: fmt.Sprintf("Secret %q has no data keys",
				gateway.Spec.CredentialRef.SecretName),
		})
		return r.patchStatus(ctx, &gateway, original)
	}

	// Step 2: If already verified (or failed) for the current generation,
	// skip re-verification. A spec change (new generation) will re-trigger.
	readyCond := meta.FindStatusCondition(gateway.Status.Conditions, ConditionTypeReady)
	if readyCond != nil &&
		readyCond.ObservedGeneration == gateway.Generation &&
		isTerminalReadyReason(readyCond.Reason) {
		return ctrl.Result{}, nil
	}

	// Clean up verification Jobs from prior generations. If a Gateway
	// spec changes while verification is queued/running, the old Job
	// is orphaned because completion events reconcile the new generation.
	var oldJobs batchv1.JobList
	if err := r.List(ctx, &oldJobs,
		client.InNamespace(gateway.Namespace),
		client.MatchingLabels{"konveyor.io/gateway": gateway.Name},
	); err != nil {
		return ctrl.Result{}, err
	}
	currentJobName := fmt.Sprintf("%s%s-gen%d", verificationJobPrefix, gateway.Name, gateway.Generation)
	for i := range oldJobs.Items {
		if oldJobs.Items[i].Name != currentJobName {
			if err := r.Delete(ctx, &oldJobs.Items[i],
				client.PropagationPolicy(metav1.DeletePropagationBackground),
			); client.IgnoreNotFound(err) != nil {
				logger.V(1).Info("Failed to delete stale verification Job",
					"job", oldJobs.Items[i].Name)
			}
		}
	}

	// Step 3: Check for an existing verification Job.
	// Include generation in name to avoid collisions when re-verifying.
	jobName := currentJobName
	jobKey := types.NamespacedName{Namespace: gateway.Namespace, Name: jobName}
	var job batchv1.Job
	if err := r.Get(ctx, jobKey, &job); err != nil {
		if errors.IsNotFound(err) {
			// No verification Job exists - create one.
			if err := r.createVerificationJob(ctx, &gateway, jobName); err != nil {
				logger.Error(err, "Failed to create verification Job")
				return ctrl.Result{}, err
			}
			gateway.Status.ConnectionVerified = false
			meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: gateway.Generation,
				Reason:             "Verifying",
				Message:            "Connectivity verification in progress",
			})
			return r.patchStatus(ctx, &gateway, original)
		}
		return ctrl.Result{}, err
	}

	// Step 3: Check the Job status.
	if isJobComplete(&job) {
		succeeded := isJobSucceeded(&job)

		// Read the probe's diagnostic from the pod's termination message
		// before deleting the Job, so the failure cause (HTTP code or
		// transport error) survives on status. Falls back to a generic
		// message when no diagnostic is available.
		diag := r.verificationDiagnostic(ctx, gateway.Namespace, jobName, job.UID)
		reason, message := gatewayVerifyReason(diag, gateway.Spec.Endpoint, jobName, succeeded)

		status := metav1.ConditionFalse
		if succeeded {
			status = metav1.ConditionTrue
		}
		gateway.Status.ConnectionVerified = succeeded
		meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             status,
			ObservedGeneration: gateway.Generation,
			Reason:             reason,
			Message:            message,
		})

		// Clean up the completed Job.
		if err := r.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
			logger.Error(err, "Failed to delete verification Job")
		}
	} else {
		// Job still running.
		gateway.Status.ConnectionVerified = false
		meta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: gateway.Generation,
			Reason:             "Verifying",
			Message:            "Connectivity verification in progress",
		})
	}

	return r.patchStatus(ctx, &gateway, original)
}

// knownProviders is the set of provider identifiers (normalized form) the
// controller and harness recognize. Providers outside this set still verify
// with the OpenAI-compatible default, but the mismatch is logged — mirroring
// the fallthrough warning in providerEnv (harness/internal/goose/lifecycle.go).
var knownProviders = map[string]bool{
	providerAnthropic:  true,
	providerOpenAI:     true,
	providerXAI:        true,
	providerGCPVertex:  true,
	providerAWSBedrock: true,
}

// normalizeProvider lowercases and converts hyphens to underscores so provider
// matching stays in lockstep with the harness (providerEnv in
// harness/internal/goose/lifecycle.go), which keys off the same normalization.
func normalizeProvider(provider string) string {
	return strings.ReplaceAll(strings.ToLower(provider), "-", "_")
}

// gatewayVerificationCurlCommand builds the shell command used by the
// verification Job. Anthropic's native API requires x-api-key +
// anthropic-version, so it gets a dedicated auth header; every other provider
// uses the OpenAI-compatible default (Authorization: Bearer against
// /v1/models). When includeAuth is false the auth header is omitted so keyless
// gateways are probed for reachability without an empty credential.
//
// The command captures curl's HTTP code and exit status and writes a compact
// diagnostic to the pod's termination message (/dev/termination-log) so the
// controller can surface the failure cause on the Gateway status:
//   - "ok code=<n>"        a 2xx response (exit 0)
//   - "auth code=<n>"      HTTP 401/403 - bad or missing credential
//   - "http code=<n>"      any other non-2xx response
//   - "unreachable rc=<n>" curl transport error (DNS, refused, timeout, ...)
//
// The endpoint and key stay in env vars ($LLM_ENDPOINT, $LLM_API_KEY) and are
// never interpolated into the command string, keeping the probe injection-safe.
func gatewayVerificationCurlCommand(provider string, includeAuth bool) string {
	curl := "curl -sk --max-time 10 -o /dev/null -w '%{http_code}'"
	if includeAuth {
		curl += gatewayVerificationAuthHeader(provider)
	}
	curl += ` "` + endpointModelsProbe + `"`

	// Classify 401/403 as an authentication failure only when the probe
	// actually sent a credential. A keyless credentialRef (empty key, e.g.
	// AWS SigV4) sends no Authorization header, so a 401/403 there is just
	// another non-2xx and must not tell the user to check an API key that
	// isn't in the secret.
	authArm := ""
	if includeAuth {
		authArm = `
401|403) echo "auth code=$code" > /dev/termination-log ;;`
	}

	return "code=$(" + curl + `); rc=$?
if [ "$rc" -ne 0 ]; then echo "unreachable rc=$rc" > /dev/termination-log; exit 1; fi
if echo "$code" | grep -qE '` + verificationHTTPCodePattern + `'; then echo "ok code=$code" > /dev/termination-log; exit 0; fi
case "$code" in` + authArm + `
*) echo "http code=$code" > /dev/termination-log ;;
esac
exit 1`
}

// gatewayVerificationAuthHeader returns the provider-specific auth header
// snippet (curl -H flags). Only Anthropic deviates from the OpenAI-compatible
// default (Authorization: Bearer) today, because its native API rejects
// Bearer; add new deviating providers as cases here.
func gatewayVerificationAuthHeader(provider string) string {
	switch normalizeProvider(provider) {
	case providerAnthropic:
		return ` -H "x-api-key: $LLM_API_KEY" -H "anthropic-version: ` + anthropicAPIVersion + `"`
	default:
		return ` -H "Authorization: Bearer $LLM_API_KEY"`
	}
}

// createVerificationJob creates a Job that verifies connectivity to the
// gateway endpoint using the agent base image.
func (r *GatewayReconciler) createVerificationJob(
	ctx context.Context,
	gateway *konveyoriov1alpha1.Gateway,
	jobName string,
) error {
	image := r.VerificationImage
	if image == "" {
		image = DefaultVerificationImage
	}

	// The verification Job runs a simple curl against the endpoint.
	// The agent base image includes curl. Only 2xx counts as success so
	// 401/403 (invalid or missing API key) fail verification instead of
	// marking ConnectionVerified. Keyless credentialRef (empty key,
	// e.g. AWS SigV4) omits the auth header entirely - an empty
	// credential would 401 under the ^2 check.
	includeAuth := gateway.Spec.CredentialRef.Key != ""
	if !knownProviders[normalizeProvider(gateway.Spec.Provider)] {
		log.FromContext(ctx).Info(
			"unrecognized gateway provider; verifying with default "+
				"Authorization: Bearer against /v1/models",
			"provider", gateway.Spec.Provider,
		)
	}
	env := []corev1.EnvVar{{Name: "LLM_ENDPOINT", Value: gateway.Spec.Endpoint}}
	if includeAuth {
		env = append(env, corev1.EnvVar{
			Name: "LLM_API_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: gateway.Spec.CredentialRef.SecretName,
					},
					Key: gateway.Spec.CredentialRef.Key,
				},
			},
		})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: gateway.Namespace,
			Labels: map[string]string{
				labelManagedBy:        managedByLabel,
				labelComponent:        "gateway-verification",
				"konveyor.io/gateway": gateway.Name,
			},
		},
		Spec: batchv1.JobSpec{
			// One shot; a bad endpoint or credential will not fix itself on retry.
			BackoffLimit: ptr.To(int32(0)),
			// A pod that is never admitted (restricted Pod Security, an
			// unpullable image) leaves the Job neither Complete nor Failed. The
			// deadline turns that into a Failed Job so the Gateway does not sit
			// on Verifying forever.
			ActiveDeadlineSeconds: ptr.To(verificationDeadline),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					// Admissible under the restricted Pod Security Standards,
					// matching the enumeration Job and the manager's own pod.
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{
						{
							Name:            verifyContainerName,
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							// The probe only reads; it writes solely to
							// /dev/null and /dev/termination-log, both on the
							// writable /dev mount, so a read-only root is fine.
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								ReadOnlyRootFilesystem:   ptr.To(true),
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							},
							Command: []string{
								"sh", "-c",
								// Use env vars to avoid shell injection.
								gatewayVerificationCurlCommand(gateway.Spec.Provider, includeAuth),
							},
							Env: env,
						},
					},
				},
			},
		},
	}

	// Set owner reference so the Job is cleaned up with the gateway.
	if err := ctrl.SetControllerReference(gateway, job, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference: %w", err)
	}

	return r.Create(ctx, job)
}

// patchStatus patches the Gateway status and returns a reconcile result.
func (r *GatewayReconciler) patchStatus(
	ctx context.Context,
	gateway *konveyoriov1alpha1.Gateway,
	original *konveyoriov1alpha1.Gateway,
) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, gateway, client.MergeFrom(original)); err != nil {
		log.FromContext(ctx).Error(err, "Failed to patch Gateway status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// isJobComplete returns true if the Job has a Complete or Failed condition.
func isJobComplete(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) &&
			c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// isJobSucceeded returns true if the Job has a Complete condition.
func isJobSucceeded(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// isTerminalReadyReason reports whether a Ready-condition reason represents a
// settled verification outcome. The reconciler skips re-verifying a Gateway
// whose current generation already reached one of these; a spec change (new
// generation) re-triggers verification.
func isTerminalReadyReason(reason string) bool {
	switch reason {
	case reasonConnectionVerified, reasonConnectionFailed, reasonAuthenticationFailed, reasonEndpointUnreachable:
		return true
	}
	return false
}

// verificationDiagnostic returns the termination message written by the
// verification Job's pod (see gatewayVerificationCurlCommand), or "" if no
// terminated pod/message is found. It prefers the "verify" container and falls
// back to any other terminated container carrying a message. Pods are pinned to
// the Job by controller UID so a stray pod carrying the same job-name label
// cannot win. Reads are uncached (see the apiReader field).
func (r *GatewayReconciler) verificationDiagnostic(ctx context.Context, namespace, jobName string, jobUID types.UID) string {
	var pods corev1.PodList
	if err := r.apiReader.List(ctx, &pods,
		client.InNamespace(namespace),
		client.MatchingLabels{
			batchv1.JobNameLabel:       jobName,
			batchv1.ControllerUidLabel: string(jobUID),
		},
	); err != nil {
		// An RBAC gap or API error looks the same as "the probe wrote
		// nothing" to the caller (both yield the generic terminal message), so
		// surface it here rather than letting it vanish.
		log.FromContext(ctx).Error(err, "Failed to list verification pods for diagnostic", "job", jobName)
		return ""
	}

	var fallback string
	for i := range pods.Items {
		for _, cs := range pods.Items[i].Status.ContainerStatuses {
			if cs.State.Terminated == nil {
				continue
			}
			msg := strings.TrimSpace(cs.State.Terminated.Message)
			if msg == "" {
				continue
			}
			if cs.Name == verifyContainerName {
				return msg
			}
			fallback = msg
		}
	}
	return fallback
}

// gatewayVerifyReason maps a probe diagnostic line to a Ready-condition reason
// and a human-readable message. succeeded is the Job's own Complete/Failed
// outcome and takes precedence for the status; the diagnostic only enriches the
// message and refines the failure reason. When diag is empty or unrecognized it
// falls back to the historical generic messages.
func gatewayVerifyReason(diag, endpoint, jobName string, succeeded bool) (reason, message string) {
	fields := strings.Fields(diag)
	code := diagValue(fields, "code")

	if succeeded {
		// Only quote the code when the probe reported success ("ok code=<n>");
		// a mismatched or empty diagnostic must not produce "reachable (HTTP 401)".
		if len(fields) > 0 && fields[0] == "ok" && code != "" {
			return reasonConnectionVerified, fmt.Sprintf("Endpoint %s is reachable (HTTP %s)", endpoint, code)
		}
		return reasonConnectionVerified, fmt.Sprintf("Endpoint %s is reachable", endpoint)
	}

	if len(fields) > 0 {
		switch fields[0] {
		case "auth":
			if code != "" {
				return reasonAuthenticationFailed, fmt.Sprintf(
					"Endpoint %s/v1/models returned HTTP %s - check the credential secret's API key",
					endpoint, code)
			}
		case "http":
			if code != "" {
				return reasonConnectionFailed, fmt.Sprintf(
					"Endpoint %s/v1/models returned HTTP %s", endpoint, code)
			}
		case "unreachable":
			return reasonEndpointUnreachable, fmt.Sprintf(
				"Endpoint %s is unreachable (%s)", endpoint, curlErrorPhrase(diagValue(fields, "rc")))
		}
	}

	return reasonConnectionFailed, fmt.Sprintf("Verification Job %q failed", jobName)
}

// diagValue extracts the value of a "key=value" token from a diagnostic line's
// fields, or "" if absent.
func diagValue(fields []string, key string) string {
	prefix := key + "="
	for _, f := range fields {
		if v, ok := strings.CutPrefix(f, prefix); ok {
			return v
		}
	}
	return ""
}

// curlErrorPhrase turns a curl exit code into a human-readable cause.
func curlErrorPhrase(rc string) string {
	switch rc {
	case "6":
		return "curl exit 6: could not resolve host"
	case "7":
		return "curl exit 7: connection refused"
	case "28":
		return "curl exit 28: timed out"
	case "":
		return "connection error"
	default:
		return "curl exit " + rc + ": transport error"
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Uncached reader for verification pods; see the apiReader field.
	r.apiReader = mgr.GetAPIReader()
	return ctrl.NewControllerManagedBy(mgr).
		For(&konveyoriov1alpha1.Gateway{}).
		Owns(&batchv1.Job{}).
		Named("gateway").
		Complete(r)
}
