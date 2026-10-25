---
type: decision
title: ADR-016 — Semantic Releases and Release-Gated Container Publication
description: Selects main-only semantic releases with an explicit same-run image handoff and private repository-scoped GHCR publication without additional credentials.
tags: [architecture, decision, release-automation, conventional-commits, github-actions, ghcr]
timestamp: 2026-10-25T00:00:00Z
---

**Status:** implemented locally; remote release/GHCR publication not verified

Implementation: `.github/workflows/release.yml`, root `package.json` / lockfile,
`release.config.mjs`, `scripts/release.mjs`, `scripts/check-pr-title.mjs`, and
`scripts/release.test.mjs`, and `scripts/smoke-image.sh`. Dockerfile copies only Go source directories; the
allowlisted `.dockerignore` excludes release dependencies and local material.
README documents operation and failed-image-job-only recovery. Existing-package
privacy/write-access verification remains an administrator prerequisite before
activation, not an automated REST preflight: GitHub's Packages REST API reference
documents classic-PAT authentication. Requiring that endpoint could block the
chosen built-in-token publication path; no additional credential or visibility
mutation is introduced. First actual workflow-token publication remains unverified.

Default-image startup correction (2026-10-25): the runtime now owns `/data` as
1000:1000 at build time, retaining the non-root entrypoint and avoiding runtime
recursive ownership changes. The previous root-owned 0755 directory prevented
SQLite initialization. Read-only `verify-image` builds the actual Dockerfile on
PR/main and runs `scripts/smoke-image.sh` before release creation: default CMD,
UID 1000, default SQLite creation, API/SPA readiness, fresh anonymous/named
volumes and database restart. A local Podman linux/amd64 build and this smoke
check passed after the original image failed the same check. Docker/GitHub CI
execution and Kubernetes storage-driver permission behavior remain unverified.
Existing volumes/bind mounts must be writable by UID 1000; the accepted Kubernetes
deployment retains runAsUser/runAsGroup/fsGroup 1000, whose volume application
depends on its storage driver. Image ownership cannot repair a mounted PVC.

Local confirmation on 2026-10-25: locked install, four release-tool tests (real
analyzer/notes/parser, API handoff guard, and native first-release dry run),
actionlint, Go build/vet/tests/integration, and SPA tests/build/lint passed. The
compatible Conventional Commits preset is locked to 9.3.1: preset 10 requires a
newer changelog writer than the selected notes generator supplies. Standard
revert + original cancel when analyzed in Git's newest-first order. Docker CLI is
absent, but Podman successfully built the real Dockerfile as `linux/amd64`;
filesystem checks confirmed the non-root runtime has the binary/SPA and the Go
stage has no checkout credentials, host dependencies, local server binary, or
Kubernetes directory. This is a local image build, not Buildx/GHCR publication,
OCI publication-label/digest proof, exhaustive layer scanning, or failed-job retry
verification. The root npm audit reports two advisories in unused bundled npm
dependencies pulled by semantic-release's default npm plugin dependency; that
plugin is not configured. No runtime/SPA dependency change was made here.

After trusted changes reach `main` and verification passes, semantic-release decides whether they warrant a version and publishes its Git tag and GitHub release. Only a newly published release starts the release-image build. The image uses that release's exact source commit, not whichever commit is newest when the build starts. For example, the first release can produce `v1.0.0` and `ghcr.io/bateau84/yuhaul:v1.0.0`; later fixes produce patch releases. Changes that do not warrant a release produce no release image.

Keep release creation and image publication in one workflow run. GitHub suppresses the downstream release event when a release is created with `GITHUB_TOKEN`; a separate `release: published` workflow would silently fail to build these images. Image failure can leave a GitHub release without an image; the two services do not provide an atomic transaction. No deployment, cluster credentials, version-bump commits, retrospective tags, or history rewrite is included.

## Derived from

- The 2026-10-25 delegated request: establish minimal safe realization for semantic-release / Conventional Commits and image build-on-release, choose routine CI defaults, inspect refs/permissions, and do not commit/push/deploy during this work.
- Existing [system architecture](../../yuhaul-system-architecture.md): single-container self-hosted deployment. This decision changes artifact production, not runtime behavior or deployment authority.
- Evidence read: `Dockerfile`, `web/package.json`, `README.md`, `k8s/deployment.yaml`, Git refs, GitHub repository/Actions/PR/release APIs, and the [requirements index](../../../requirements/yuhaul/index.md).

## Satisfies

