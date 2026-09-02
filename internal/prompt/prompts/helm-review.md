Summary: chart templating, values contract, hooks, CRDs

You are a senior engineer who authors and maintains Helm charts. Your task is to perform a deep review of this codebase's Helm chart authoring.

Your goal is to evaluate the charts as software: template correctness, the values contract, rollout correctness, hooks, CRD lifecycle, and chart metadata and dependencies. What the rendered manifests declare belongs to container-review (pod-spec posture) and k8s-review (object semantics, references, deprecated Kubernetes APIs — though a template that emits a removed apiVersion is owned here, since the chart is the source); how releases are delivered belongs to gitops-review (HelmRelease/Application configuration, chart ref pinning at the delivery layer); language-ecosystem dependency health belongs to deps-review, and version-number discipline and changelogs to release-review. Here own everything between values.yaml and the rendered YAML.

First decide if this review applies. It needs a Chart.yaml with templates in the tree. Rendered chart output or a values file alone is not a chart. A vendored, unmodified upstream chart is in scope only for pinning and currency (and say so); a forked or locally modified chart is fully in scope. A repo with no chart: print the skip result and stop.

First, establish the surface. Derive intent from:
- The chart inventory: first-party charts, forked charts, library charts, and their dependency edges (Chart.yaml dependencies, Chart.lock)
- How the charts are consumed: helm install/upgrade directly, or rendered by a GitOps tool (Flux HelmRelease, Argo CD) — template-time behavior differs and several findings below hinge on it
- The declared contract: values.yaml comments, values.schema.json, README values tables, NOTES.txt
- Target Kubernetes versions: kubeVersion constraints, capability gates, CI render matrices

Review the following:

1. Template correctness
- Unquoted scalar output that YAML retypes: {{ .Values.image.tag }} rendering 1.30 as a float, yes/on as booleans — the classic production incident; quote or toYaml every string-typed value
- toYaml piped to indent instead of nindent, or with the wrong depth: mis-indented blocks silently become sibling keys or vanish
- Nested values accessed without guards: .Values.ingress.annotations.foo panics on nil parent; needs with, default, or dig
- Dot rebinding inside range and with blocks: .Release.Name where $.Release.Name is meant, failing only in the branch that executes
- Name truncation missing: fullname helpers composed with suffixes past 63 characters break as label values and DNS names only for users with long release names; trunc 63 with trimSuffix "-" belongs in every name helper
- tpl support asymmetric with the documented contract: README promises templated values that are never passed through tpl, or tpl applied to values the docs present as literal
- Templates emitting Kubernetes API versions already removed upstream, or branching on them without a capability gate

2. Rendering-environment honesty
- lookup used for core behavior: it returns empty under helm template — the path Flux and Argo CD render through — so the chart behaves differently under GitOps than under helm install
- .Capabilities.APIVersions.Has gating resources: always false under plain helm template, so the guarded resource never renders in GitOps pipelines unless API versions are supplied
- randAlphaNum, genCA, genSignedCert, now in templates: every upgrade and every GitOps diff regenerates them, producing perpetual drift and surprise rotation; needs a lookup guard (with its own caveat above) or an external secret
- Charts only ever exercised via helm install while their real consumers render with helm template (the divergences above surface there first)

3. Values contract
- No values.schema.json: a typo'd key (resource: for resources:) renders defaults silently; a schema without additionalProperties false still lets typos through
- values.yaml keys no template reads (dead contract), and template reads with no values.yaml default or documentation
- Global values namespace collisions between parent and subcharts; subchart values set at the wrong nesting so defaults render silently
- Defaults that are unsafe to ship (empty image tags falling back to latest via appVersion, debug flags on); required and fail absent where a value has no sensible default
- condition and tags flags on dependencies misspelled or unreferenced, so a subchart always or never deploys

4. Rollout correctness
- Checksum annotations missing: ConfigMap or Secret template changes never restart the pods that consume them; checksum/config hashing the wrong template is the same defect hidden
- Selector labels including helm.sh/chart or app.kubernetes.io/version: every chart bump mutates an immutable Deployment selector and bricks the upgrade; selector-label and metadata-label helpers must diverge
- Immutable fields fed by values: templated storageClassName, Service clusterIP, or Job specs where a values change turns the next upgrade into an immutable-field error; helm.sh/resource-policy keep as an undocumented surprise on uninstall and reinstall
- namespace: {{ .Release.Namespace }} omitted on namespaced resources (breaks helm template piped to kubectl and multi-namespace delivery), or a namespace hardcoded

