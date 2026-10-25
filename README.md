# YuHaul

**YuHaul is a personal YouTube subscription discovery, filtering, routing, and analytics system.**

It periodically inspects content published by channels you subscribe to, evaluates discovered videos against configurable routes, and sends qualifying videos to the appropriate playlists.

YuHaul was originally built for music discovery: subscribe to channels that publish music, let YuHaul collect their uploads, reject things you do not want, deduplicate songs that appear across multiple channels, and put the remaining discoveries into playlists for later listening.

The routing model is intentionally more general than music, however. A route can target any kind of subscribed content.

## North Star

> **Find more of what you'll actually keep.**

YuHaul should maximize useful discoveries while minimizing noise, wasted attention, wasted API quota, and unexplained decisions.

It automates repetitive discovery work, but the user remains in control of subscriptions, routing policy, thresholds, and other meaningful decisions.

---

## How it works

At a high level:

```text
YouTube account
      │
      ▼
Subscriptions
      │
      ▼
Discovered videos
      │
      ▼
Ordered routes
      │
      ├── Filters / conditions
      │
      ├── Duplicate detection
      │
      └── Actions
      │
      ▼
Playlists
```

Every routing decision is recorded so YuHaul can explain what happened and produce meaningful historical statistics.

---

## Routes

YuHaul evaluates discovered videos against user-defined routes.

A route contains conditions and an action, such as adding a matching video to a playlist.

Examples of filtering conditions include:

* ignored channels
* ignored video IDs
* ignored titles
* ignored words or phrases
* minimum video duration
* maximum video duration
* exact duplicate detection
* fuzzy duplicate detection against previously seen titles

Routes are ordered.

A video may match multiple routes and therefore be added to multiple playlists.

A route can also be marked **final**. When a video successfully matches a final route, no later routes are evaluated for that video.

This makes it possible to create flows such as:

```text
Fireship videos
    ↓
Programming playlist
    ↓
FINAL

Everything else
    ↓
Music filters
    ↓
TBL playlist
```

---

## Duplicate detection

Music discovery produces a particular problem: several channels may upload effectively the same song using slightly different titles.

For example:

```text
Artist - Track
Artist - Track (Official Audio)
ARTIST – TRACK
Artist - Track [Extended Mix]
```

YuHaul therefore maintains a durable historical title corpus and supports fuzzy title comparison in addition to exact matching.

Duplicate decisions retain their provenance.

If several subscribed channels publish substantially the same track, YuHaul can retain information about all of those sources even if only the first copy is routed.

This allows statistics to distinguish between:

* **discovery credit** — which channel introduced the content first
* **relevance credit** — which other channels also published content that proved useful

---

## Scheduled processing

YuHaul is intentionally not a live-monitoring system.

It runs periodically according to a user-defined schedule.

Each run discovers new content and processes all configured routes.

Work that cannot be completed because API quota is exhausted is deferred and processed during a later run.

Temporary operational limitations must not cause eligible content to disappear.

---

## Quota awareness

YouTube API capacity is a finite resource.

YuHaul treats quota as an operational budget rather than simply waiting for requests to fail.

Where possible, it tracks:

* quota consumed
* quota remaining
* quota usage per run
* quota usage by operation type
* expensive operations
* deferred work caused by insufficient quota

Routing should perform cheap and locally decidable work before expensive remote operations.

YuHaul should reuse known metadata, cache durable information, batch compatible operations where possible, and avoid performing external mutations until a video has satisfied its configured filters.

---

## Processing and error handling

YuHaul distinguishes between different processing outcomes.

A video may be:

* pending
* evaluated
* accepted
* rejected
* deferred
* successfully processed
* failed

**Not processed is never equivalent to rejected.**

Quota-limited work is placed into a deferred queue.

Unexpected failures are placed into an error queue where they can be inspected, retried, or deliberately removed by the user.

Completed actions must be idempotent so scheduled runs do not repeatedly add the same video to the same playlist.

---

## Explainability

YuHaul should always be able to answer:

> Why was this video added?

and:

> Why wasn't this video added?

For each video and route, decision evidence should make it possible to inspect:

* whether the route was evaluated
* whether it matched
* which condition accepted or rejected it
* duplicate similarity information
* the historical title it matched against
* whether a final route stopped further processing
* which action was attempted
* whether that action succeeded, failed, or was deferred

Logs and statistics should be derived from durable decision history rather than being the only surviving record.

---

## Analytics

Analytics are a core part of YuHaul.

YuHaul should measure both what its automation does and whether its discoveries actually prove useful.

Statistics should be available by:

* run
* route
* playlist
* source/channel
* time period
* lifetime

Useful measurements include:

* videos discovered
* videos evaluated
* videos accepted
* videos rejected
* rejection reasons
* videos deferred
* errors
* playlist additions
* quota expenditure
* source yield
* source usefulness

### Source usefulness

A channel that generates many candidate videos is not necessarily valuable.

YuHaul therefore distinguishes between:

**Yield**

How often content from a channel survives automatic routing.

and:

**Usefulness**

How often routed content later produces evidence that the user actually wanted it.

