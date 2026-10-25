---
type: decision
title: ADR-010 — Protect YuHaul Ingress with Traefik BasicAuth
description: Establish Traefik BasicAuth and HTTPS as the access boundary for every non-local YuHaul ingress path, with externally provisioned credentials and an explicit local port-forward bypass.
tags: [architecture, decision, yuhaul, ingress, authentication, traefik, security]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** accepted

YuHaul's named-host access remains HTTPS with a shared Traefik BasicAuth challenge; ordinary HTTP only redirects and never selects the app. For example, HTTPS `/api/routes` without credentials must receive 401 before any app request. The 2026-10-25 independent provider review found the previous realization incomplete: a TLS Secret reference did not activate router TLS, and Traefik omitted the empty-sink HTTP router. The correction below retains the existing boundary and certificate lifecycle, without adding application authentication or another workload. Cluster provisioning and the exact corrected request-path proof remain required.

## Derived from

- Explicit user direction to protect YuHaul with Traefik BasicAuth.
- User expectation that Let's Encrypt certificates be provisioned and renewed automatically while preserving the existing `yuhaul-tls` Secret interface.
- [YuHaul System Architecture](yuhaul-system-architecture.md).
- Security evidence: `docs/ephemeral-reports/hacker/security/2026-10-24.md`, findings YH-01 through YH-07. The report confirms no inbound API authentication in the current server wiring and an nginx-class ingress without auth or a TLS block; it does **not** verify public internet reachability.

## Satisfies

- The user's accepted direction that Traefik BasicAuth protect the YuHaul ingress.
- The demonstrated YH-01/YH-02 access-control gap for requests that enter through the YuHaul Ingress. This decision does not claim protection for direct service, pod, or port-forward access.
- No accepted BR currently specifies inbound administrative authentication. BR-001 concerns the outbound YouTube OAuth account and is not an ingress-authentication requirement.
- The user's automatic Let's Encrypt expectation, realized by cert-manager while retaining the exact `spec.tls.secretName: yuhaul-tls` contract; no current BR separately defines certificate lifecycle behavior.

## Decision question

Where should authentication and TLS certificate lifecycle be enforced for the single-user YuHaul administrative UI/API, and what transport and deployment prerequisites are required before non-local application use?

## Drivers and constraints

- The unauthenticated UI/API can expose private run and route history and can initiate actions against the operator's YouTube account if reachable.
- YuHaul is a single-user self-hosted application. The user explicitly selected Traefik BasicAuth rather than an application login design.
- The current Kubernetes Service is `ClusterIP`; the Ingress is in namespace `default`, names `yuhaul.bateau.cloud`, and routes `/` to the service. At decision time it declared the nginx class with no auth middleware annotation and no `spec.tls` block; the checked-in manifest has since been moved onto the Traefik contract defined below.
- BasicAuth credentials are reusable secrets. HTTPS is mandatory for every non-local network exposure.
- The user also expects Let's Encrypt issuance and renewal to be automatic. The HTTPS Ingress contract already requires a Kubernetes TLS Secret named exactly `yuhaul-tls`.
- No public-network reachability claim is supported by repository evidence.

## Options considered

### A. Traefik BasicAuth at the Ingress (chosen)

Apply Traefik BasicAuth only to the HTTPS `websecure` Ingress route for the host's `/` Prefix. A separate HTTP `web` Ingress uses a RedirectScheme Middleware and targets an endpoint-free sink Service, never the YuHaul Service. The valid TLS Secret, explicit `web`/`websecure` entrypoints, TLS pass-through to Traefik, CRD-backed Middlewares, and externally provisioned bcrypt htpasswd Secret are prerequisites for non-local exposure. The redirect and no-HTTP-backend invariant live in checked-in Kubernetes objects rather than depending on a controller-wide redirect setting.

