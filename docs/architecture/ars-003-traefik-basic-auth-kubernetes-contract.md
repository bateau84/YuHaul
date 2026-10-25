---
type: specification
title: ARS-003 — Traefik BasicAuth and TLS Kubernetes Contract
description: Exact Kubernetes Secret, Middleware, HTTP redirect and ACME challenge routes, HTTPS BasicAuth route, cert-manager TLS lifecycle, and rollout requirements for YuHaul ingress.
tags: [architecture, specification, yuhaul, kubernetes, traefik, basic-auth, tls]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted

In ordinary use, the operator reaches YuHaul through explicitly separated Traefik routes. Plain HTTP normally matches a redirect route whose backend is a dedicated empty sink; it never selects the YuHaul Service. While cert-manager is answering an active HTTP-01 challenge, only the challenge URI is routed to cert-manager's temporary solver instead of the redirect sink. HTTPS matches the YuHaul route, where BasicAuth runs before any SPA, API, OAuth, or SSE request reaches the application. For example, an unauthenticated HTTPS request to `/api/routes` receives a BasicAuth challenge. An HTTP request to the same application path is redirected when the redirect middleware works; if that middleware is unavailable, it fails at the empty sink or has no route, and still cannot reach YuHaul. A local `kubectl port-forward` is different: it connects directly to the ClusterIP Service and intentionally bypasses both Traefik authentication and Ingress TLS.

## Derived from

- [ADR-010 — Traefik BasicAuth Ingress Boundary](adr-010-traefik-basic-auth-ingress-boundary.md).
- [YuHaul System Architecture](yuhaul-system-architecture.md).
- Security evidence: `docs/ephemeral-reports/hacker/security/2026-10-24.md`, YH-01/YH-02.

## Satisfies

- The user's accepted direction to use Traefik BasicAuth for YuHaul ingress.
- The user's expectation that Let's Encrypt certificates be provisioned and renewed automatically, while preserving the required `spec.tls.secretName: yuhaul-tls` Kubernetes interface.
- The ingress-path portion of YH-01/YH-02: every request that reaches the app through the YuHaul host is authenticated; plaintext HTTP is never dispatched to the app.
- The downgrade failure mode is closed at the manifest boundary: the HTTP route has no YuHaul backend, and the HTTPS route is the only route to that Service.
- No existing BR specifies inbound admin authentication. BR-001 remains the separate YouTube OAuth/account-connection requirement.

## Implements

- [ADR-010 — Traefik BasicAuth Ingress Boundary](adr-010-traefik-basic-auth-ingress-boundary.md).

## Scope

This specification defines the Kubernetes deployment contract for the YuHaul HTTP redirect and HTTPS application Ingresses. It does not add authentication inside the Go application, and does not protect requests that bypass Traefik through a Service, Pod, cluster-internal route, or port-forward. It does not establish actual DNS, firewall, or internet reachability.

## Structural constraints