One useful signal is whether a routed video is subsequently added to one of the user's own listening or rotation playlists.

This makes questions such as these possible:

> Which channels consistently give me music I keep?

> Which channels create lots of candidates but almost nothing useful?

> Has this channel become less useful over the last year?

> Which subscriptions generate the most useful discoveries per API quota spent?

---

## Historical trends

Lifetime totals can hide important changes.

YuHaul preserves historical data so metrics can be visualized over time.

Graphs should make it possible to distinguish:

* temporary dips
* seasonal patterns
* long-term deterioration
* improving sources
* inactive sources
* routing changes
* changes in discovery volume
* changes in usefulness

Historical statistics should also retain enough configuration provenance to distinguish a change in source quality from a change in routing rules.

---

## Dry-run and backtesting

YuHaul should make routing changes testable before they affect the user's YouTube account.

### Dry-run

Evaluate current or proposed routing rules without performing playlist mutations.

### Historical backtesting

Apply proposed route configurations against previously observed content.

Examples:

> What would have happened if duplicate similarity were 95% instead of 97%?

> Would adding this ignored word have rejected songs I later kept?

> What would I have lost if this channel had been ignored for the last year?

> How much API quota would this rule have saved?

Historical simulations must remain clearly separate from actual routing history.

---

## Recommendations

YuHaul may use its statistics to provide evidence-backed recommendations.

For example:

> This channel has produced 183 routed tracks during the last year and none were subsequently retained.

YuHaul may advise that the subscription is worth reviewing.

It must **not automatically unsubscribe from channels or autonomously modify routing policy**.

Likewise, future versions may suggest channels that appear likely to be useful, but subscribing remains a user decision.

---

## Administration

YuHaul is administered through a responsive web interface designed to work properly on both desktop and mobile devices.

The interface is expected to expose areas such as:

* account status
* routes
* filters
* route ordering
* schedules
* playlist destinations
* ignore lists
* run history
* decision history
* deferred work
* errors
* quota usage
* channel analytics
* statistics
* graphs
* dry-runs
* historical backtests

---

## Principles

YuHaul follows a few fundamental principles:

### Evidence over assumptions

Keep enough factual history to explain decisions and statistics later.

### Automation without loss of control

Automate repetitive work aggressively, but leave policy decisions with the user.

### Efficient by design

Treat external API quota as a constrained resource.

### Explain every decision

A filtered or routed video should never become an unexplained historical outcome.

### Preserve history

Historical observations are useful not only for duplicate detection, but also for analytics, trend analysis, and backtesting.

### Measure actual value

Routing volume is not success.

Useful discovery is.

---

## Releases and published images

The [release workflow](.github/workflows/release.yml) verifies Go
(build, vet, tests and integration tests), the SPA (tests/build), release
tooling, and actual image startup with fresh data volumes before publishing from trusted pushes to `main`. PRs have read-only
checks and cannot publish. Publication uses the built-in `GITHUB_TOKEN`, not a
registry password or npm token. Nothing deploys automatically.

Use Conventional Commits, preferably via squash merge with a conventional PR
title, for example `fix: retain deferred videos` or `feat(routes): add a filter`.
PR titles are checked on edits as well as new commits. With merge/rebase merging,
the actual constituent commit messages must also follow the convention: a title
check alone cannot enforce those messages. Recognized changes determine the
highest version increment since the last release:

- `fix:` and `perf:` → patch; `feat:` → minor.
- `feat!:` / `fix(api)!:` or a `BREAKING CHANGE:` footer → major.
- Non-breaking `docs:`, `ci:`, `chore:` and other maintenance types do not release.
- Standard reverts cancel their original change when both occur in the analyzed
  history (newest first); an unmatched conventional revert warrants a patch.

There is no fabricated baseline tag. With no previous release and release-worthy
history, semantic-release chooses **v1.0.0**, even if the enabling commit itself is
`ci:`. Old nonconforming commits are ignored, not rewritten. Git tags are release
authority; the SPA's private `0.0.0` package version is unrelated. Release notes
are GitHub release notes, not committed changelog/version-bump files.

A successfully published GitHub release passes its exact SHA/tag to the image
job **in the same run**. A separate release-event workflow would not be triggered
by this token-created release. The image is `linux/amd64`, named
`ghcr.io/<lowercase-owner>/<lowercase-repository>:vX.Y.Z` (currently
`ghcr.io/bateau84/yuhaul:v1.0.0` for the expected first release). There is no
`latest` or moving major/minor alias. OCI labels record source/version/SHA; the
image job summary records the pushed digest. Prefer the digest for exact artifact
identity. Mutable base images mean rebuilding a tag can produce a new digest;
neither reproducible builds nor registry-enforced tag immutability is promised.

### Operator prerequisites and recovery

- Repository administrators must allow Actions and `contents: write` for the
  release job and `packages: write` for the image job. Protect/review `main` where
  supported: publication trusts repository writers, not a configured review gate.
- New GHCR packages default **private**. Before enabling publication, an
  administrator must confirm any existing package is private and grants this
  repository Actions write access. The workflow does not change visibility or
  automatically verify it: the Packages REST API documents classic-PAT
  authentication, so a mandatory REST preflight would undermine the no-extra-token
  path. Do not activate publication over a public package or add broader
  credentials to bypass a denial. First real publication remains unverified.
