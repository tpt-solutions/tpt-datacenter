# SPDX-FileCopyrightText: 2024 TPT Solutions
# SPDX-License-Identifier: MIT OR Apache-2.0
#
# Multi-stage build for any Go service in this monorepo workspace.
# Build with:  docker build -f deploy/docker/go.Dockerfile \
#               --build-arg SERVICE=api/cmd/control .
# The built binary is placed at /app/bin/server.

FROM golang:1.23 AS build
WORKDIR /src

# Cache module downloads first.
COPY go.work ./go.work
COPY api/go.mod api/go.sum ./api/
COPY dashboard/go.mod ./dashboard/
COPY go-telemetry/go.mod ./go-telemetry/
RUN cd api && go mod download

# Build the requested service via the workspace (go.work).
ARG SERVICE
COPY . .
RUN cd api && go build -o /app/bin/server ./../${SERVICE}

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /app/bin/server /app/server
# Topology/control services seed from this spec.
COPY deploy/topology/facility.json /app/deploy/topology/facility.json
# Dashboard SPA assets (the dashboard service sets -static to this dir).
COPY dashboard/static /app/dashboard/static
EXPOSE 8080 8081 8082 8083 8084 8085
ENTRYPOINT ["/app/server"]