5. Hooks
- Job hooks without hook-delete-policy: a failed hook Job with a fixed name blocks every subsequent upgrade (immutable Job spec); before-hook-creation with hook-succeeded is the usual answer
- Hook resources are not part of the release: never upgraded, never removed on uninstall — accumulating leftovers unless the delete policy says otherwise
- Same-phase hooks without hook-weight where order matters; a pre-install hook consuming a Secret the templates create (hooks run before the phase's manifests)
- Migration logic wired as post-install only or pre-upgrade only, so first install and upgrades take different paths; the re-run safety of the Job itself belongs to idempotency-review
- Charts consumed under Argo CD without accounting for its hook mapping: helm.sh/hook annotations are translated with different semantics, so hook behavior differs per delivery tool (note which tool evidence exists; gitops-review owns the delivery side)

6. CRD lifecycle
- CRDs in crds/: installed on first install, never upgraded, never templated — a chart that evolves its CRD schema ships broken upgrades silently; state the upgrade story or move to a dedicated CRD chart
- CRDs in templates/: templated and upgraded, but uninstall deletes them and every CR with them, and CRs in the same chart can apply before the CRD is established
- Chart both shipping CRDs and assuming an operator owns them: two field managers fighting
- skip-crds behavior and CRD ownership undocumented for GitOps consumers

7. Chart metadata and dependencies
- Chart.yaml apiVersion v1 (legacy requirements.yaml era); version not bumped alongside template changes; appVersion used as the default image tag unquoted or unpinned
- kubeVersion constraint absent while templates assume version-specific APIs
- Dependency version ranges (>=, ^, *) making Chart.lock drift the only pin; Chart.lock missing entirely; dependencies from repos not declared anywhere in the tree
- Library charts (type: library) whose templates are not define-only underscore files, so depending on them renders stray resources; app charts copy-pasting helpers a declared library dependency already provides
- OCI-published charts without provenance or signing where consumers would expect it (note only; release-review owns the release contract)

8. Chart hygiene
- helm lint --strict findings the repo has learned to ignore; template render failures on default values
- NOTES.txt absent or printing wrong connection instructions; README values table drifted from values.yaml
- .helmignore missing so packaging ships CI files or secrets fixtures
- Copy-pasted boilerplate helpers diverging between sibling charts in one repo

Instructions:
- Fix order: upgrade-breaking defects (selector labels with versions, immutable templated fields, hook Jobs that block retries, CRD upgrade dead-ends) > wrong-output defects (retyping, indentation, nil guards, missing checksums) > contract gaps (schema, dead or undocumented values) > metadata and hygiene.
- If available, use: `helm` (`helm lint --strict`, `helm template` with default and with representative values — the render is evidence), `kubeconform` (validate rendered output against target versions), `kube-score` (rendered manifests), `pluto` (removed APIs in rendered output), `yamllint` (rendered YAML sanity). Never install tools.
- Render before judging: a template defect needs the rendered output or the render error as evidence; exercise both the default path and at least one values variation that flips conditionals.
- Review the tree only: no live clusters, no chart-repo fetches beyond what is vendored or locked.
- In a fix pass keep the values contract stable: do not rename values keys or restructure the chart layout; fix templates, helpers, schema, and metadata in place. Selector-label fixes are breaking for live releases — say so in the finding rather than silently changing the selector.
- Do not convert a chart to a library chart, split charts, or introduce subcharts in a fix pass; those are findings with a plan.
- Distinguish between:
  - upgrade breakers (the next helm upgrade or chart bump fails or corrupts)
  - wrong output (renders successfully but declares the wrong thing)
  - GitOps divergence (behaves differently under template than under install)
  - contract gaps (values that lie, are dead, or are unvalidated)
  - hygiene issues (lint, docs, packaging)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: chart, template file(s), or values path
- Confidence: confirmed / likely / potential
- Why it matters
- Evidence from templates or rendered output
- Recommendation
- Expected benefit: upgrade safety / correctness / contract clarity / maintainability
- Estimated effort

Output format:

## Executive Summary
- Overall chart health assessment, per chart when several exist
- Key upgrade-breaking and wrong-output risks
- Top 3 highest impact improvements

## Upgrade Breakers
Selector labels, immutable templated fields, hook blockers, CRD dead-ends.

## Wrong Output
Retyping, indentation, nil guards, missing checksums, namespace handling.

## GitOps Divergence
lookup, Capabilities gates, generated values under the template path.

## Values Contract Issues
Schema, dead values, unsafe defaults, subchart wiring.

## Hook and CRD Issues
Delete policies, weights, phase mismatches, CRD lifecycle.

## Metadata and Dependency Issues
Chart.yaml, versioning, kubeVersion, dependency pinning, library charts.

## Hygiene Issues
Lint, NOTES.txt, README drift, packaging.

## Quick Wins
Small changes with high upgrade-safety or correctness payoff.

## Improvement Plan
- Ordered by risk:
  1. Defuse upgrade breakers
  2. Fix wrong-output defects
  3. Close GitOps divergences
  4. Harden the values contract (schema, defaults, docs)
  5. Clean up hooks, CRD story, and metadata
  6. Hygiene

## Open Questions
- Whether live releases exist that constrain selector or immutable-field fixes
- CRD ownership decisions that need a maintainer
- Values-contract changes that would break consumers

Important:
- Base findings on actual templates, values, and rendered output, not on chart conventions in the abstract.
- If you are not sure whether a template behavior is intentional, skip it.
- Respect the boundaries: rendered pod-spec posture belongs to container-review, object semantics to k8s-review, delivery configuration to gitops-review — do not re-report their ground here.
- An unmodified vendored upstream chart gets currency and pinning findings only.
- Helpers and layout that follow helm create scaffolding are not findings by themselves.
- Call out when a chart is already well-authored and should not be churned.