- Private pulls require operator-provisioned authentication outside this pipeline.
  Update deployment image references/pull configuration yourself; this change
  provisions no cluster credentials and modifies no deployment manifests.
- Release and registry publication are **not atomic**. If the image job fails,
  the Git tag/release remain. Inspect the failure and rerun **only the failed image
  job**, retaining the successful release outputs. Rerunning the entire release
  job may find no new version and skip the image; it is not image recovery.
- If only the tag was created before release failure, inspect and repair the
  partial release explicitly; do not delete/rewrite tags or history automatically.
  Manually created releases do not trigger this pipeline. Active publication runs
  are serialized without cancellation; pending runs may be superseded, and stale
  main runs skip safely rather than releasing a newer, unverified head.

Release tooling is a private root package, independent of `web/`. With Node 24
(at least 24.10), run `npm ci --ignore-scripts` and `npm test`. Tests exercise the
locked analyzer/notes/title parser and a real local semantic-release dry run with
synthetic history; they never publish. The test runner disables process isolation
because semantic-release's stdout masking hook conflicts with Node's child-test
IPC stream. Do not run `npm run release` as a local preview: it is guarded for the
trusted main-push Actions environment and is a publishing command.

Implementation details and boundaries: [ADR-016](docs/architecture/release-automation/decisions/adr-016-semantic-release-image-publication.md).

## Deployment

YuHaul ships as a single container. The container serves the admin SPA and the
REST API on one port and stores all durable state in a SQLite database file
that lives on a persistent volume.

Kubernetes is the supported deployment target. A plain `docker run` equivalent
still works, but the repository now ships Kubernetes manifests instead of a
Docker Compose file.

### Quick start with Kubernetes

1. Build the container image and make it available to your cluster:

   ```bash
   docker build -t yuhaul:latest .
   # Push the image to a registry your cluster can reach, or load it into
   # your local cluster (e.g. `kind load docker-image yuhaul:latest`).
   ```

