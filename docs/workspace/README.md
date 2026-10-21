# Workspace architecture and dashboard workflows

Snapshots of the shared Qwish workspace documentation, included here because the workspace parent is not a Git repository. Source paths in inline code refer to that multi-repository workspace. Linked implementation files resolve to this backend or the corresponding GitHub repository.

- [Entity map](QWISH_ENTITY_MAP.md)
- [Platform grouping](QWISH_PLATFORM_MAP.mmd)
- [Dashboard implementation and validation](DASHBOARD_WORKFLOWS.md)
- [Product context](PRODUCT.md)
- [Architecture](ARCHITECTURE.MD)

The browser regression runner is checked in under `qwish-teacher-panel/scripts/dashboard-workflows.browser.cjs`. Run it from that repository with `node scripts/dashboard-workflows.browser.cjs`; it accepts the environment overrides described in the implementation notes.
