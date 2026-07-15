module github.com/TPT-Solutions/tpt-datacenter/go-telemetry

go 1.22

// Phase 3 telemetry ingestion engine. QuestDB is written natively over the
// InfluxDB Line Protocol (ILP) on TCP port 9009, so no third-party client
// dependency is required. A future move to the official QuestDB Go client
// would be added here.