2. **Traefik v3 controller.** The cluster must run a Traefik v3 ingress
   controller with the Kubernetes Ingress and CRD providers enabled (the CRD
   provider with `safeNaming` disabled — see below), an `IngressClass` named
   `traefik`, and entrypoints `web` (`:80`) and `websecure` (`:443`) exposed.
   The HTTP→HTTPS redirect itself is owned by these manifests: a redirect
   Middleware plus a redirect-only HTTP Ingress whose backend is an
   endpoint-free sink Service, never YuHaul. Point `yuhaul.bateau.cloud` DNS
   at that controller. Prerequisites and checks: [Traefik BasicAuth and
   TLS](#traefik-basicauth-and-tls).

3. Create the OAuth secret from `k8s/secret.example.yaml`:

   ```bash
   cp k8s/secret.example.yaml k8s/secret.yaml
   # Edit k8s/secret.yaml and replace every CHANGEME value.
   kubectl apply -f k8s/secret.yaml
   ```

   The redirect URL must be registered in Google Cloud Console exactly as:

   ```text
   https://yuhaul.bateau.cloud/api/auth/callback
   ```

4. Provision the BasicAuth credential Secret `yuhaul-basic-auth` outside
   source control — never commit it:

   ```bash
   # Choose your own username. htpasswd prompts for the password, so it never
   # appears in shell history or in the process list.
   YUHAUL_USER='<your-username>'
   umask 077
   htpasswd -nB "$YUHAUL_USER" > "$HOME/.yuhaul-htpasswd"   # needs Apache htpasswd with -B (bcrypt)
   kubectl create secret generic yuhaul-basic-auth \
     --namespace default \
     --from-file=users="$HOME/.yuhaul-htpasswd" \
     --dry-run=client -o yaml | kubectl apply -f -
   shred -u "$HOME/.yuhaul-htpasswd"
   ```

   The Secret must contain exactly one key, `users`, holding newline-separated
   bcrypt `username:hash` records; that is the shape Traefik reads. Details
   and rotation: [Provisioning the credential
   secret](#provisioning-the-credential-secret).

5. TLS is issued automatically: the HTTPS Ingress carries
   `cert-manager.io/cluster-issuer: letsencrypt-prod`, so cert-manager's
   ingress-shim creates a Certificate for `yuhaul.bateau.cloud` and writes,
   then renews, Secret `yuhaul-tls`. Before applying the Ingress:

   ```bash
   # cert-manager must be installed (CRDs, webhook, controller), and your own
   # production ClusterIssuer must exist with that name — this repository
   # supplies neither, and never stores an ACME account key or DNS credential.
   kubectl get clusterissuer letsencrypt-prod
   ```

   If your issuer has another name, patch the annotation to match. HTTP-01 is
   the configured path (solver IngressClass `traefik`); it needs public port
   80 reachable by the Let's Encrypt validator. If port 80 is not reachable,
   configure that issuer with a DNS-01 solver and external provider
   credentials instead — the `yuhaul-tls` contract does not change. Do not
   create or edit `yuhaul-tls` by hand while cert-manager owns it, and do not
   expose the Ingress or send credentials until the certificate is issued and
   client-trusted.

6. Apply the remaining manifests (Middlewares and the sink Service before the
   Ingresses):

   ```bash
   kubectl apply -f k8s/pvc.yaml
   kubectl apply -f k8s/deployment.yaml
   kubectl apply -f k8s/service.yaml
   kubectl apply -f k8s/sink.yaml
   kubectl apply -f k8s/middleware.yaml
   kubectl apply -f k8s/ingress.yaml
   ```

7. Verify the boundary before relying on it: unauthenticated HTTPS requests
   must be challenged by Traefik, HTTP requests must be redirected without a
   BasicAuth challenge, and neither may ever reach YuHaul over plain HTTP. See
   the [verification checklist](#verification-before-non-local-exposure).

The SQLite database is stored on the `yuhaul-data` PVC and persists across
pod restarts, rescheduling, and image updates. The Deployment uses the
`Recreate` strategy on purpose: the old Pod must terminate before the new one
starts, so old and new binaries never overlap against the same SQLite file
during the UUID migration rollout (ARS-004). This stop-before-start rollout
accepts brief downtime; no zero-downtime guarantee is made, and no second
process may write the database concurrently.

### Kubernetes manifests

| File | Purpose |
|---|---|
| `k8s/pvc.yaml` | PersistentVolumeClaim for the SQLite database file |
| `k8s/deployment.yaml` | Single-replica Deployment with the YuHaul container; `Recreate` strategy so old/new binaries never overlap against the SQLite PVC (UUID migration rollout, ARS-004) |
| `k8s/service.yaml` | ClusterIP service exposing port 80 to the HTTPS Ingress |
| `k8s/sink.yaml` | Endpoint-free ClusterIP Service `yuhaul-http-sink`; the HTTP redirect Ingress targets it so plaintext can never route to YuHaul. Must never gain a matching workload |
| `k8s/middleware.yaml` | Traefik `Middleware`s: `yuhaul-auth` (BasicAuth from Secret `yuhaul-basic-auth`, HTTPS route only) and `yuhaul-https-redirect` (HTTP→HTTPS `redirectScheme`, HTTP route only) |
| `k8s/ingress.yaml` | Two Ingresses for `yuhaul.bateau.cloud`: HTTP-only (`web`) redirect to the sink with no app backend and router priority `1` (so a cert-manager HTTP-01 challenge outranks it), and HTTPS-only (`websecure`) BasicAuth + TLS via Secret `yuhaul-tls`, the only route to the `yuhaul` Service, annotated `cert-manager.io/cluster-issuer: letsencrypt-prod` and `acme.cert-manager.io/http01-ingress-ingressclassname: traefik` for automatic issuance |
| `k8s/secret.example.yaml` | Template for the OAuth credentials Secret |

### Traefik BasicAuth and TLS

The `yuhaul.bateau.cloud` host is served by **two explicitly separated
Ingresses** (ADR-010, ARS-003):

- **HTTP, entrypoint `web`:** redirect-only. Attaches only the
  `yuhaul-https-redirect` Middleware and targets the endpoint-free
  `yuhaul-http-sink` Service — never `yuhaul`. Plain HTTP therefore cannot be
  dispatched to the application: a working redirect answers with an
  HTTPS redirect (no BasicAuth challenge), and a missing/broken redirect fails
  at the empty sink or has no route. There is no HTTP fallback to the app.
- **HTTPS, entrypoint `websecure`:** the only route to the `yuhaul` Service.
  The `yuhaul-auth` BasicAuth Middleware runs on the whole `/` Prefix route —
  SPA, `/api/`, OAuth callback, and SSE stream alike — before any request
  reaches the container, and TLS terminates with the `yuhaul-tls` Secret.
  The app Ingress explicitly sets
  `traefik.ingress.kubernetes.io/router.tls: "true"`: neither the entrypoint
  name nor `spec.tls` alone activates router TLS.

In ordinary use credentials are entered only after the HTTPS BasicAuth
challenge; the HTTP path never issues one. The application has no inbound
authentication of its own, and the Service stays `ClusterIP`, so the HTTPS
Ingress is the only non-local path to YuHaul.

#### Controller prerequisites

The deployed Traefik controller must provide all of the following; the
manifests cannot create them:

- Traefik **v3** with the `traefik.io/v1alpha1` Middleware CRD installed:
  `kubectl get crd middlewares.traefik.io`
- Kubernetes Ingress provider **and** CRD provider enabled, with RBAC
  permission to `get`/`list`/`watch` `middlewares.traefik.io` and `secrets`
  (Traefik watches Secrets so credential rotation takes effect without a
  restart). The CRD provider must have **`safeNaming` disabled**, because the
  Ingress annotations reference middlewares with the exact hyphenated form
  (`default-yuhaul-auth@kubernetescrd`, `default-yuhaul-https-redirect@kubernetescrd`);
  if your install enables `safeNaming`, adapt the references to the documented
  underscore form before applying.
- An `IngressClass` named `traefik`: `kubectl get ingressclass traefik`
  (matches `spec.ingressClassName` in `k8s/ingress.yaml`).
- Static **`providers.kubernetesIngress.allowEmptyServices: true`**. Without
  it, Traefik omits the endpoint-free sink router and HTTP returns 404 instead
  of redirecting. The CRD provider's similarly named option is not sufficient.
  Review shared-controller impact before enabling it: unrelated empty-service
  routers can remain active, run middleware, affect route precedence, and
  return 503 instead of being omitted. Keep the sink endpoint-free.
- Entrypoints named `web` on port 80 and `websecure` on port 443, and TLS
  pass-through from the upstream load balancer to Traefik (if the load
  balancer terminates TLS first, the `redirectScheme` target would need a
  forwarded-protocol configuration that is not part of this contract):

  ```yaml
  # Traefik static configuration. Presenting the provider block is what enables
  # it; for other install methods, enable the same keys per Traefik's static
  # configuration reference. allowEmptyServices belongs to kubernetesIngress.
  entryPoints:
    web:
      address: ":80"
    websecure:
      address: ":443"
  providers:
    kubernetesIngress:
      allowEmptyServices: true
    kubernetesCRD:
      safeNaming: false
  ```

  An optional controller-wide `web`→`websecure` redirect may be added as
  defense in depth, but it MUST NOT be the sole HTTP-downgrade control: the
  manifest-owned redirect route plus sink is the primary control, so the
  boundary does not depend on a hidden controller setting.

If any of these is missing, keep access on a loopback port-forward (below)
and do not expose the Ingress outside the local host.

#### Automatic certificates (cert-manager)

Certificate issuance and renewal are external ingress/cert-manager
responsibilities. These annotations are deployer wiring; YuHaul contains no
certificate issuance, renewal, ACME storage, or certificate-management code.

`k8s/ingress.yaml` carries two certificate annotations, on the HTTPS Ingress
only:

| Annotation | Value | Meaning |
|---|---|---|
| `cert-manager.io/cluster-issuer` | `letsencrypt-prod` | ingress-shim creates a Certificate from the `spec.tls` block and manages Secret `yuhaul-tls`. The name is a patch point: it must match a `ClusterIssuer` you installed |
| `acme.cert-manager.io/http01-ingress-ingressclassname` | `traefik` | the temporary HTTP-01 solver Ingress uses `spec.ingressClassName: traefik`, so challenges land on the same controller |

The redirect Ingress deliberately carries no cert-manager annotation, and its
`traefik.ingress.kubernetes.io/router.priority: "1"` keeps it below the
challenge route: while a challenge is active, only
`/.well-known/acme-challenge/<token>` reaches the solver; everything else is
still redirected to the sink and never reaches YuHaul.

What the repository does **not** contain, and the deployment owner must
provide: cert-manager itself, a `ClusterIssuer` with that name (cert-manager
creates and holds its ACME account-key Secret), and — for HTTP-01 — public
port 80 reachability from the Let's Encrypt validator. If port 80 is not
reachable, configure the issuer with a DNS-01 solver and external provider
credentials instead; the `yuhaul-tls` Secret contract is unchanged either way.
No account key, DNS credential, certificate, or private key belongs in this
repository.

```bash
kubectl get clusterissuer letsencrypt-prod                 # exists and Ready
kubectl get certificate -n default yuhaul-tls              # Ready
kubectl get secret yuhaul-tls -n default                   # populated kubernetes.io/tls
kubectl get ingress yuhaul-http-redirect -n default \
  -o jsonpath='{.metadata.annotations.traefik\.ingress\.kubernetes\.io/router\.priority}'
echo                                                       # expect: 1
```

Use a staging issuer for repeated issuance tests; do not hammer the
production Let's Encrypt endpoint while testing routing.

#### Provisioning the credential secret

`yuhaul-basic-auth` is provisioned by the operator, outside the application
source tree, and is never written to a file in this repository:

```bash
YUHAUL_USER='<your-username>'
umask 077
htpasswd -nB "$YUHAUL_USER" > "$HOME/.yuhaul-htpasswd"   # prompts; bcrypt output
kubectl create secret generic yuhaul-basic-auth \
  --namespace default \
  --from-file=users="$HOME/.yuhaul-htpasswd" \
  --dry-run=client -o yaml | kubectl apply -f -
shred -u "$HOME/.yuhaul-htpasswd"

# Confirm exactly one key is present (Traefik rejects Opaque Secrets with
# any other key count) — expect a single line: users
kubectl get secret yuhaul-basic-auth --namespace default \
  -o go-template='{{range $k, $v := .data}}{{$k}}{{"\n"}}{{end}}'
```

Requirements:

- bcrypt only (`htpasswd -B`); plaintext or legacy MD5/SHA1 records do not
  satisfy this contract.
- Exactly one key named `users`, whose value is one or more newline-separated
  `username:bcrypt-hash` records.
- `kubectl create secret generic --from-file=users=...` keeps the password off
  the command line and out of shell history; base64 in the API is encoding,
  not encryption — protect cluster Secret access with normal RBAC/encryption.

Rotation: re-run the same commands with a new password. Traefik re-reads the
Secret through its informer; if the old password still works, check that the
controller can watch Secrets and restart the Traefik pod.

#### Fail-closed behavior

The HTTPS Ingress attaches `default-yuhaul-auth@kubernetescrd` to the whole
host route. If the `yuhaul-auth` Middleware or the `yuhaul-basic-auth` Secret
is missing, empty, or malformed, Traefik cannot build that router: the
provider logs `secret 'default/yuhaul-basic-auth' not found`, router building
logs `middleware "default-yuhaul-auth@kubernetescrd" does not exist` (or
`no users found`), and requests receive **404 from Traefik** instead of being
forwarded to YuHaul. This was exercised against Traefik v3.7.16: a router
with a missing middleware and a router with an empty user list both returned
404 with zero requests reaching a live backend, while a control router
without the middleware returned 200.

The HTTP Ingress is fail-closed by construction: its only backend is the
endpoint-free `yuhaul-http-sink` Service. If the `yuhaul-https-redirect`
Middleware is missing or broken, requests get a sink error (no endpoints) or
no matching route — they still cannot reach YuHaul, but that is not a
successful redirect; treat the route as not operationally ready.

Do not "repair" a 404 by removing the middleware annotation, retargeting the
HTTP route at `yuhaul`, or dropping the TLS block — fix the
Secret/Middleware/controller configuration instead.

A **404 is not exclusively an authentication error**. If HTTPS returns 404
but credential-free plaintext on port 443 gets a BasicAuth challenge, check
that the app Ingress has `router.tls: "true"`; this is a transport-routing
defect, not a working HTTPS boundary. If HTTP returns 404, check the Ingress
provider's `allowEmptyServices` setting and controller logs for the omitted
empty-sink route, as well as the redirect Middleware. A fail-closed 404 is
not proof of a ready redirect or protected HTTPS route.

#### Verification before non-local exposure

Run these against the real cluster (ARS-003 §Verification):

```bash
# 1. Controller, CRD, IngressClass, and both Middlewares exist
kubectl get crd middlewares.traefik.io
kubectl get ingressclass traefik
kubectl get middleware yuhaul-auth yuhaul-https-redirect --namespace default

# 2. Topology: the sink has no endpoints and no workload may match it;
#    the HTTP Ingress must never target the yuhaul Service
kubectl get endpointslices -n default \
  -l kubernetes.io/service-name=yuhaul-http-sink          # expect no rows
kubectl get pods -n default -l app=yuhaul-http-sink       # expect no rows
kubectl get ingress yuhaul-http-redirect -n default \
  -o jsonpath='{.spec.rules[*].http.paths[*].backend.service.name}' \
  ; echo                                                  # expect: yuhaul-http-sink
kubectl get ingress yuhaul-http-redirect yuhaul -n default \
  -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.annotations.traefik\.ingress\.kubernetes\.io/router\.entrypoints}{"\n"}{end}'
# expect: yuhaul-http-redirect: web  /  yuhaul: websecure
kubectl get ingress yuhaul -n default \
  -o jsonpath='{.metadata.annotations.traefik\.ingress\.kubernetes\.io/router\.tls}{"\n"}'
# expect: true; also inspect the deployed controller static configuration for
# providers.kubernetesIngress.allowEmptyServices: true (not kubernetesCRD).

# 3. Every representative path is challenged before the backend:
#    SPA, API read, API mutation, the corpus upload (ARS-002's unauthenticated
#    surface), OAuth callback (do not complete it), SSE
for p in / /api/routes /api/corpus/import /api/auth/callback /api/runs/example-id/stream; do
  printf '%-40s ' "$p"
  curl -sk -o /dev/null -w '%{http_code}\n' "https://yuhaul.bateau.cloud$p"
done
printf '%-40s ' "POST /api/routes"
curl -sk -o /dev/null -w '%{http_code}\n' -X POST -H 'Content-Type: application/json' \
  -d '{}' https://yuhaul.bateau.cloud/api/routes
printf '%-40s ' "POST /api/corpus/import/preview"
curl -sk -o /dev/null -w '%{http_code}\n' -X POST -F 'file=@/dev/null' \
  https://yuhaul.bateau.cloud/api/corpus/import/preview
# expect 401 for each; 404 can mean missing TLS activation, a missing router,
# or a Secret/Middleware failure (fail-closed, not ready); investigate logs.
# 2xx/3xx without credentials is a failure

# 4. Valid credentials reach YuHaul, invalid ones do not
#    (set YUHAUL_PASSWORD in this shell first — keep it out of files/history)
curl -sk -o /dev/null -w '%{http_code}\n' -u "$YUHAUL_USER:$YUHAUL_PASSWORD" \
  https://yuhaul.bateau.cloud/api/overview          # 200
curl -sk -o /dev/null -w '%{http_code}\n' -u "$YUHAUL_USER:wrong" \
  https://yuhaul.bateau.cloud/api/overview          # 401

# 5. HTTP is redirect-only with no BasicAuth exchange, and TLS is client-trusted.
#    Never send real credentials over HTTP in these tests.
for p in / /api/routes /api/auth/callback /api/runs/example-id/stream; do
  printf '%-40s ' "$p"
  curl -sI -o /dev/null -w '%{http_code}\n' "http://yuhaul.bateau.cloud$p"   # 301 to https://
done
curl -sI http://yuhaul.bateau.cloud/ | head -1                       # 3xx to https://
curl -sI http://yuhaul.bateau.cloud/ | grep -i www-authenticate      # no output (no challenge over HTTP)
curl -sI https://yuhaul.bateau.cloud/ -o /dev/null -w '%{http_code}\n'   # no -k: chain must be trusted and match the host
curl -svI https://yuhaul.bateau.cloud/ -o /dev/null 2>&1 | grep -E 'subject:|certificate'   # matches yuhaul.bateau.cloud
# Credential-free malformed transport: plaintext directly on the TLS port
curl -sI http://yuhaul.bateau.cloud:443/                 # rejection/no app route
# No BasicAuth challenge and zero backend hits; no particular status required.
# NEVER send credentials in this plaintext probe.

# 6. cert-manager owns `yuhaul-tls`, and the challenge route outranks the
#    redirect sink (priority 1). Use a staging issuer for repeated tests.
kubectl get ingress yuhaul -n default \
  -o jsonpath='{.metadata.annotations.cert-manager\.io/cluster-issuer}{"\n"}'   # letsencrypt-prod
kubectl get ingress yuhaul -n default \
  -o jsonpath='{.metadata.annotations.acme\.cert-manager\.io/http01-ingress-ingressclassname}{"\n"}'  # traefik
kubectl get ingress yuhaul-http-redirect -n default \
  -o jsonpath='{.metadata.annotations.traefik\.ingress\.kubernetes\.io/router\.priority}{"\n"}'       # 1
kubectl get certificate yuhaul-tls -n default            # expect Ready=True
# While a challenge is active, an HTTP request to
# /.well-known/acme-challenge/<token> must return the solver response (never a
# 301 or a sink error) with zero YuHaul backend hits; every other HTTP path
# still gets the redirect.

# 7. UUID-migration rollout: single replica, stop-before-start strategy
kubectl get deployment yuhaul -n default \
  -o jsonpath='{.spec.replicas} {.spec.strategy.type}{"\n"}'         # expect: 1 Recreate

# 8. No credential or key material in the repository
git grep -nE '\$2[aby]\$' -- k8s docs README.md ; echo "exit=$?"   # expect no matches
```

A redirect over HTTP is only meaningful if backend logs confirm zero YuHaul
hits for those requests; in a controlled test, removing the
`yuhaul-https-redirect` Middleware must turn HTTP requests into sink
errors/no-route responses (still zero backend hits), not YuHaul traffic —
restore the Middleware afterwards.

For an isolated provider regression test, use the real Kubernetes Ingress
and CRD providers with these manifests, `allowEmptyServices: true`, no global
redirect, and **no entrypoint-default TLS on `websecure`**. This proves the
app annotation rather than masking its absence. Inspect resolved routers:
the app router is TLS-only on `websecure`; the priority-1 sink router is
non-TLS on `web`. Verify HTTP 301 preserves host/path/query, has no challenge,
and causes zero backend hits; HTTPS without/with invalid credentials gives
401 before dispatch, and valid credentials reach the backend without an
Authorization header. In isolation, detaching only the redirect reference
must give an empty-backend 503; removing the redirect Middleware must reject
the router. Both must cause zero app hits and no challenge. Setting
`allowEmptyServices: false` must omit the HTTP router, never fall back to the
app. Restore all configuration; do not expose an insecure dashboard/API for
inspection. These local checks do not prove cluster readiness or certificate
issuance/renewal (ARS-003 Verification 15–17).

Header removal (`removeHeader: true` in `k8s/middleware.yaml`) is enforced by
the Middleware; to observe it, temporarily deploy a header-echo service (for
example `traefik/whoami`) behind the same Middleware annotation and confirm
the echoed request carries no `Authorization` header, then delete it.

Also confirm there is no second route around the boundary:
`kubectl get ingress --all-namespaces` must show no other Ingress, Gateway,
or LoadBalancer Service pointing at the YuHaul Service.

#### Local operator access (intentional bypass)

```bash
kubectl -n default port-forward service/yuhaul 8080:80
# open http://127.0.0.1:8080/
```

This connects directly to the ClusterIP Service and deliberately bypasses
both BasicAuth and TLS. It is an operator-only path: keep the forward bound
to loopback (the `kubectl` default) and never bind it to a non-loopback
interface for shared or remote access. A missing challenge here is expected
and must not be reported as an ingress-auth failure.

#### Residual caveats

- BasicAuth is one shared credential for the whole single-user admin surface.
  It is not per-user identity or authorization, provides no CSRF or XSS
  protection, and adds no rate limiting; an authenticated caller has full
  administrative authority. A compromised browser or leaked credential
  exposes everything the app can do.
- The redirect is server-side: it cannot protect credentials a client has
  already chosen to send. In ordinary use credentials are entered only after
  the HTTPS challenge, but an explicit client that preemptively sends an
  `Authorization` header on its **initial HTTP request** transmits it in
  cleartext before Traefik can redirect. Such clients must be reconfigured to
  use HTTPS and must not send BasicAuth credentials over HTTP; the server
  guarantees only that HTTP never dispatches to YuHaul and never issues a
  BasicAuth challenge.
- Credentials travel only over HTTPS — hence the mandatory redirect and the
  `yuhaul-tls` Secret, which cert-manager issues and renews while the issuer
  annotation is present (a TLS block without a valid, client-trusted
  certificate is not secure transport). Without a trusted certificate, the
  Ingress must not be exposed outside the local host; there is no plaintext
  fallback.
- Cluster-internal callers (Service/Pod) and loopback port-forward bypass the
  middleware by design. Keep the Service `ClusterIP` and Kubernetes RBAC
  tight.
- Repository manifests encode this contract but cannot prove deployment:
  they do not prove DNS, firewall, or internet reachability of
  `yuhaul.bateau.cloud`, controller/CRD/`safeNaming` conformance, or that the
  assembled request path was verified on a cluster. They also do not decide
  whether BasicAuth-only is an acceptable risk for the intended threat model
  (that is the security owner's open question in ARS-003).

### Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `YUHAUL_DB_PATH` | no | `/data/yuhaul.db` | Path to the SQLite database file |
| `YUHAUL_HTTP_ADDR` | no | `:8080` | HTTP listen address |
| `YUHAUL_WEB_ROOT` | no | `/app/web` | Directory containing the built SPA |
| `YUHAUL_DAILY_QUOTA` | no | `10000` | YouTube Data API daily quota limit |
| `YUHAUL_YOUTUBE_BASE_URL` | no | — | Override the YouTube Data API v3 base URL for Product Acceptance / sandboxing |
| `YUHAUL_OAUTH_CLIENT_ID` | yes | — | Google OAuth2 client ID |
| `YUHAUL_OAUTH_CLIENT_SECRET` | yes | — | Google OAuth2 client secret |
| `YUHAUL_OAUTH_REDIRECT_URL` | yes | — | Absolute OAuth callback URL |
| `YUHAUL_ENCRYPTION_KEY` | yes | — | 32-byte AES-256-GCM key (raw or base64) |

### OAuth callback hostname and client-secret provisioning

The YouTube OAuth2 flow requires a registered redirect URI. The value of
`YUHAUL_OAUTH_REDIRECT_URL` must match one of the **Authorized redirect URIs**
configured in the Google Cloud Console exactly, including scheme, hostname, and
path. The application exposes the callback handler at `/api/auth/callback`.

For the Kubernetes ingress hostname `yuhaul.bateau.cloud`, set:

```text
https://yuhaul.bateau.cloud/api/auth/callback
```

Provision the client secret by editing `k8s/secret.example.yaml` (copied to
`k8s/secret.yaml`) and applying it with `kubectl apply`. The secret is mounted
into the container as environment variables, never logged, and used only to
exchange the authorization code for an access token during the OAuth flow.

### Docker run (without Kubernetes)

```bash
docker run -d \
  --name yuhaul \
  -p 8080:8080 \
  -v yuhaul-data:/data \
  -e YUHAUL_OAUTH_CLIENT_ID \
  -e YUHAUL_OAUTH_CLIENT_SECRET \
  -e YUHAUL_OAUTH_REDIRECT_URL=https://yuhaul.bateau.cloud/api/auth/callback \
  -e YUHAUL_ENCRYPTION_KEY \
  yuhaul:latest
```

The SPA is built into the container image during `docker build`; no external
volume for static assets is required.

The image runs as UID 1000 and owns its `/data` directory, so new Docker named
or anonymous volumes can initialize the default `/data/yuhaul.db` without root.
Existing volumes and bind mounts must already permit UID 1000 to write the
database directory and files (including SQLite WAL/shm files); the entrypoint
does not change mounted-data ownership. Kubernetes uses the existing
`runAsUser`, `runAsGroup`, and `fsGroup: 1000` deployment settings; the storage
driver must support that volume permission contract. Image ownership does not
override a mounted PVC's permissions.

Repeat the release image smoke check locally after building:

```sh
docker build -t yuhaul-smoke:verify .
sh scripts/smoke-image.sh yuhaul-smoke:verify
# For Podman: CONTAINER_RUNTIME=podman sh scripts/smoke-image.sh IMAGE
```

The check runs the unchanged default entrypoint/CMD, verifies UID 1000, database
creation, API/SPA readiness, fresh anonymous/named volumes and database restart,
then removes its containers and volumes. It does not verify a Kubernetes PVC.

---

## Status

YuHaul is currently being rebuilt from an earlier working application.

The initial development focus is establishing the deterministic discovery and routing core, durable processing state, duplicate detection, quota-aware execution, explainability, and the historical evidence required for meaningful analytics.

Implementation architecture and technology choices will be documented separately as the design progresses.

---

## The loop

```text
DISCOVER
   ↓
EVALUATE
   ↓
EXPLAIN
   ↓
ROUTE
   ↓
MEASURE
   ↓
LEARN
   ↓
USER DECIDES
```

**YuHaul — haul in the good stuff.**