| Obligation | Disposition |
| --- | --- |
| Requested semantic versioning from Conventional Commits | Satisfied by main-only semantic-release and shared Conventional Commits preset. |
| Release-image build only after a new release | Satisfied by successful release-job outputs and a dependent image job in the same run. |
| No new registry secret required | Supported by GHCR's documented repository-scoped token path; actual first publication remains unverified. |
| No agent commit/push/deployment | Preserved. Future CI artifact publication is distinct from agent operations now and from deployment. |
| Existing BR-001–BR-032 / OC-001–OC-004 | Satisfied elsewhere by existing runtime designs/contracts; no application behavior or acceptance claim changed here. |
| Atomic release/image availability, public access, multi-architecture support, automatic rollout | Not promised by this request; not silently added. |

## Decision question

How can YuHaul produce versioned GitHub releases and corresponding container images without extra credentials, event-chain failure, or privileged pull-request execution?

## Drivers and constraints

Observed on 2026-10-25:

- Private `bateau84/YuHaul`; default branch/remote `HEAD` is `main`. Open PR #3 is `anchor` → `main`. Remote main was `b3f8b9a8d42b9174b803182d5a78cdc73ae269aa`; remote anchor was `86563377a2db554d81b1dbc4ad2ba1a4c1634bd8`; local anchor was further ahead. These hashes are observations, not permanent configuration.
- Remote tag listing was empty and GitHub releases API returned `[]`. Main has `first commit`, `feat: implement YuHaul ... (#1)`, and `fix: ... (#2)`; old history must not be rewritten merely for lint adoption.
- No `.github` directory, root release config, or root Node manifest was present. Private `web/package.json` has version `0.0.0`; it is not application release authority.
- Actions are enabled, all actions permitted, default token read-only, no enforced SHA pinning. Main is unprotected. Rulesets API returned 403 with a plan-upgrade requirement; enforced branch review cannot be assumed.
- Squash, merge, and rebase merges are enabled. Squash defaults use PR title/body. A title check alone does not enforce non-squash commit contents.
- Dockerfile already builds SPA + Go server into one Alpine image; mutable base tags and broad `COPY . .`, with no `.dockerignore`, preclude a reproducibility claim and require context hygiene.
- Existing container-package listing returned 403 because the local CLI token lacks `read:packages`. Conflicting package names, visibility, and Actions write grants remain unknown, not absent.

Only trusted code accepted onto main may receive release/registry write authority. Currently this trusts repository writers, not enforced reviews. PR checks use ordinary `pull_request`, read-only tokens, and no release secrets. Do not use `pull_request_target`, privileged `workflow_run`, PR artifacts, PR-selected refs, or PR-derived registry destinations for publication. Use isolated GitHub-hosted runners, not persistent privileged self-hosted runners.

## Options considered

| Option | Benefit | Cost / failure / authority impact | Disposition |
| --- | --- | --- | --- |
| Separate release-event image workflow, default token | Familiar separation | Bot-created release event does not start it | Rejected: fails handoff. |
| Release-event image workflow with PAT / GitHub App | Independent event consumer, including manual releases | Extra credential/authority solely for event suppression workaround | Viable but unnecessarily complex. |
| Dispatch / workflow-run bridge | Starts another run | Extra validation/recovery surface; privileged workflow-run risks low-trust producer escalation | Rejected: same-run dependency suffices. |
| Same workflow, dependent image job | Explicit handoff, separate permissions, no extra secret | Non-atomic image failure; manually created releases not handled | Chosen. |

Baseline alternatives: use native untagged first release, or establish a historical semver tag. Choose native behavior; no real previously published version was found to justify a historical tag. Never invent `v0.0.0` / `v1.0.0` tags to hide history. Registry alternatives: GHCR reuses repository identity/token; Docker Hub requires new credentials. Choose GHCR. Build-only downloadable archives remain an alternative if publication is not authorized, not a necessity merely because no registry secret exists.

## Decision

### Version and tooling authority