- Namespace is `default`, matching the current YuHaul Deployment, Service, and Ingress manifests.
- The intended IngressClass is `traefik`; nginx-specific annotations are not part of this contract.
- The HTTPS router for `yuhaul.bateau.cloud` uses path `/` with `pathType: Prefix`, backend Service `yuhaul`, port `80`, and the BasicAuth middleware. Its Ingress MUST explicitly set `traefik.ingress.kubernetes.io/router.tls: "true"`; the name `websecure` and `spec.tls` certificate reference do not activate TLS routing by themselves. Thus the same authentication middleware applies to the SPA, `/api/`, OAuth callback, and SSE paths, only over TLS.
- A distinct HTTP router for the same host and `/` Prefix is attached only to entrypoint `web`; it uses only the HTTPS redirect middleware and its backend is `yuhaul-http-sink`, never `yuhaul`.
- cert-manager ingress-shim owns the Certificate and `kubernetes.io/tls` Secret lifecycle for the HTTPS Ingress. The Secret identity remains `default/yuhaul-tls`; cert-manager, its ClusterIssuer, ACME account key, and any DNS-provider credentials are operator/deployment dependencies and are not installed or stored by this repository.
- The HTTP redirect Ingress has Traefik router priority `1`. A cert-manager HTTP-01 solver Ingress, when used, MUST resolve to priority greater than `1`, so the temporary `/.well-known/acme-challenge/<token>` request reaches only the cert-manager solver and is not redirected to the sink. Traefik's default priority is rule length; the cert-manager challenge rule is more specific than the `/` catch-all, but the explicit sink priority makes the intended ordering stable and reviewable.
- The HTTPS Ingress carries `cert-manager.io/cluster-issuer: letsencrypt-prod` as the conventional default. This is a configurable deployment value, not evidence that a ClusterIssuer with that name exists. For HTTP-01, the Ingress also sets `acme.cert-manager.io/http01-ingress-ingressclassname: traefik`; for a deployment where public port 80 is unavailable, the selected ClusterIssuer MUST instead be configured with a working DNS-01 solver and its provider credentials outside the repository.
- The deployed Traefik v3 controller MUST have its Kubernetes Ingress provider, CRD provider, Middleware CRD, RBAC access to Ingresses, Middleware resources, Secrets, Services and Endpoints/EndpointSlices, and an `IngressClass` named `traefik` available. The concrete installed CRD API version must support the fields below.
- Traefik MUST expose entrypoints named `web` on port 80 and `websecure` on port 443. Both Ingresses select their entrypoint explicitly. The CRD provider's `safeNaming` option MUST be disabled for the exact hyphenated Middleware references specified below; if it is enabled, this contract's references must be changed to the documented underscore form before deployment.
- The controller's static configuration MUST set `providers.kubernetesIngress.allowEmptyServices: true`. This is the **Ingress provider** option, not `providers.kubernetesCRD.allowEmptyServices`. Otherwise the deliberately endpoint-free sink causes the HTTP route to be omitted before its redirect Middleware can run. This provider-wide prerequisite permits empty load balancers; it does not create endpoints or grant a fallback to YuHaul. Keep `web` without entrypoint-default TLS so both the redirect route and HTTP-01 solver remain HTTP. Entrypoint-default TLS on `websecure` is optional, not a substitute for the app Ingress's explicit TLS annotation.
- Traefik MUST terminate client TLS on `websecure`. The upstream load balancer MUST pass TLS through to Traefik rather than terminate it before `websecure`; otherwise RedirectScheme would require an explicitly trusted forwarded-protocol configuration not included in this contract.
- No cluster-wide HTTP-to-HTTPS redirect is required by this contract. An optional global redirect is defense in depth only and MUST NOT be the sole HTTP-downgrade control.
- Any second Ingress, Gateway, or other non-local network route to the YuHaul Service MUST enforce an equivalent authentication boundary and HTTPS. Do not create an unprotected alternate host/path.
- No application-side auth is implied. Liveness/readiness probes and requests through ClusterIP are internal paths and do not traverse this middleware.

## Kubernetes contract

### BasicAuth credential Secret

The deployer provisions a Secret in namespace `default` with this identity and key shape:

| Field | Required value |
|---|---|
| `apiVersion` | `v1` |
| `kind` | `Secret` |
| `metadata.name` | `yuhaul-basic-auth` |
| `metadata.namespace` | `default` |
| `type` | `Opaque` |
| Secret key | `users` |
| Secret value | One or more newline-separated `username:bcrypt-hash` htpasswd records |

The Secret MUST be provisioned outside the application source tree through the operator's secret-management process. The repository MUST NOT contain a live credential, default username/password, or an apply-ready Secret with sample credentials. A bcrypt htpasswd password hash is required; plaintext passwords and weaker legacy hash formats are not accepted by this contract. Secret data is Kubernetes base64-encoded at rest in the API representation; base64 is not encryption. Protect Kubernetes Secret access with the cluster's normal RBAC/encryption controls.

### Traefik Middleware

Provision both Middlewares in namespace `default`:

| Middleware | Required configuration |
|---|---|
| `yuhaul-auth` | `spec.basicAuth.secret: yuhaul-basic-auth`; `realm: YuHaul`; `removeHeader: true` |
| `yuhaul-https-redirect` | `spec.redirectScheme.scheme: https`; `port: "443"`; `permanent: true` |

Normative objects:

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: yuhaul-auth
  namespace: default
spec:
  basicAuth:
    secret: yuhaul-basic-auth
    realm: YuHaul
    removeHeader: true