- **Benefits:** Uses the requested proxy boundary; covers the SPA, REST API, OAuth routes, and SSE through one HTTPS `/` Prefix route; makes the HTTP route's backend structurally incapable of reaching YuHaul even if the redirect middleware is missing; does not add login/session state or modify the application server.
- **Costs:** Requires Traefik's Ingress and CRD providers, Middleware CRDs, explicit `web`/`websecure` entrypoints, and a permanently endpoint-free sink Service; credentials are shared at the proxy boundary; operators provision/rotate the BasicAuth Secret and operate cert-manager plus a production ClusterIssuer.
- **Failure modes:** An unprotected alternate Ingress or direct service path bypasses the middleware. Missing/invalid TLS still makes HTTPS identity untrustworthy; cert-manager or issuer failure prevents automatic population/renewal of `yuhaul-tls`. An HTTP-01 solver route that loses priority to the redirect sink cannot validate. A client that proactively sends BasicAuth credentials in an initial HTTP request has already exposed them before a redirect can respond; the HTTP route does not challenge or route to YuHaul, but server-side redirect cannot prevent client-side preemptive transmission. The application itself does not authenticate callers.
- **Security and coupling:** Small deployment-only boundary, but every externally reachable route to YuHaul must attach the middleware. BasicAuth is not CSRF protection, per-user authorization, or rate limiting.
- **Obligation impact:** Meets the requested Ingress authentication behavior without adding an application identity/session system. Port-forward and cluster-internal access remain trusted deployment paths, not authenticated paths.

### B. Application login/session and request authorization

Add an application login, session lifecycle, and request middleware to YuHaul.

- **Benefits:** Protects every network path reaching the application, including direct service access; can support application-level CSRF defenses and future identity-aware policy.
- **Costs:** Adds persistent/session lifecycle, browser login/logout behavior, CSRF and cookie configuration, and application code beyond the explicit Traefik BasicAuth direction.
- **Failure modes:** Incorrect session or CSRF handling can create a second security boundary with its own bypasses; port-forward would still reach the app but would then be guarded.
- **Security and coupling:** Stronger application-level enforcement, but increases coupling between the SPA, API, and identity mechanism. No multi-user authorization requirement exists.
- **Obligation impact:** Satisfies the ingress-auth goal but is not the least-complex realization of the user-selected proxy boundary.

### C. Keep the service private and remove the Ingress

Expose YuHaul only through a trusted private network or operator-controlled port-forward.

- **Benefits:** Avoids publishing an HTTP control plane and requires no proxy credential middleware.
- **Costs:** Does not realize the explicit request to use Traefik BasicAuth for the ingress and removes the configured named-host access path.
- **Failure modes:** Accidental exposure through another network path remains possible; operators cannot use the named ingress.
- **Security and coupling:** Smallest runtime boundary if private-only exposure is the goal, but it changes the requested deployment behavior.
- **Obligation impact:** Rejected because it does not satisfy the selected Traefik-authenticated ingress direction.

### Certificate lifecycle alternatives within the selected Ingress boundary

#### C1. Continue manual provisioning of `yuhaul-tls`

The deployment owner creates and renews the Kubernetes `kubernetes.io/tls` Secret by hand or through an unspecified external process.

- **Benefits:** No additional controller dependency if an existing certificate process is already in use; retains the current `spec.tls.secretName` contract.
- **Costs and failure modes:** Does not realize the user's automatic Let's Encrypt expectation without an external mechanism; manual expiry/renewal can leave the host without a trusted certificate.
- **Security and coupling:** The operator owns TLS key distribution and Secret lifecycle; no new in-repository secret is needed.
- **Obligation impact:** Compatible with the Ingress shape but incomplete for the requested automatic issuance/renewal behavior.

#### C2. Traefik built-in ACME certificate resolver

Configure Traefik's ACME resolver to request certificates directly and store its ACME state in its configured file storage (commonly `acme.json`).

- **Benefits:** Certificate requests and renewals stay inside the Traefik process; no Kubernetes certificate controller is needed.
- **Costs and failure modes:** Traefik's [Kubernetes Ingress provider documentation](https://doc.traefik.io/traefik/reference/routing-configuration/kubernetes/ingress/) says Let's Encrypt certificates cannot be managed in Kubernetes Secrets. The resolver therefore does not produce the `default/yuhaul-tls` Secret that the accepted Ingress contract names. Traefik's [ACME documentation](https://doc.traefik.io/traefik/reference/install-configuration/tls/certificate-resolvers/acme/) also documents a Kubernetes availability caveat for multi-instance ACME because challenge requests can reach the wrong instance.
- **Security and coupling:** Requires persistent/protected Traefik ACME storage and makes certificate lifecycle depend on the Traefik deployment model; this is a different interface from the application's Kubernetes TLS Secret.
- **Obligation impact:** Rejected for the current contract because it cannot satisfy `spec.tls.secretName: yuhaul-tls` as documented by Traefik's Kubernetes provider. It could be reconsidered only if the Kubernetes Secret interface is deliberately replaced.

#### C3. cert-manager ingress-shim (chosen)