- Explicit release branches: `main` only; no inherited maintenance/prerelease patterns, anchor releases, or tag-triggered publication. Trigger on main `push`, without path filters, after verification of that event SHA in the same run.
- With no previous semver tag and release-worthy history, native first release is `1.0.0`. Analyze reachable history; old nonconforming messages are ignored by the analyzer, not rewritten or retroactively linted. Even a `ci:` enabling commit may release initially because old `feat:` / `fix:` commits are unreleased.
- Git tag format `v${version}`; tags, not SPA package version or PR text, determine versions. A v1 label does not certify product acceptance.
- Explicit plugins in order: `@semantic-release/commit-analyzer`, `@semantic-release/release-notes-generator`, `@semantic-release/github`. Both analyzer/notes use `preset: conventionalcommits`, compatible explicit preset configuration, and the installed preset package. Confirm consistent `!` and `BREAKING CHANGE:` parsing.
- Breaking → major, feat → minor, fix/perf → patch; other types do not release unless breaking. Highest applicable increment wins. Verify revert behavior against the locked preset instead of inventing a broad rule.
- GitHub plugin: disable `successComment`, `failComment`, `failTitle`, and `releasedLabels` with `false`, avoiding issue/PR write permission. No npm plugin/token/publication, Git commit plugin, committed generated changelog, or version-bump commit.
- Root private manifest/lockfile owns release tooling, independent of `web/`. Install locked dependencies and run the installed tool, not an unversioned download. Select Node 24 for release tooling; confirm the locked tool's engine range (current official docs require ≥22.14). No Docker SPA Node upgrade is implied.
- Validate future PR titles with the same grammar on ordinary `pull_request` events, including `edited`. Pass titles as file/stdin or quoted environment data; never interpolate them into shell source. Recommend squash merge so title becomes analyzed commit. Other allowed merge modes must retain conventional constituent messages and breaking notices. Title-only validation does not guarantee all main commits conform; no merge settings are changed here.

### Exact same-run handoff

| Job | Dependency / condition | Token permissions | Contract |
| --- | --- | --- | --- |
| Verify | Main push or PR | `contents: read` only | Check out event SHA; Go build/test/vet/integration, SPA install/test/build, release tooling and actual image default-entrypoint smoke checks. Cannot publish. |
| Release | Verify succeeds; event push; ref exactly `refs/heads/main` | `contents: write` only | Full history/tags; event SHA, not untested remote head; invoke semantic-release once through documented JS API. |
| Image | Release succeeds AND output `published == 'true'` | `contents: read`, `packages: write` only | Validate outputs, fresh checkout of released SHA, assert tag resolves to it, build root Dockerfile and publish versioned GHCR image. |

Verification floor: `go build ./...`, `go vet ./...`, `go test -count=1 ./...`, `go test -tags=integration -count=1 ./e2e/...`; inside web, `npm ci`, `npm test`, `npm run build`; image verification builds the root Dockerfile and runs `sh scripts/smoke-image.sh yuhaul-smoke:verify`. Release depends on all required checks. Verification builds are local only; release image publication remains gated on a newly published release.

Release job string outputs are `published` (`true` only after successful non-dry-run publication, otherwise `false`), `version` (`nextRelease.version`), `git_tag` (`nextRelease.gitTag`), and `git_head` (`nextRelease.gitHead`, full SHA). Emit them after API success and confirm the result includes the configured GitHub publisher. False result skips image; thrown error fails release and skips image. Never scrape logs, infer a version, consume dry-run results, or query “latest release” as a substitute for this handoff. Require released SHA to equal verified event SHA. Validate stable semver, exact `v` + version, and full hexadecimal SHA before checkout/shell use; pass these as data, not generated shell source.

One main/repository release-concurrency group spans this workflow with `cancel-in-progress: false`. Active release/image work must not be cancelled by a new push. Pending runs may be superseded by GitHub; there is no one-release-per-push promise. A stale run skips/fails safely rather than resetting to remote main and releasing unverified code. No moving latest alias is needed, avoiding downgrade when an old failed job is retried.

### Image and credentials

- Name: `ghcr.io/` + lowercase `github.repository`, currently `ghcr.io/bateau84/yuhaul`; destination derives from trusted context, not PRs/inputs/artifacts.
- Tag: exact release Git tag, e.g. `:v1.2.3`. No latest, major/minor, or development aliases. Initial platform `linux/amd64`; no multi-architecture claim.
- Record pushed digest and full image reference in job summary; OCI labels carry source URL, version, and full source SHA. Digest is exact identity; GHCR tags remain mutable references. Rebuilding the same source may yield another digest because existing base tags/dependencies are mutable; do not claim byte reproducibility or enforced tag immutability.
- GHCR login uses `github.actor` + that job's `GITHUB_TOKEN`. No PAT, Docker Hub credential, npm token, OIDC, or cluster credential is needed.
- Preserve private visibility. New GHCR packages default private. An existing package needs this repository's Actions write grant and must be verified private before uploading; do not change visibility or fall back to a broader credential to bypass failure.
- Fresh source-only image context excludes `.git`, credentials, local secrets/env files, databases/uploads, host dependencies, and local binaries. Existing broad COPY and missing dockerignore must not copy checkout auth/local data into build stages. Do not persist checkout credentials; registry config and release-tool installations stay outside context. Never use token build args or copy credentials into layers.
- Pin third-party actions to reviewed full commit SHAs with version comments; explicit job timeouts; no PR-produced executable cache/artifact reuse. Signing, external builders, cross-run bridges, and extra coordination stores are unnecessary for the stated goal.