---
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: yuhaul-https-redirect
  namespace: default
spec:
  redirectScheme:
    scheme: https
    port: "443"
    permanent: true
```

`removeHeader: true` prevents validated BasicAuth credentials from being forwarded to YuHaul. The redirect middleware is never combined with BasicAuth and never routes to YuHaul. Both Middleware resources must resolve in the same namespace. The selected contract targets `traefik.io/v1alpha1`; if the cluster does not serve that API group/schema, the deployer must install/upgrade compatible Traefik v3 CRDs/controller before applying these objects.

### Redirect-only sink Service

The HTTP Ingress backend is deliberately not the YuHaul Service. It MUST use this ClusterIP Service, with no matching Pods or Endpoints/EndpointSlices:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: yuhaul-http-sink
  namespace: default
spec:
  type: ClusterIP
  selector:
    app: yuhaul-http-sink
  ports:
    - name: sink
      port: 80
      targetPort: 8080
```

No Deployment, Pod, or other workload may use label `app: yuhaul-http-sink`; the Service must remain without endpoints. Its only purpose is to make a missing/broken redirect middleware fail as an unavailable sink response rather than accidentally falling through to the YuHaul backend.

The empty Service is usable for middleware-only routing only with `providers.kubernetesIngress.allowEmptyServices: true`. The operator MUST merge this into the existing Traefik static configuration (or use the equivalent CLI flag `--providers.kubernetesingress.allowemptyservices=true`) and apply it through the controller's deployment mechanism. Do not replace unrelated provider configuration. Minimal relevant static configuration, not a repository-owned controller installation:

```yaml
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

This changes empty-service handling for all Ingresses watched by that provider: their routers/middleware can remain active and an empty backend returns 503 rather than having that route omitted. The deployment owner must review shared-controller route precedence and error-handling dependencies before rollout. If an existing deployment constraint forbids this provider-wide behavior, do not silently remove the sink or retarget HTTP at YuHaul; return that constraint to architecture for a scoped redirect-route alternative.

### Ingresses and TLS

Use two separate Ingress resources. The explicit entrypoint annotations prevent either router from inheriting a broader controller default:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: yuhaul-http-redirect
  namespace: default
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: web
    traefik.ingress.kubernetes.io/router.middlewares: default-yuhaul-https-redirect@kubernetescrd
    traefik.ingress.kubernetes.io/router.priority: "1"
spec:
  ingressClassName: traefik
  rules:
    - host: yuhaul.bateau.cloud
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: yuhaul-http-sink
                port:
                  number: 80
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: yuhaul
  namespace: default
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
    traefik.ingress.kubernetes.io/router.middlewares: default-yuhaul-auth@kubernetescrd
    cert-manager.io/cluster-issuer: letsencrypt-prod
    acme.cert-manager.io/http01-ingress-ingressclassname: traefik
spec:
  ingressClassName: traefik
  tls:
    - hosts:
        - yuhaul.bateau.cloud
      secretName: yuhaul-tls
  rules:
    - host: yuhaul.bateau.cloud
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: yuhaul
                port:
                  number: 80
```

cert-manager ingress-shim MUST observe the HTTPS Ingress and create/manage a Certificate in namespace `default` whose `spec.secretName` is exactly `yuhaul-tls` and whose DNS name is `yuhaul.bateau.cloud`. On successful issuance, cert-manager writes the Kubernetes `kubernetes.io/tls` Secret `default/yuhaul-tls` with `tls.crt` and `tls.key`; it renews the certificate and updates that same Secret. Traefik MUST read that Secret and serve the issued certificate after issuance and renewal. The certificate MUST chain to a root trusted by intended clients and be valid for `yuhaul.bateau.cloud`. Do not also provision or mutate this Secret manually while cert-manager owns it. The certificate private key, ACME account key, and any DNS-provider credentials MUST remain outside the repository.

