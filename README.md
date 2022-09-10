# Caddy Mesh

[Caddy][1] service mesh based on [the host/node architecture][2].


![architecture](docs/architecture.png)


## Features

- [x] [Timeouts](#timeouts)
- [x] [Retries](#retries)
- [ ] Circuit Breaking
- [x] [Rate Limiting](#rate-limiting)
- [x] [Traffic Splitting](#traffic-splitting)


## Installation

### Prerequisites

Build the image for Caddy Mesh controller:

```console
$ make build-image tag=v0.1.0
```

If using any plugins, you need to build the Caddy image locally:

```console
$ make build-caddy-image tag=2.6.0-beta.3-custom
```

### Install the Helm Chart

```console
$ make helm-install
```


## Configuration

All features provided by Caddy Mesh can be enabled by using [annotations][3] on Kubernetes services.

### Timeouts

Timeouts can be enabled by using the following annotations:

```
mesh.caddyserver.com/timeout-dial-timeout: "<duration>"
mesh.caddyserver.com/timeout-read-timeout: "<duration>"
mesh.caddyserver.com/timeout-write-timeout: "<duration>"
```

Parameters:

- `timeout-dial-timeout`: How long to wait before timing out trying to connect to an upstream. Default: `3s`. (See [dial_timeout](https://caddyserver.com/docs/json/apps/http/servers/routes/handle/reverse_proxy/transport/http/dial_timeout/).)
- `timeout-read-timeout`: The maximum time to wait for next read from backend. Default: no timeout. (Requires Caddy [v2.6.0-beta.3](https://github.com/caddyserver/caddy/releases/tag/v2.6.0-beta.3).)
- `timeout-write-timeout`: The maximum time to wait for next write to backend. Default: no timeout. (Requires Caddy [v2.6.0-beta.3](https://github.com/caddyserver/caddy/releases/tag/v2.6.0-beta.3).)

### Retries

Retries can be enabled by using the following annotations:

```
mesh.caddyserver.com/retry-count: "<count>"
mesh.caddyserver.com/retry-duration: "<duration>"
mesh.caddyserver.com/retry-on: "<expression>"
```

Parameters:

- `retry-count`: How many times to retry selecting available backends for each request if the next available host is down. Default: disabled. (Requires Caddy [v2.6.0-beta.3](https://github.com/caddyserver/caddy/releases/tag/v2.6.0-beta.3).)
    + If `retry-duration` is also configured, then retries may stop early if the duration is reached.
- `retry-duration`: How long to try selecting available backends for each request if the next available host is down. Default: disabled. (See [try_duration](https://caddyserver.com/docs/json/apps/http/servers/routes/handle/reverse_proxy/load_balancing/try_duration/).)
- `retry-on`: An [expression](https://caddyserver.com/docs/caddyfile/matchers#expression) matcher that restricts with which requests retries are allowed. Default: `""`. (See [retry_match](https://caddyserver.com/docs/json/apps/http/servers/routes/handle/reverse_proxy/load_balancing/retry_match/).)
    + If either `retry-count` or `retry-duration` is specified, `retry-on` will default to `"true"`.

### Rate Limiting

Rate limiting can be enabled by using the following annotations:

```
mesh.caddyserver.com/rate-limit-key: "<key>"
mesh.caddyserver.com/rate-limit-rate: "<rate>"
mesh.caddyserver.com/rate-limit-zone-size: "<zone_size>"
```

Note that this feature requires the [caddy-ext/ratelimit](https://github.com/RussellLuo/caddy-ext/tree/master/ratelimit) plugin.

### Traffic Splitting

Traffic splitting can be enabled by using the following annotations:

```
mesh.caddyserver.com/traffic-split-expression: "<expression>"
mesh.caddyserver.com/traffic-split-new-service: "<name>"
mesh.caddyserver.com/traffic-split-old-service: "<name>"
```

Parameters:

- `traffic-split-expression`: An [expression](https://caddyserver.com/docs/caddyfile/matchers#expression) matcher that restricts with which requests will be redirected to the new service (or, if unmatched, to the old service). Default: `""`.
- `traffic-split-new-service`: The name of the new Kubernetes Service. Default: `""`.
- `traffic-split-old-service`: The name of the old Kubernetes Service. Default: `""`.

#### Workflow

(This workflow is inspired by [SMI TrafficSplit][3].)

In this example workflow, the user has previously created the following resources:

- Deployment named `server-v1`, with labels: `app: server` and `version: v1`.
- Service named `server`, with a selector of `app: server`.
- Service named `server-v1`, with selectors: `app: server` and `version: v1`.
- Clients use the FQDN of `server` to communicate.
    + To leverage Caddy Mesh, clients must use `server.test.caddy.mesh` (instead of `server.test.svc.cluster.local`).

In order to update an application, the user will perform the following actions:

- Enable Traffic splitting on `server` (without redirecting traffic to `server-v2`).

    ```diff
    ---
    kind: Service
    apiVersion: v1
    metadata:
      name: server
      namespace: test
      labels:
        app: server
    + annotations:
    +   mesh.caddyserver.com/traffic-split-expression: "false"
    +   mesh.caddyserver.com/traffic-split-new-service: server-v2
    +   mesh.caddyserver.com/traffic-split-old-service: server-v1
    spec:
      ...
    ```
  
- Create a new deployment named `server-v2`, with labels: `app: server` and `version: v2`.
- Create a new service named `server-v2`, with selectors: `app: server` and `version: v2`.
- Once the deployment is healthy, spot check by sending manual requests to the `server-v2`.

When ready, the user begins to redirect traffic to `server-v2`:

- For example, the user first route Chrome consumers to `server-v2`:

    ```diff
    ---
    kind: Service
    apiVersion: v1
    metadata:
      name: server
      namespace: test
      labels:
        app: server
      annotations:
    -   mesh.caddyserver.com/traffic-split-expression: "false"
    +   mesh.caddyserver.com/traffic-split-expression: "header({'User-Agent': '*Chrome*'})"
        mesh.caddyserver.com/traffic-split-new-service: server-v2
        mesh.caddyserver.com/traffic-split-old-service: server-v1
    spec:
      ...
    ```
  
- Verify health metrics and become comfortable with the new version.
- The user decides to redirect all traffic to the new version:

    ```diff
    ---
    kind: Service
    apiVersion: v1
    metadata:
      name: server
      namespace: test
      labels:
        app: server
      annotations:
    -   mesh.caddyserver.com/traffic-split-expression: "header({'User-Agent': '*Chrome*'})"
    +   mesh.caddyserver.com/traffic-split-expression: "true"
        mesh.caddyserver.com/traffic-split-new-service: server-v2
        mesh.caddyserver.com/traffic-split-old-service: server-v1
    spec:
      ...
    ```

When completed, cleanup the old resources:

- Delete the old `server-v1` deployment.
- Delete the old `server-v1` service.
- Remove the Traffic splitting annotations as it is no longer needed.


[1]: https://caddyserver.com/
[2]: https://traefik.io/glossary/service-mesh-101/
[3]: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/
[4]: https://github.com/servicemeshinterface/smi-spec/blob/main/apis/traffic-split/v1alpha4/traffic-split.md#workflow
