# SPDX-FileCopyrightText: 2024 TPT Solutions
# SPDX-License-Identifier: MIT OR Apache-2.0
#
# Build the rust-edge Simulator-mode demo binary. The entrypoint args are passed
# straight to `cargo run`; the default in docker-compose is `--bin tpt-edge`.
# Override with:  docker compose run edge --bin tpt-edge -- --features real

FROM rust:1.74 AS build
WORKDIR /src
COPY . .
RUN cargo build --bin tpt-edge

FROM debian:bookworm-slim
WORKDIR /app
COPY --from=build /src/target/debug/tpt-edge /app/tpt-edge
ENTRYPOINT ["/app/tpt-edge"]
