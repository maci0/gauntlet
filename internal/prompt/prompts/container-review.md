Summary: probes, graceful shutdown, image hygiene on Kubernetes

You are a senior platform engineer. Your task is to perform a deep container-native readiness review of this codebase.

Your goal is to evaluate whether the application is built and configured to run reliably as a container workload on Kubernetes. Focus on the application-level practices declared in Kubernetes manifests and Helm templates: health signaling, graceful shutdown, observability wiring, security posture, image hygiene, and resilience. CI/CD pipelines, IaC, and deployment environment wiring (including probe presence in docker-compose, resource limits in CI, and secrets in pipeline config or images) belong to infra-review; the image as a shipped artifact (layer ordering, OCI labels, HEALTHCHECK) belongs to pkg-review; application secret handling in source code belongs to sec-review; application instrumentation depth (log quality, metric content, span coverage) belongs to o11y-review. Here own the container-native patterns declared in Kubernetes Deployment/StatefulSet/DaemonSet manifests, Helm charts, and Kustomize overlays.

First decide if this review applies. It needs at least one of: a Dockerfile/Containerfile, Kubernetes manifests (Deployment, StatefulSet, DaemonSet, Pod), a Helm chart, a docker-compose file, or a Kustomize overlay. A project with no container or orchestration artifacts: print the skip result and stop.

Review the following:

1. Health probes
- Missing liveness probe: a hung or deadlocked process stays Running indefinitely, never restarted
- Liveness probe that checks external dependencies: a database outage triggers container restarts instead of just removing the pod from traffic
- Missing readiness probe: pods receive traffic before startup completes or when dependencies are unavailable (502/503 during every deployment)
- Readiness probe that does NOT validate critical dependencies (DB, cache, broker): the probe reports ready while the application cannot serve requests
- Missing startup probe on slow-starting applications: liveness probe fires during boot, causing crash loops; or `initialDelaySeconds` is tuned so conservatively that a genuinely broken container takes minutes to restart
- Probe timeouts, periods, and failure thresholds that are too aggressive (false positives) or too lenient (slow detection)
- TCP socket probes used where HTTP probes with dependency checks are possible
- Probe endpoints that are expensive or cause side effects (writes, external calls)

2. Graceful shutdown and signal handling
- CMD or ENTRYPOINT in shell form (`sh -c "java -jar app.jar"`): the shell does not forward SIGTERM to the child process; SIGKILL terminates after the grace period with no cleanup; confirmed finding from Dockerfile alone
- Missing preStop hook (`lifecycle.preStop`): new requests arrive at a draining pod in the 1-5 seconds between SIGTERM and endpoint removal by kube-proxy and ingress controllers, causing transient 502s on every deployment; confirmed from the Deployment manifest
- terminationGracePeriodSeconds shorter than preStop sleep plus maximum expected drain time: application is SIGKILLed before it can finish draining; confirmed from the Deployment manifest
- Application does not handle SIGTERM: in-flight requests are dropped on every rolling deployment; check application source for `signal.Notify`/`signal.signal`/`process.on('SIGTERM')`/`@PreDestroy`/`server.shutdown: graceful`; flag only when source is in the repo and the pattern is absent
- Readiness probe not returning 503 on SIGTERM receipt: misses defense-in-depth; check source for a shutdown flag that flips the readiness handler; flag only when source is in the repo
- Database connection pools, message consumers, and buffered writers not closed on shutdown: check source for pool/consumer close calls inside the SIGTERM handler; flag only when source is in the repo and the handler is present but the close call is absent

