GATEWAY MODULES

Gateway's public testing scope is Headers and Bitcoin Blocks. The module
catalog also describes retained future capabilities, including Satline.
Presence in that catalog does not make a capability available: release locks
apply to its UI, API, command-line and background entry points.

Additional indexes and Gateway sharing cannot be enabled by installing a
manifest or changing a setting in this release. See docs/INDEXING.md in the
repository for available behavior and docs/GATEWAY-ROADMAP.md for future scope.

External module manifests can be discovered for inspection through
/api/modules. That does not load or execute arbitrary downloaded module code.