Annotate the HTTPS Ingress with an operator-configurable `cert-manager.io/cluster-issuer` reference. cert-manager ingress-shim creates a Certificate in the Ingress namespace using `spec.tls.hosts` and the existing `spec.tls.secretName`, then stores/renews the certificate in that Secret, as specified by [cert-manager's annotated Ingress contract](https://cert-manager.io/docs/usage/ingress/).

- **Benefits:** Realizes automatic Let's Encrypt issuance and renewal while preserving the existing Ingress, Secret name, Secret type, and Traefik consumer interface. The same Secret contract works with HTTP-01 or DNS-01 challenges.
- **Costs and failure modes:** Requires cert-manager installation, a production ClusterIssuer with its cluster-held ACME account-key Secret, and public port-80 reachability for HTTP-01 or external DNS-provider access for DNS-01. cert-manager creates/manages its account-key Secret; no account key is committed. If renewal or Secret synchronization fails, HTTPS is not ready and must not be treated as trusted.
- **Security and coupling:** Certificate/key and issuer credentials are held in Kubernetes Secrets outside the repository; cert-manager becomes the sole owner of `default/yuhaul-tls`. The integration stays at the Kubernetes Ingress/TLS Secret boundary rather than adding application code or Traefik storage semantics.
- **Obligation impact:** Chosen as the least-complex option that both meets the automatic Let's Encrypt expectation and preserves the exact existing TLS contract.

## Comparison

Within Option A, a controller-wide `web`→`websecure` entrypoint redirect is simpler but leaves the security-critical guarantee in unversioned controller configuration and gives the HTTP router no manifest-level backend boundary. A redirect-only Ingress plus an empty sink adds one Middleware, one Ingress and one Service, but it fails closed against YuHaul dispatch even when the redirect component is absent. This is the least complex complete pattern that keeps the security boundary in reviewable Kubernetes resources. Option B offers stronger protection against non-Ingress paths and enables application-level request protections, but it invents a larger login/session system not requested. Option C would be simpler only by removing the requested ingress access path. For certificate lifecycle, manual provisioning is compatible but does not meet the automatic expectation; Traefik's built-in ACME resolver does not satisfy the Kubernetes `yuhaul-tls` Secret contract; cert-manager ingress-shim preserves that contract. No option makes public reachability safe without HTTPS and operator-provisioned BasicAuth credentials.

### 2026-10-25 provider correction: alternatives and decision

**Question:** What is the smallest complete correction to activate HTTPS routing and retain redirect middleware execution for the deliberately empty sink?

The real-provider evidence summarized in [ARS-003](ars-003-traefik-basic-auth-kubernetes-contract.md) establishes two independent defects on Traefik v3.7.16: without TLS activation the app router challenges plaintext on `websecure` but returns HTTPS 404; without Ingress-provider `allowEmptyServices`, the HTTP sink route is omitted. Neither `spec.tls` nor an entrypoint named `websecure` is a sufficient transport boundary. These findings are accepted; the user-facing authentication, redirect-only HTTP, and automatic-certification promises are unchanged.

| Realization | Benefits | Costs / failure modes | Disposition |
|---|---|---|---|
| App Ingress `router.tls: "true"` plus static `providers.kubernetesIngress.allowEmptyServices: true` | One app annotation; reuses both Ingresses, Middlewares, empty sink, and cert-manager lifecycle; TLS-only app route remains explicit in source | Operator must enable the provider-wide setting; other empty-service routers can remain active (middleware/error/priority behavior changes); actual per-Ingress combination still needs proof | **Chosen:** least change to existing resources and accepted interfaces |
| Entrypoint-default `entryPoints.websecure.http.tls: {}` plus the same `allowEmptyServices: true` | Supported Traefik mechanism; reviewer's local control produced HTTPS 401 and HTTP 301 | Makes YuHaul TLS activation depend on a shared entrypoint default and changes defaults for every attached router | Viable, but loses to a local app annotation; optional additional default only, never the sole realization |
| Keep `allowEmptyServices: false` and supply a live sink workload | Makes the Ingress provider retain the redirect router without changing global empty-service handling; app need not be the backend | Adds image/workload/lifecycle/availability for a backend ordinary requests must never reach; abandons the accepted endpoint-free sink topology | Rejected: unnecessary machinery absent a demonstrated constraint prohibiting the provider option |
| Point HTTP at YuHaul, or rely solely on a global redirect | Small superficial diff | Missing redirect can route plaintext to the app, or removes the manifest-level backend boundary | Rejected: violates the accepted no-HTTP-app invariant |

The first option requires a controller prerequisite but **not** controller-owned HTTP redirection or app TLS activation. Source-owned topology still fails closed if the empty-service setting is absent: HTTP has no route rather than an app fallback. Provider-wide `allowEmptyServices` can affect unrelated routes, so the operator must review shared-controller precedence/error dependencies; no shared-controller restriction is demonstrated in this repository. If one is established, revisit a scoped redirect realization rather than add an unsolicited controller or silently weaken the boundary. No IngressRoute migration, dummy workload, new certificate resolver, or live credential belongs in this correction.

## Decision

Use **Traefik BasicAuth Middleware on the HTTPS YuHaul Ingress**, with externally provisioned bcrypt htpasswd credentials and cert-manager-managed Let's Encrypt TLS. A separate HTTP redirect Ingress targets only the dedicated endpoint-free sink Service, while the authenticated app Ingress is attached only to `websecure`. cert-manager ingress-shim owns the `default/yuhaul-tls` Secret lifecycle without changing the exact Ingress `spec.tls.secretName` contract. The conventional issuer annotation default is `letsencrypt-prod`, but the issuer name is a deployment patch point and the repository does not install a ClusterIssuer. The exact Kubernetes object names, Middleware fields, Secret keys, annotations, challenge routing, entrypoint prerequisites, TLS lifecycle, and local port-forward behavior are normative in [ARS-003](ars-003-traefik-basic-auth-kubernetes-contract.md).

The app Ingress MUST explicitly set `traefik.ingress.kubernetes.io/router.tls: "true"`. The operator MUST enable static `providers.kubernetesIngress.allowEmptyServices: true` so the HTTP sink-backed router exists and its redirect can execute; the CRD-provider option of the same name does not satisfy this prerequisite. `web` remains non-TLS for redirects and HTTP-01. Keep all BasicAuth, certificate annotations/Secret references, sink backend, and redirect priority `1` unchanged.

The middleware is the access boundary only for requests traversing that Ingress. It MUST cover all user-facing paths on the host, including `/`, every `/api/` path, the OAuth callback, and the SSE stream. A second Ingress or other externally reachable route to the service MUST use the same protection. Do not add credentials or placeholder credentials to the repository.

For non-local application use, a valid trusted `yuhaul-tls` Secret and the ARS-003 HTTP redirect/HTTPS-auth pattern are prerequisites. cert-manager and a production ClusterIssuer must be installed/configured by the deployment owner. If HTTP-01 is used, public port 80 must be reachable for ACME validation, and its temporary solver route must outrank the redirect sink without reaching YuHaul; the redirect sink is assigned priority `1`. DNS-01 is the operator alternative when public port 80 is unavailable. The controller must expose `web` at port 80 and `websecure` at port 443; a controller-wide redirect may be added but is not required. Do not send credentials or treat the application as ready until cert-manager has populated `yuhaul-tls` and Traefik serves the trusted certificate. The checked-in manifests carry the issuer/solver annotations and the sink priority `1` required by this reconciliation; neither those annotations nor the architecture text is a claim about applied cluster state.

`kubectl port-forward` to the ClusterIP Service is an intentional local operator path that bypasses Traefik BasicAuth and TLS. It is acceptable only when bound to loopback and used by an operator with Kubernetes access; it is not a remotely exposed access path or an authentication substitute.

## Consequences

- **Positive:** Every HTTPS request entering via the specified Traefik host route receives a BasicAuth challenge before reaching the application; HTTP never dispatches to YuHaul and does not receive an auth challenge. The implementation does not need a second user/session database.
- **Negative:** An authenticated user has the full single-user administrative authority. BasicAuth does not identify an individual operator in the application audit trail. Direct in-cluster paths and port-forward bypass the middleware.
- **Security residuals:** BasicAuth alone does not provide CSRF protection, XSS protection, per-user authorization, or brute-force/rate limiting. These are not silently claimed as solved. The security/deployment owner must assess them before treating public internet exposure as an approved risk.
- **Certificate lifecycle:** cert-manager adds an external controller and issuer configuration but writes the same `default/yuhaul-tls` Secret the Traefik Ingress already consumes. It is the sole Secret owner while active; no TLS key, ACME account key, or DNS-provider credential is committed. HTTP-01 introduces a narrow temporary solver route, with explicit route priority to keep it ahead of the redirect sink; DNS-01 remains available if public port 80 is unavailable.
- **Deployment:** The corrected app Ingress explicitly activates router TLS; README includes the Ingress-provider empty-service prerequisite, shared-controller implications, and 404 diagnosis. The narrow local real-provider probe recorded in ARS-003 confirms HTTPS 401, HTTP 301, plaintext TLS-port no challenge, and zero backend hits without entrypoint TLS/global redirect defaults; disabling the provider option reproduces HTTP 404. This is not full Verification 15–17, independent re-review, cluster readiness, or certificate-lifecycle proof. Issuance and renewal remain external ingress/cert-manager responsibilities, not application mechanics.
- **Shared-controller impact:** `allowEmptyServices: true` retains other empty-service routers and their middleware, potentially changing route precedence and empty-backend responses to 503. It does not make empty backends reachable or remove BasicAuth. Review deployment-specific dependencies before changing the controller; no controller installation is added to the app repository.
- **Scope:** No claim is made that `yuhaul.bateau.cloud` resolves publicly or is reachable from the internet.

## Confirmation and reconsideration

Before enabling non-local ingress, require independent security review and deployment evidence that:

1. unauthenticated HTTPS requests to representative SPA, API mutation/read, OAuth, and SSE paths receive the Traefik BasicAuth challenge;
2. valid credentials succeed, invalid credentials fail, and the Authorization header is not forwarded to YuHaul;
3. the certificate is valid for `yuhaul.bateau.cloud`; HTTP never dispatches to YuHaul, sends no BasicAuth challenge, and reaches only the redirect middleware/empty sink. A client-sent preemptive Authorization header on HTTP remains outside what a redirect can protect;
4. every YuHaul Ingress path uses the middleware, while the local loopback port-forward is documented as a bypass;
5. credentials and TLS private keys are absent from committed files and are provisioned by the operator's secret-management process;
6. cert-manager's Certificate is Ready, the exact `default/yuhaul-tls` Secret is populated, Traefik serves its host-matching trusted certificate, and a renewal updates/serves the same Secret; for HTTP-01, the active challenge route reaches the solver instead of the priority-1 sink and has zero YuHaul backend hits. Use a staging issuer for repeated verification.
7. the exact corrected manifests work through real Kubernetes Ingress/CRD providers with `websecure` entrypoint-default TLS unset, `allowEmptyServices: true`, and no global redirect masking the sink router. Inspect TLS/non-TLS routers and test credential-free plaintext directly on port 443: no BasicAuth challenge and zero app hits. Failure injection must prove sink-only/no-route behavior when redirect is detached/missing, and auth rejection before backend dispatch when BasicAuth dependencies fail. ARS-003 Verification 15–17 is normative; recommend independent reviewer confirmation before relying on the correction. The earlier entrypoint-TLS control does not prove the chosen per-Ingress realization.

Reconsider application-level authentication if YuHaul gains multiple users, if callers outside the trusted Kubernetes/operator boundary can reach the Service directly, or if accepted security requirements require application-enforced CSRF/session identity rather than proxy authentication. Reconsider the public exposure posture if security review finds BasicAuth-only insufficient for the intended deployment threat model.

## Open questions

- **Verification owner — blocks full conformance and non-local Ingress activation:** implementation and narrow local transport/redirect proof are complete as recorded in ARS-003; finish its remaining Verification 15–17 checks and independent re-review. Cluster readiness remains unverified.
- **Deployment owner — blocks non-local Ingress activation:** confirm Traefik v3, the `traefik` IngressClass, Middleware CRD/provider and naming mode, Ingress-provider `allowEmptyServices: true` and its shared-controller impact, non-TLS `web` plus `websecure` listeners, TLS pass-through from the load balancer, and load-balancer exposure; deploy and verify the complete ARS-003 two-route pattern. The repository does not establish cluster readiness.
- **Deployment owner — blocks automatic issuance and non-local Ingress activation:** install/confirm cert-manager ingress-shim and provide a production `ClusterIssuer` referencing the cluster-held ACME account-key Secret that cert-manager manages; select its actual name (documented default `letsencrypt-prod`); confirm public DNS points `yuhaul.bateau.cloud` at the intended controller; configure HTTP-01 with `traefik` when port 80 is reachable or DNS-01 with external provider credentials otherwise; and confirm HTTPS application access can remain unavailable until the trusted certificate is issued. Then verify certificate issuance, renewal, solver priority, and Traefik's served certificate. The checked-in Ingress carries issuer/solver annotations, redirect priority, and explicit app TLS activation; apply the documented provider prerequisites as well.
- **Deployment owner — blocks non-local Ingress activation:** provision the `yuhaul-basic-auth` credentials Secret outside source control.
- **Security owner / user — does not block the architecture record; blocks a claim that public exposure is fully safe:** decide whether BasicAuth-only is acceptable for the intended network/browser threat model given residual CSRF and rate-limit risks, or require additional defenses before public exposure.
