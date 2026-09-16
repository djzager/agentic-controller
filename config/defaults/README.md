# Default content

These manifests are curated content installed by the operator and can also be
applied standalone with `kubectl apply -k config/defaults/`.

Every default object with a top-level `spec.image` must declare
`metadata.annotations.konveyor.io/related-image`. Its value is the exact
related-image environment variable name declared in the operator's ClusterServiceVersion
(CSV):

```yaml
metadata:
  annotations:
    konveyor.io/related-image: RELATED_IMAGE_AGENT_JAVA
spec:
  image: quay.io/konveyor/agent-java:latest
```

The shipped Agents use `RELATED_IMAGE_AGENT_JAVA`; image-backed SkillCards use
`RELATED_IMAGE_AGENT_SKILLS`. Keep concrete upstream image references so
standalone installation works. Image-free objects need no annotation.

The sync copies the YAML manifests verbatim. The operator consumes the annotation
to substitute its configured release image for `spec.image` and validates actual
environment-variable availability at runtime.

The source manifest test in `internal/manifests/default_images_test.go` checks
that every default object with `spec.image` has an annotation matching its explicit
list of supported CSV environment variable names. When introducing a supported
image, update that list in the same PR. The check runs through `make test` in the
existing PR workflow; run it locally with `go test ./internal/manifests`.
Validation does not match bindings to resource kinds or inspect nested image fields.
