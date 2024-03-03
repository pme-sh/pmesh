<p align="center">
  <img src="logos/pmesh_title.png" width="248" alt="pme.sh">
</p>
<p align="center">
   <a href="https://github.com/pme-sh/pmesh/actions/workflows/build.yml">
      <img src="https://github.com/pme-sh/pmesh/actions/workflows/build.yml/badge.svg" alt="Build status">
   </a>
   <a href="https://github.com/pme-sh/pmesh/actions/workflows/release.yml">
      <img src="https://github.com/pme-sh/pmesh/actions/workflows/release.yml/badge.svg" alt="Release status">
   </a>
</p>

[pmesh](https://pme.sh) is an all-in-one service manager, reverse proxy, and enterprise service bus. It is designed to be a simple and powerful all-in-one replacement for a wide variety of tools commonly deployed in web services. It is currently in alpha and under active development.

## Features

The main objective of pmesh is to provide feature parity with all of the following tools, while being simpler and more powerful, with a single binary and a single configuration file:

- **Reverse proxies (nginx, traefik, haproxy, ...)**: pmesh can act as a reverse proxy for your services, providing SSL termination, routing, rate-limiting, load balancing and more at very high speeds (200k+ req/s on a 12-core server). It also provides many additional features such as automatic GeoIP identification, automatic TLS certificate issuance, builtin URL signing, publishing webhooks as messages across the bus, and more.

- **Service managers (systemd, pm2, ...)**: pmesh can manage your services, automatically restarting them if they crash, scaling them up and down based on load, and providing a simple and powerful API for managing and observing them, rolling updates, checking health, and more. It can also seamlessly hot-swap services with zero downtime and features builtin support for npm, pnpm, yarn, go, flask, and many other languages and frameworks enabling single-line configuration of services, allowing you to focus on writing code, not managing services.

- **Service discovery (consul, etcd, ...)**: pmesh can automatically discover and register your services, and route traffic to them based on their health and other factors. It can also automatically configure your services to talk to each other, providing simple APIs such as Lambda functions.

- **ESB/Message Queues (RabbitMQ, Kafka, ...)**: pmesh can provide a simple and powerful message bus for your services to communicate with each other, including pub/sub, request/reply, and more. It embeds a fork of the NATS.io server, which is designed for high performance and low latency, with additional features such as automatic setup of clusters and superclusters, node discovery, and more.

- **KV and Object Storage**: With the embedded JetStream server, pmesh can provide a simple and powerful key-value store and object storage for your services, including automatic replication, sharding, and many other features.

- **Topology management**: pmesh instances can be spread across multiple machines, automatically discovering each other and forming a cluster, optimizing routes based on region detection, all with a simple `pmesh join pmtp://...` command. No impossible to maintain configuration of servers, no manual assignment of routes and regions, no need to restart any service. Automatically manage `/etc/hosts`, transparently issue TLS certificates, and authenticate mutually with client-certificates.

- **Distributed logging**: pmesh can capture logs from your services and provides a simple and powerful API for querying and observing them, including tailing logs, searching logs, and more, across all your services, all your machines, and all your sessions. It will also assign a unique ID to each request that can be used to precisely identify the logs associated with a request.

## Usage

```
Usage:
  pmesh [command]

Log Queries:
  raytrace    Find logs by ray ID
  tail        Tail logs

Service Controls:
  ls          List services
  rebuild     Invalidates build cache and restarts service
  restart     Restart service
  stop        Stops service
  view        Show service details

Daemon Commands:
  get         Get the pmesh node configuration
  get-seed    Get the seed URL
  go          Start the pmesh node, optionally with a manifest file
  preview     Previews the rendered manifest
  set         Set the pmesh node configuration
  setup       Run the setup utility

Additional Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command

Flags:
  -B, --bind string             Bind address for public connections (default "0.0.0.0")
  -C, --cwd string              Sets the working directory before running the command
  -D, --dumb                    Disable interactive prompts and complex ui
  -E, --env string              Environment name, used for running multiple instances of pmesh
  -h, --help                    help for pmesh
  -H, --http int                Listen port for public HTTP (default 80)
  -S, --https int               Listen port for public HTTPS (default 443)
      --internal-port int       Internal port (default 8443)
  -L, --local-bind string       Bind address for local connections (default "127.0.0.1")
      --subnet-dialer string    Dialer subnet (default "127.2.0.0/16")
      --subnet-service string   Service subnet (default "127.1.0.0/16")
  -R, --url string              Specifies the node URL for the command if relevant
  -V, --verbose                 Enable verbose logging

Use "pmesh [command] --help" for more information about a command.
```