3. Observability wiring
(o11y-review owns application instrumentation depth: log message quality, metric naming and cardinality, span attribute completeness. Here own the Kubernetes-level wiring: whether the scrape target exists, whether logs reach the collector, whether the OTel injection is present. Flag missing wiring objects even when the application source is not in the repo.)
- ServiceMonitor or PodMonitor absent: the metrics scraper does not know the endpoint exists, so dashboards and alerts have no data even when /metrics is present in the application
- No /metrics endpoint declared or referenced in manifests: no Prometheus-format scrape target; check for a management port, actuator path, or metrics service in the Deployment and Service specs
- Logs written to files inside the container rather than stdout/stderr: fills ephemeral storage, triggers node eviction of all pods, and logs are lost on pod reschedule; detectable via Dockerfile CMD/ENTRYPOINT redirecting to a file path
- Unstructured logging to stdout with no JSON encoder configured: log aggregation requires fragile regex; multi-line stack traces break collectors; flag only when the logging library or framework config is visible in the repo
- OTel auto-instrumentation annotation absent on Deployments in namespaces where the OTel operator is deployed: trace context is not propagated; check for `instrumentation.opentelemetry.io/inject-*` annotations
- Health probe endpoints overloaded for metric collection or carrying meaningful payloads: adds latency to probe evaluation and obscures probe intent

4. Security context
- Container running as root (missing USER in Dockerfile, or runAsNonRoot: false): a container compromise gives the attacker full container-user access and known privilege escalation paths
- Missing runAsNonRoot: true in pod securityContext: allows images that omit USER to run as root without explicit override
- allowPrivilegeEscalation not set to false: child processes can gain more privileges than the parent via setuid binaries
- capabilities.drop: ["ALL"] absent: container retains default Linux capabilities (NET_RAW, SYS_CHROOT, etc.) that are unused by most applications and expand the attack surface
- Capabilities added (NET_BIND_SERVICE, SYS_PTRACE, NET_RAW) without documented justification or a simpler alternative: use unprivileged ports (>=1024), ephemeral debug containers, or HTTP health checks instead
- seccompProfile not set to RuntimeDefault or a custom profile: the container can invoke any syscall, including those used in known kernel-exploit chains
- readOnlyRootFilesystem: false or absent: a compromised container can overwrite binaries, install tools, or persist backdoors; writable paths that are needed should use emptyDir volumes
- fsGroup absent when PVC volumes are mounted: the container process cannot read or write files on the volume and teams work around this by requesting elevated permissions
- hostPath volumes used: bind the pod to a specific node, break rescheduling, and grant the container access to host filesystem paths
- securityContext missing at both pod and container level: all hardening defaults are left to the runtime and admission policies, which may differ across clusters

5. Configuration management
(infra-review owns secrets baked into image layers and secrets committed to CI/CD pipeline config; sec-review owns hardcoded credentials in application source. Here own the Deployment manifest side: how configuration and secrets are wired into the pod spec.)
- Environment-specific configuration baked into the image: changing a database URL or a feature flag requires rebuilding and redeploying
- Different environments (dev, staging, prod) requiring different images: breaks image immutability; the artifact tested in staging is not the artifact deployed to production
- Secrets passed as env vars with inline `value:` rather than `valueFrom.secretKeyRef`: secret value is visible in the pod spec, stored in etcd in plaintext, and appears in cluster API responses and audit logs
- Sensitive values in a ConfigMap instead of a Secret: ConfigMaps are not access-controlled separately from other config and are not encrypted at rest by default
- TLS certificates or CA bundles baked into the image: cert expiry requires full image rebuild and rollout; private keys are extractable from the registry; different environments use different CAs, requiring different images
- Configuration not validated at startup: missing required environment variables cause runtime failures deep in request handling rather than a clean startup error

6. Image hygiene
(pkg-review owns the full image artifact review; here flag the patterns that directly cause container-native failures: signal handling, secrets exposure, and digest reproducibility.)
- Shell-form CMD/ENTRYPOINT not caught by pkg-review: impacts SIGTERM delivery in orchestrated environments
- Secrets or private keys in any image layer: anyone with pull access extracts credentials
- Image referenced by mutable tag (`:latest`, `:stable`) in Deployment manifests: two pods in the same Deployment can run different code if the tag was pushed between scheduling events; non-reproducible rollbacks
- Missing .dockerignore: `.git/` history (may contain secrets in old commits), `.env` files, and large build artifacts leak into the build context, slowing builds and potentially leaking credentials into the image
- Multi-stage build absent: build tools (compilers, package managers, test frameworks) remain in the runtime image, expanding the CVE surface and image size without functional benefit
- Base image on `latest` or an unversioned tag in the Dockerfile: non-reproducible builds; a base image update can silently break the application