### Failure and authority boundary

Git tag/GitHub release exists before image build. Failed build/login/push leaves a failed image job and intact release/tag; never deploy or report image success. Maintainer may retry only the failed image job with successful release job outputs retained. Rerunning the whole release job may correctly find no new release and is not image recovery. Partial release success (tag pushed, GitHub release missing) needs inspection/repair; no automatic transaction recovery or version deletion is promised.

This architecture-only work pushes nothing, publishes nothing, edits no settings, commits nothing, and accesses no cluster. Future CI publication necessarily writes release tags/notes and registry artifacts, not version-bump commits or deployments. If “never push” upstream also forbids future CI writes rather than agent operations now, resolve that authority conflict before activating publication.

## Consequences

- No additional credential is inherently required; package write stays out of release job, repository write out of Docker job.
- Event suppression is solved structurally without undocumented credential substitution.
- Private images require authenticated external pulls. This does not provision pull secrets, change Kubernetes manifests, or promise anonymous deployment access.
- Main currently trusts repository writers. Required reviews/checks and branch/tag protection are recommended if the account plan permits them; do not grant branch-bypass credentials merely to automate releases.
- First notes include recognized historical changes; neither clean-slate notes nor production readiness is certified.

## Confirmation and reconsideration

Independent workflow/security critique is recommended before reliance. Required confirmation:

1. Locked parser/title/notes tests recognize feat/fix/perf, `!`, breaking footer, non-releasing docs/ci/chore, mixed legacy messages, and a first `1.0.0` without fabricated baseline.
2. Shell-looking PR title stays data; PR/fork/anchor/tag/manual-release events cannot reach publishing jobs or write credentials.
3. False/error release results skip image; successful GitHub result carries tested SHA/version/tag. New pushes cannot cause untested-head release.
4. First real trusted-main run creates the GitHub release and private GHCR package with only listed permissions; verify existing-package visibility/grants if applicable. No publication was attempted here.
5. Inspect image/config/context for data/credentials and verify labels, digest, platform, absence of moving aliases/deployment. Exercise image failure and failed-job-only retry without another version.

Revisit if actual prior released versions are found, or requirements add other branches, public distribution, other architectures, manual-release consumers, immutable/reproducible artifacts, or atomic release-and-image readiness.

## Open questions

- **GHCR first-write readiness — repository/package administrator; blocks proof, not implementation.** Existing package visibility/grants cannot be inspected with current CLI scope; documented workflow-token path remains unexercised.
- **No-push wording — coordinating session/product owner; blocks activation only if future CI writes are forbidden.** Selected interpretation is no agent push/deploy now, with future artifact publication requested. Do not activate writes under a contrary upstream instruction.
- **Private deployment pull — deployment owner; blocks cluster consumption, not build/publication.** Pull credentials were neither inspected nor provisioned. No registry-secret choice is needed just to implement this pipeline.

## Official references checked

Read 2026-10-25. Legacy GitBook points to new official semantic-release site. Preferred webfetch-mcp crawler was unavailable (connection refused); direct web fetch used instead.

- [Configuration](https://semantic-release.org/usage/configuration/), [JS API](https://semantic-release.org/developer-guide/js-api/), [GitHub Actions recipe](https://semantic-release.org/recipes/ci-configurations/github-actions.md), [Node requirement](https://semantic-release.org/support/node-version/).
- [Commit analyzer](https://github.com/semantic-release/commit-analyzer), [GitHub publisher](https://github.com/semantic-release/github): parsing, permission purposes, side-effect options, and event suppression.
- [GitHub workflow triggering](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow): token event suppression.
- [GHCR](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry): workflow token, repository linkage, private default, source metadata, and digest identity.
- [Packages REST API](https://docs.github.com/en/rest/packages/packages): checked during implementation; documents classic-PAT authentication for metadata endpoints, so existing-package privacy is an administrator activation prerequisite rather than a mandatory workflow REST call.