The ClusterIssuer name is configurable at the HTTPS Ingress annotation; `letsencrypt-prod` is the documented conventional default/patch point, not a resource supplied here. The deployment owner MUST provide a compatible cert-manager installation (including ingress-shim and its CRDs/webhook), and a `cert-manager.io/v1` `ClusterIssuer` configured against the Let's Encrypt production ACME directory with an ACME account-key Secret referenced by the issuer. cert-manager creates/manages that account-key Secret; no account key is stored in this repository. For HTTP-01, the solver MUST use the Traefik IngressClass. The per-Ingress `acme.cert-manager.io/http01-ingress-ingressclassname: traefik` annotation selects `spec.ingressClassName: traefik` on the temporary solver Ingress. HTTP-01 is permitted only when Let's Encrypt can reach the challenge through public port 80. If that condition is false, use a matching DNS-01 solver whose DNS API credentials and permissions are provisioned externally; the application-facing `yuhaul-tls` Secret contract remains unchanged.

The corrected `k8s/ingress.yaml` carries explicit app `router.tls: "true"`, `cert-manager.io/cluster-issuer: letsencrypt-prod`, and `acme.cert-manager.io/http01-ingress-ingressclassname: traefik` on the app Ingress, and `traefik.ingress.kubernetes.io/router.priority: "1"` on the HTTP redirect Ingress. README now documents static `providers.kubernetesIngress.allowEmptyServices: true`, its shared-controller implications, and transport/provider-aware 404 diagnosis. No controller install manifest is introduced. Certificate issuance and renewal remain external ingress/cert-manager responsibilities; YuHaul adds no certificate mechanics. No ClusterIssuer object, ACME account key, or DNS credential is present. None of these annotations proves that cert-manager, a `letsencrypt-prod` ClusterIssuer, or a certificate exists in any cluster.

A local corrected-manifest routing probe (`/tmp/opencode/yuhaul-independent/corrected_routing.py`, results in `corrected-results.log`) ran Traefik v3.7.16's real Kubernetes Ingress/CRD providers against the reviewer's simulated Kubernetes API and an instrumented live HTTP backend. With no entrypoint-default TLS or global redirect, HTTPS returned 401 with the YuHaul challenge, HTTP returned 301 preserving host/path/query without a challenge, and credential-free plaintext on the TLS listener had no challenge; all produced zero backend hits. Disabling Ingress-provider `allowEmptyServices` reproduced HTTP 404 while HTTPS remained 401. The API fixture's empty-list watch bookmarks were corrected to use the requested resource kind; no router output was injected. This proves the narrow corrected transport/redirect composition, not live Kubernetes compatibility, valid/invalid credential handling, Authorization removal, failure injection, certificate lifecycle, or all of Verification 15–17.

### Independent provider evidence and correction boundary (2026-10-25)

The independent review exercised real Traefik v3.7.16 Kubernetes Ingress/CRD providers against a local simulated Kubernetes API serving the unmodified manifests. `/tmp/opencode/yuhaul-independent/traefik-probes.log` records HTTPS 404 despite a TLS Secret being present, plaintext HTTP on the `websecure` listener returning BasicAuth 401, and `web` HTTP 404 instead of redirect. `traefik-defaults.log` records the app router without TLS and `Skipping service: no endpoints found` for the sink. The control run in `traefik-control.log` and `traefik-probes.log` used **entrypoint-default TLS** plus `allowEmptyServices: true` and produced HTTPS 401 and HTTP 301. This supports the root causes, but does not prove the chosen **per-Ingress** TLS annotation with the corrected shipped manifests; that exact composition remains a mandatory implementation check. These are local provider results, not live-cluster or cert-manager issuance evidence. Raw DEBUG logs contain test credential records; preserve only a sanitized summary, not their contents, in committed evidence.

The HTTP redirect router MUST NOT reference `yuhaul`, `yuhaul-auth`, or any route that can reach the YuHaul Service. While an HTTP-01 challenge is active, cert-manager's separate temporary solver Ingress is the sole exception to ordinary HTTP redirect routing; it MUST serve only the challenge URI and target only the cert-manager solver. The HTTPS router is the only ingress route allowed to target Service `yuhaul`, and it MUST attach `default-yuhaul-auth@kubernetescrd` to its `/` Prefix route. A TLS block without a valid trusted certificate is not sufficient evidence of secure transport; do not treat application access as ready or send credentials until TLS and the redirect setup are verified. Public HTTP reachability needed only for the ACME challenge does not authorize plaintext access to YuHaul.

## Invariants and lifecycle