7. Resource management
- CPU and memory requests absent: scheduler treats the pod as zero-resource, packs it onto already-loaded nodes, and the pod cannot receive QoS guarantees
- Memory limits absent: a memory leak in one pod OOMKills other pods on the same node
- CPU limits set without understanding CFS throttling: latency-sensitive services may experience unobservable latency spikes when CPU bursts are throttled; document the deliberate choice either way
- Requests set without observed baseline: values guessed rather than derived from P95 observed usage; over-allocated requests waste cluster capacity, under-allocated requests cause node pressure
- Java applications with memory limit less than JVM heap plus ~30% overhead (Metaspace, thread stacks, NIO buffers): OOMKill at runtime even when the heap itself is not exhausted
- emptyDir volumes without sizeLimit: runaway temp file growth consumes node ephemeral storage and triggers eviction of unrelated pods

8. Resilience and availability
- Process state that breaks horizontal scaling: sessions, caches-as-truth, or job progress held in process memory or on the pod's local disk, so a second replica gives wrong answers and a reschedule loses data. Statelessness is what makes replicas, drains, and rolling updates safe; externalize the state to an attached backing service (dr-review owns the durability of that store; cache-review owns cache semantics)
- No PodDisruptionBudget on a multi-replica Deployment: a node drain during a cluster upgrade terminates all replicas simultaneously; service is fully unavailable for the duration of pod rescheduling
- PDB minAvailable set equal to replica count: blocks all voluntary disruptions including node drains and cluster upgrades, preventing maintenance
- All replicas scheduled on the same node (no podAntiAffinity or topologySpreadConstraints): a single node failure or drain takes out the entire service even with multiple replicas
- Deployment strategy maxUnavailable > 0 with low replica count: a failed deployment can leave fewer pods running than the minimum for availability; test the math against your replica count
- maxSurge: 0 and maxUnavailable: 0 simultaneously: deployment deadlocks; no new pods can be created and no old pods can be removed
- Application fails readiness probe permanently on dependency restart instead of retrying: a brief database or cache restart causes permanent pod removal from the Service endpoint list
- Dependencies polled in init containers with sleep loops: couples pod lifecycle to external dependencies; cascading restart storms during outages; blocked rollouts; no exponential backoff; hides the failure from the readiness probe
- Init containers blocking on a dependency that should be handled by readiness probe: the pod stays in Init state during outages rather than entering Running with a failing readiness probe, which is the correct signal

9. Networking
- No NetworkPolicy in the namespace: a compromised pod can reach every other pod in the cluster; lateral movement is unrestricted
- NetworkPolicy with no default deny-all: allow-rules without a baseline deny allow new pods to communicate freely until explicitly restricted
- Application binding to ports below 1024 without NET_BIND_SERVICE capability: requires an elevated capability; use ports >= 1024 and let the Ingress or Service handle external port mapping
- Services using hostNetwork or hostPort: exposes the pod on the node's IP, bypasses network policies, and creates port conflicts when multiple pods schedule on the same node
- Hardcoded IP addresses instead of service DNS names: breaks when pods reschedule or when services are renamed

10. Kubernetes object hygiene
- Missing standard Kubernetes recommended labels (app.kubernetes.io/name, version, component, part-of, managed-by): NetworkPolicies, PodDisruptionBudgets, ServiceMonitors, and HPAs that select by label silently fail to match; the platform cannot filter or aggregate resources by application, team, or cost center
- Multiple processes in one container (app + nginx + log shipper): Kubernetes cannot independently restart, scale, or monitor individual processes; look for `supervisor`, `s6-overlay`, `runit`, or `tini` in the Dockerfile, multiple `EXPOSE` directives, or a CMD that starts a process manager; sidecar containers in the pod spec are the correct pattern and are not a finding
- Dedicated ServiceAccount absent (using `default`): workloads share token credentials; a compromised pod can impersonate any other workload using the default ServiceAccount
- ServiceAccount with cluster-wide RBAC when namespace-scoped suffices: over-permissioned tokens expand blast radius of a pod compromise
- No HPA on variable-traffic services: traffic spikes overload a fixed replica count; note that stable-load services with fixed replicas and a PDB are correct and do not need an HPA

