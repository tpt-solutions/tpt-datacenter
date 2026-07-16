# api

Core platform API and orchestration layer. The contract schema of record is the
proto3 definition in `proto/tpt/v1/tpt.proto` (generated message types in
`internal/orchestration/pb`), and the services are currently served as
HTTP+JSON (gRPC stubs are not yet implemented). Includes the Hardware
Management API (Redfish/IPMI) for compute server power throttling.

See [todo.md](../todo.md) Phase 7 and Phase 8.