1. The repository-owned `web` redirect router for `yuhaul.bateau.cloud` targets only the endpoint-free `yuhaul-http-sink` Service. It MUST NEVER target `yuhaul` or any proxy/service chain that reaches the YuHaul application. During an active HTTP-01 challenge, the only additional HTTP route is cert-manager's temporary challenge Ingress to its solver Service; no HTTP route may reach the YuHaul Service.
2. The `websecure` router is the only Ingress route to Service `yuhaul`; the whole `/` Prefix route attaches `default-yuhaul-auth@kubernetescrd`, explicitly activates router TLS, and declares the certificate using `yuhaul-tls`. Plaintext HTTP on either public listener MUST NOT match an application router or issue a BasicAuth challenge.
3. No HTTP request is authenticated by BasicAuth or dispatched to YuHaul. During an active HTTP-01 challenge only `/.well-known/acme-challenge/<token>` may reach cert-manager's temporary solver; all other requests receive an HTTPS redirect when the redirect Middleware works and, if it is missing or invalid, can receive only a sink error or no matching route. After the solver Ingress is removed, challenge paths receive the ordinary redirect/sink behavior. There is no HTTP fallback to the app.
4. The BasicAuth Secret is provisioned and rotated by the deployer; it is not generated with a repository default.
5. The Ingresses and Middlewares are in namespace `default`, and middleware references exactly match the configured Traefik CRD-provider naming convention.
6. Non-local application use over HTTPS requires a valid certificate trusted by intended clients. During initial HTTP-01 issuance, public port 80 may be reachable only to let the solver answer the challenge; this challenge route is not an application route. Do not send credentials or treat the app as ready over HTTPS until `yuhaul-tls` is valid and Traefik serves it. Missing TLS does not authorize cleartext fallback.
7. BasicAuth is only an Ingress boundary. A direct Service/Pod/port-forward request is not authenticated by this design.
8. Secrets, passwords, bcrypt records, Authorization headers, TLS private keys, ACME account keys, and DNS-provider credentials MUST NOT be emitted in logs or committed artifacts.
9. cert-manager is the sole lifecycle owner of `default/yuhaul-tls` while the annotated Ingress is active; do not also manually create or mutate that Secret.

### UUID migration rollout constraint

ARS-004's route-identity migration and startup backfill assume one application writer. For the release that first ships this migration, `Deployment/yuhaul` MUST retain `replicas: 1` and set:

```yaml
spec:
  replicas: 1
  strategy:
    type: Recreate
```

The old YuHaul Pod must terminate before Kubernetes creates the new-version Pod. The default `RollingUpdate` strategy is not acceptable because it can temporarily overlap old and new binaries against the same SQLite PVC. This deliberate stop-before-start rollout can cause downtime; no zero-downtime migration guarantee is made. No second deployment/job/process may run the migration or write this database concurrently. The `Recreate` strategy is a release constraint for UUID migration; the checked-in `k8s/deployment.yaml` now sets `replicas: 1` and `strategy.type: Recreate`, but that is not a claim about applied cluster state, which remains deployment-owner verification work.

## Failure behavior