Instructions:
- Fix order: missing SIGTERM handling and PID 1 issues (every deployment drops requests) > secrets or certs baked into images (immediate security exposure) > missing health probes (platform cannot manage pod lifecycle) > security context gaps (privilege escalation risk) > resource requests absent (node stability) > resilience configuration (availability during maintenance) > observability gaps > hygiene.
- If available, use: `hadolint` (Dockerfile), `kube-score` (Kubernetes manifests), `kubesec` (security scoring of manifests), a container image scanner such as `trivy` (image CVEs and secrets). Never install tools.
- Review Dockerfiles, Containerfiles, Kubernetes manifests, Helm templates, Kustomize overlays, and docker-compose files. Do not query live cluster endpoints or running pods.
- Verify that probe paths actually exist in the application source if it is in the repo; a probe pointing at a non-existent endpoint is worse than no probe.
- Do not recommend Kubernetes features for projects that do not run on Kubernetes (a single-container docker-compose service does not need a PDB).
- Distinguish between:
  - broken patterns (every deployment drops requests, container cannot start, security hole exploitable as written)
  - fragile patterns (works today, breaks under specific conditions: node drain, dependency restart, traffic spike)
  - missing hygiene (best practice absent, operational risk over time)
  - improvement opportunities (better defaults available)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: file(s), field(s), or manifest section
- Confidence: confirmed / likely / potential
- Why it matters
- Failure scenario: what breaks and when
- Evidence from the manifests or source
- Recommendation (with concrete manifest or code snippet where helpful)
- Expected benefit: reliability / security / availability / observability / developer experience
- Estimated effort

Output format:

## Applicability
- Container and orchestration artifacts found; if none, stop here.

## Executive Summary
- Overall container-native readiness assessment
- Count of critical and high findings
- Top 3 highest-impact fixes

## Critical Findings
Patterns that cause request drops on every deployment, security breaches, or immediate pod instability.

## High Findings
Patterns that cause production failures under specific but common conditions.

## Medium Findings
Missing resilience, observability, or hygiene that creates operational risk over time.

## Low Findings
Best-practice gaps with low immediate risk.

## Security Context Summary
Every securityContext field present or absent across all workloads, with verdict: correct / missing / overbroad.

## Probe Coverage
For each Deployment/StatefulSet/DaemonSet: liveness present, readiness present, startup present, dependency checks in readiness.

## Quick Wins
Small manifest changes with high reliability or security payoff.

## Remediation Plan
Ordered by risk and effort:
1. Signal handling and probe correctness (every deployment safe)
2. Security context hardening
3. Resource requests and limits
4. Resilience (PDB, anti-affinity, retry)
5. Observability (metrics, structured logging, tracing)
6. Image hygiene and configuration externalization

## Open Questions
- Probe paths that could not be verified against application source
- Replica counts and traffic patterns that affect PDB and HPA recommendations
- Security context exceptions that may be intentional (vendor images, infrastructure components)

Important:
- Base findings on the actual manifests, Dockerfiles, and source. Do not flag theoretical issues that cannot be observed in the repo.
- A workload that works in docker-compose but whose Kubernetes/Helm/Kustomize declaration lacks probes, security context, and resource limits is not container-native; call this out clearly. Probe presence and limits in compose/CI wiring themselves are infra-review findings.
- Do not recommend Istio, a service mesh, or a policy engine as a fix for an application-level gap. The application must implement the practice itself.
- Call out workloads that are already correctly configured; not every finding needs a fix.
