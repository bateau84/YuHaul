# Release automation architecture

- [ADR-016 — Semantic Releases and Release-Gated Container Publication](decisions/adr-016-semantic-release-image-publication.md) — Main-only releases, native first-release baseline, same-run image handoff, GHCR authentication, and security/failure boundaries. Implemented and locally checked, including a Podman Dockerfile build; remote publication remains unverified.

The read-only image verification gate also exercises default non-root startup
with fresh anonymous/named volumes and database restart before release creation.

Changes are recorded in the parent [Architecture Change Log](../log.md).