- Missing/malformed credentials on HTTPS: Traefik rejects the request with its BasicAuth challenge; the request does not reach YuHaul.
- Invalid/missing redirect Middleware: the HTTP route has only the endpoint-free sink backend, so redirect failure cannot dispatch to YuHaul. The route is not considered operationally ready; do not treat a sink error as a successful redirect.
- Missing/false `providers.kubernetesIngress.allowEmptyServices`: Traefik omits the sink-backed HTTP route and responds with no matching route (404 in the reviewed setup). This remains fail-closed for YuHaul but violates normal redirect availability. Correct the controller prerequisite, never the sink's endpoint-free topology.
- Missing app `router.tls: "true"` with no entrypoint TLS default: the reviewed provider creates a non-TLS app router even when `spec.tls` names a populated Secret. HTTPS can return 404 while plaintext on port 443 triggers BasicAuth and can dispatch authenticated requests. This violates the transport boundary and blocks exposure; certificate issuance alone does not fix it.
- Invalid/missing BasicAuth Middleware, credential Secret, IngressClass, or CRD provider: HTTPS is not considered protected or ready for non-local exposure. Deployment must fail closed operationally; an operator must not bypass the error by publishing the Service directly.
- Missing/invalid `yuhaul-tls`: HTTPS identity is not trusted, so the application is not ready for non-local use; no plaintext fallback is accepted. HTTP-01 may still use the narrow solver route on port 80 for issuance, without routing to YuHaul.
- Missing cert-manager, ClusterIssuer, issuer reference, ACME account state, or solver configuration: the Secret is not automatically provisioned/renewed. Keep the application unavailable for non-local use until a valid, trusted `yuhaul-tls` is present; do not work around the failure by publishing YuHaul over plaintext.
- HTTP-01 challenge route loses priority to the redirect sink: Let's Encrypt cannot validate the challenge and certificate issuance/renewal fails. The challenge request MUST reach the temporary solver and MUST NOT reach the sink or YuHaul. If port 80 is not publicly reachable, select DNS-01 rather than weakening the route boundary.
- A client that explicitly sends an `Authorization` header in its initial HTTP request has already transmitted those credentials in cleartext before Traefik can return a redirect. The HTTP router MUST NOT issue a BasicAuth challenge, but a redirect cannot prevent a misconfigured client from sending credentials preemptively. Clients MUST use HTTPS and MUST NOT send BasicAuth credentials over HTTP.
- Direct ClusterIP access and loopback port-forward: reach the application without BasicAuth; those paths rely on Kubernetes and host access control.
- This contract does not assign HTTP status bodies for Traefik's challenge/error responses beyond requiring rejection before the YuHaul backend.

## Local port-forward behavior

An operator with Kubernetes access may use the existing Service for local access, for example:

```text
kubectl -n default port-forward service/yuhaul 8080:80
```

The intended URL is `http://127.0.0.1:8080/`. The default `kubectl` binding is loopback (`localhost`); the operator MUST NOT bind the forward to a non-loopback interface for shared or remote access. This route bypasses Traefik BasicAuth and TLS by design. It is acceptable only as a local operator path whose host and Kubernetes credentials are trusted.

## Security surface

- **Credential authority:** Traefik reads the htpasswd Secret and authenticates each Ingress request. The Go API does not read or validate this credential.
- **Transport boundary:** HTTP is redirect-only and never routes to YuHaul. HTTPS BasicAuth protects every path on the application router. A server-side redirect cannot protect credentials a client already chose to send in cleartext on the initial HTTP request; no such credential-confidentiality claim is made for HTTP clients.
- **Assets:** BasicAuth credential hashes, TLS private key, Google OAuth callback traffic, route/configuration state, run history, and YouTube actions.
- **Threats addressed:** unauthenticated remote reads and mutations that traverse the configured Ingress.
- **Residual risks:** BasicAuth does not provide CSRF protection, XSS protection, per-user authorization, rate limiting, or defense against a compromised browser/operator credential. In-cluster callers with Service reachability and Kubernetes users able to port-forward bypass the Ingress middleware. No such path is claimed to be protected.
- **Reachability:** repository manifests cannot prove whether the DNS name or cluster ingress is internet-reachable. This specification makes no such claim.

## Verification

Deployment conformance must include an assembled request-path check, not only YAML validation:

1. Confirm the installed Traefik CRD/controller accepts both Middleware resources, Secret references, explicit `web`/`websecure` entrypoints, and `IngressClass/traefik`; confirm `safeNaming` agrees with the references.
2. Confirm `Service/yuhaul-http-sink` has no Endpoints/EndpointSlices and no Pod/workload selects `app: yuhaul-http-sink`.
3. Without credentials, send HTTP requests for `/`, `/api/routes`, an API mutation route, `/api/auth/callback`, and `/api/runs/<id>/stream`; verify each receives an HTTPS redirect, no `WWW-Authenticate` BasicAuth challenge, and zero hits at the YuHaul backend. Do not test by sending real credentials over HTTP.
4. In a controlled test, remove or invalidate the redirect Middleware and repeat the HTTP requests; verify they fail as a sink error/no route and still produce zero YuHaul backend hits. Restore the Middleware afterward.
5. Through HTTPS, verify unauthenticated and invalid-credential requests to representative SPA, API read/mutation, OAuth, and SSE paths are challenged before backend dispatch. Verify valid credentials reach YuHaul, and confirm the Authorization header is removed before backend receipt.
6. Confirm the configured ClusterIssuer name exists and targets the Let's Encrypt production ACME directory. `letsencrypt-prod` is only the documented default; replace it with the deployment's actual name when necessary.
7. For HTTP-01, confirm public port 80 is reachable from the ACME validator, the temporary solver Ingress has `spec.ingressClassName: traefik`, and its resolved router priority is greater than the redirect sink's explicit priority `1`. During a staging or controlled issuance, request the active `/.well-known/acme-challenge/<token>` and verify the solver response, not an HTTPS redirect or sink error; verify zero YuHaul backend hits and no BasicAuth challenge. For DNS-01, verify DNS validation and external provider credentials/configuration instead.
8. Verify issuance creates `default/yuhaul-tls`, cert-manager marks its Certificate Ready, and Traefik serves a trusted certificate valid for `yuhaul.bateau.cloud`. Verify a renewal updates that same Secret and the new certificate is served without manual Secret replacement.
9. For initial HTTP-01 issuance, keep public port 80 limited to redirects and the challenge route; do not send credentials or treat the application as ready over HTTPS until the certificate is trusted. If external port 443 cannot be withheld before issuance, use the deployment's approved exposure controls and do not treat the host as ready until certificate verification passes.
10. Verify Secret files/values, Authorization headers, TLS private keys, ACME account keys, and DNS-provider credentials are absent from repository commits and logs.
11. Verify loopback port-forward reaches the app without an auth challenge, and do not report this bypass as an Ingress-auth failure.
12. Review every non-local route to the Service for an equivalent boundary. No external reachability probe is implied by repository-only verification.
13. Before the UUID migration release, verify the applied Deployment has `replicas: 1`, `strategy.type: Recreate`, and no second process can access the SQLite database concurrently. Observe that the old Pod is gone before the new-version Pod starts and that startup migration/backfill completes before the app becomes ready.
14. Use the Let's Encrypt staging endpoint for repeated challenge/route verification; do not repeatedly request production certificates during tests.
15. Exercise the corrected `k8s/ingress.yaml`, `k8s/middleware.yaml`, and `k8s/sink.yaml` through real Traefik Kubernetes Ingress and CRD providers, not only a file-provider approximation or schema checker. Confirm the app router has TLS enabled and only `websecure`, and the priority-1 HTTP router exists on `web`, references only the redirect Middleware and empty sink, and is non-TLS. For this regression test leave `websecure` entrypoint-default TLS **unset** so the per-Ingress annotation is actually proved; set Ingress-provider `allowEmptyServices: true` and keep global redirects disabled so they cannot mask a missing HTTP router. The earlier control did not prove this exact combination. Do not publish an insecure Traefik API/dashboard to inspect it.
16. With a healthy instrumented backend, check HTTPS no credentials and invalid credentials → 401 with `WWW-Authenticate: Basic realm="YuHaul"`, zero backend hits; valid credentials → backend success and no upstream Authorization header. Check HTTP on port 80 → 301 to the same host/path/query over HTTPS, no BasicAuth challenge, zero backend hits. Also send **credential-free plaintext HTTP directly to port 443** with the YuHaul Host header → rejection/no application route, no BasicAuth challenge, zero backend hits. Do not require a particular rejection status for that malformed transport, and never send real credentials in this probe.
17. In isolation, remove the redirect Middleware (router rejected/no route), then restore it and detach only its reference from the HTTP test router (empty load balancer → 503); neither case may hit YuHaul or challenge for credentials. Confirm auth Middleware/Secret failures also reject HTTPS before dispatch. Restore all configuration. Toggling `allowEmptyServices` false must reproduce HTTP route omission, not application fallback. These tests distinguish fail-closed safety from a working redirect.

## Open questions

