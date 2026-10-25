## Go navigation

Prefer `gopls` for semantic Go code lookup: definitions, references, implementations, symbols, and type information.

Use `rg`/`grep` only for literal text search or when `gopls` cannot answer the query.

## Documentation

Repository documentation follows the Open Knowledge Format (OKF) and is indexed
through `okf-mcp`.

### Navigation

Use `okf-mcp` as the primary way to discover relevant documentation and its
relationships. Follow the graph to establish the context required for the task
before performing broad documentation reads. Source documents remain
authoritative; OKF is the discovery, navigation, and relationship layer.

### Update obligation

**Any change that affects documented behavior, decisions, contracts, plans, or
relationships also requires the affected documentation to be updated. You do
not have to be told separately.**

When creating, modifying, moving, superseding, or deleting documentation:

1. Keep its OKF metadata and relationships accurate.
2. Update affected related documents when their relationship or content has
   changed.
3. Update the documentation log/index required by the repository.
4. Perform these updates in the same task/wave as the change that caused them.

Documentation work is not complete while the source documentation and its OKF
representation disagree.

If `okf-mcp` is incomplete or inconsistent with an authoritative source, use
the source and correct or report the OKF defect.
