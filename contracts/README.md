# FinConfig Contracts

This module owns FinConfig's protobuf and OpenAPI sources, generated Go
transport bindings, and MySQL schema compatibility manifest. It contains no
product domain behavior.

During the multi-module migration, the old root `api/`, `gen/`, `kitex_gen/`,
and MySQL schema manifest remain temporary compatibility copies until the root
module switches to a tagged Contracts version. A transitional parity test
prevents these copies from drifting. New contract edits must be made here.