- **Verification owner — blocks full conformance/non-local exposure:** implementation and narrow local transport/redirect proof are complete as recorded above; finish the remaining real-provider checks in Verification 15–17 and obtain independent confirmation. Cluster deployment and certificate lifecycle remain unverified.
- **Deployment owner — blocks manifest application/non-local exposure:** confirm Traefik v3, the `traefik.io/v1alpha1` Middleware CRD/provider, `IngressClass/traefik`, `safeNaming: false` (or adapt references), `providers.kubernetesIngress.allowEmptyServices: true` and its shared-controller implications, non-TLS `web: :80` plus `websecure: :443` listeners/exposure, and TLS pass-through from the load balancer to Traefik.
- **Deployment owner — blocks automated issuance and non-local application use:** install/confirm cert-manager and ingress-shim; provide a Let's Encrypt production `ClusterIssuer` referencing its cluster-held ACME account-key Secret (created/managed by cert-manager); choose the actual issuer name (documented default `letsencrypt-prod`); confirm public DNS for `yuhaul.bateau.cloud` reaches the intended controller; and determine whether public port 80 is reachable. Use HTTP-01 through IngressClass `traefik` when it is, otherwise provision DNS-01 provider credentials/configuration. Confirm the deployment can keep application HTTPS unavailable until `yuhaul-tls` is Ready and trusted, then verify that Secret is created and renewed in namespace `default`.
- **Deployment owner — blocks HTTP-01 issuance/renewal:** the checked-in ingress manifest already sets redirect sink priority to `1` and carries the issuer/solver annotations, so the solver Ingress resolves to IngressClass `traefik`; verify on the cluster that the challenge route wins the priority comparison and reaches only the temporary solver.
- **Security owner / user — blocks a claim of fully safe public exposure, not this ingress contract:** is BasicAuth-only acceptable for the chosen browser/network threat model despite residual CSRF and rate-limit risks, or are additional defenses required?
- **Security owner / user — blocks only an absolute claim that no BasicAuth credential can ever traverse HTTP:** are all supported clients required to avoid sending credentials preemptively over HTTP? The server-side guarantee in this contract is no HTTP dispatch to YuHaul and no HTTP BasicAuth challenge; an initial client-sent HTTP Authorization header cannot be undone by a redirect.

## External references

- [Traefik v3 Kubernetes Ingress](https://doc.traefik.io/traefik/reference/routing-configuration/kubernetes/ingress/) — router entrypoint/middleware and priority annotations, TLS handling, and the limitation that Let's Encrypt certificates are not managed in Kubernetes Secrets by the Ingress provider.
- [Traefik v3 Kubernetes Ingress provider](https://doc.traefik.io/traefik/reference/install-configuration/providers/kubernetes/kubernetes-ingress/) — `allowEmptyServices` defaults to false; enabling it retains empty-service routes so middleware can run before a possible 503. Both this reference and the routing/TLS reference above were read during the 2026-10-25 correction.
- [Traefik v3 RedirectScheme Middleware](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/redirectscheme/) — Kubernetes CRD shape and redirect fields.
- [Traefik v3 Kubernetes Middleware CRD](https://doc.traefik.io/traefik/reference/routing-configuration/kubernetes/crd/http/middleware/) — CRD requirement and same-namespace reference behavior.
- [Traefik v3 Kubernetes CRD provider](https://doc.traefik.io/traefik/reference/install-configuration/providers/kubernetes/kubernetes-crd/) — provider configuration, including the `safeNaming` reference-name variant.
- [cert-manager annotated Ingress](https://cert-manager.io/docs/usage/ingress/) — ingress-shim creates a Certificate from the TLS host/secretName and supported issuer annotations.
- [cert-manager ACME Issuer](https://cert-manager.io/docs/configuration/acme/) — ACME server/account configuration and the cluster-held account-key Secret.
- [cert-manager ACME HTTP-01 solver](https://cert-manager.io/docs/configuration/acme/http01/) — solver IngressClass selection and temporary solver resources.
- [cert-manager ACME DNS-01 solver](https://cert-manager.io/docs/configuration/acme/dns01/) — DNS TXT validation and provider configuration for the no-public-port-80 alternative.
- [Traefik v3 ACME certificate resolver](https://doc.traefik.io/traefik/reference/install-configuration/tls/certificate-resolvers/acme/) — ACME storage configuration and Kubernetes caveats; recommends a certificate controller such as cert-manager for Kubernetes Secret-backed certificates.
- [Traefik v3 HTTP router priority](https://doc.traefik.io/traefik/reference/routing-configuration/http/routing/rules-and-priority/) — default priority is rule length, with explicit numeric priority support.
- [Kubernetes Deployments: Recreate strategy](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#recreate-deployment) — stop-before-start rollout semantics.
