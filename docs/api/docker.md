# docker API

Back to the [API reference](README.md).

## docker.compose.down v1

Stop and remove the containers of a Compose project.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.compose.down/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.compose.down/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `profiles` | array of string | no |  |
| `project` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `containers` | array of object | yes |  |
| `containers[].id` | string | yes |  |
| `containers[].name` | string | yes |  |
| `containers[].running` | boolean | yes |  |
| `project` | string | yes |  |

## docker.compose.pull v1

Pull the images of a Compose project.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.compose.pull/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.compose.pull/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `profiles` | array of string | no |  |
| `project` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `containers` | array of object | yes |  |
| `containers[].id` | string | yes |  |
| `containers[].name` | string | yes |  |
| `containers[].running` | boolean | yes |  |
| `project` | string | yes |  |

## docker.compose.restart v1

Restart the containers of a Compose project.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.compose.restart/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.compose.restart/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `profiles` | array of string | no |  |
| `project` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `containers` | array of object | yes |  |
| `containers[].id` | string | yes |  |
| `containers[].name` | string | yes |  |
| `containers[].running` | boolean | yes |  |
| `project` | string | yes |  |

## docker.compose.up v1

Create or recreate the containers of a Compose project.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.compose.up/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.compose.up/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `profiles` | array of string | no |  |
| `project` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `containers` | array of object | yes |  |
| `containers[].id` | string | yes |  |
| `containers[].name` | string | yes |  |
| `containers[].running` | boolean | yes |  |
| `project` | string | yes |  |

## docker.container.inspect v1

Show a container's configuration and state.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/docker.container.inspect/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `container` | string | yes |  |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `cmd` | array of string | no |  |
| `created` | string (date-time) | yes |  |
| `entrypoint` | array of string | no |  |
| `env` | array of object | yes |  |
| `env[].name` | string | yes |  |
| `env[].value` | string | yes |  |
| `exitCode` | integer | yes |  |
| `finishedAt` | string | no |  |
| `id` | string | yes |  |
| `image` | string | yes |  |
| `imageId` | string | yes |  |
| `labels` | map of string | no |  |
| `mounts` | array of object | yes |  |
| `mounts[].destination` | string | yes |  |
| `mounts[].name` | string | no |  |
| `mounts[].rw` | boolean | yes |  |
| `mounts[].source` | string | yes |  |
| `mounts[].type` | string | yes |  |
| `name` | string | yes |  |
| `networks` | array of object | yes |  |
| `networks[].aliases` | array of string | no |  |
| `networks[].container` | string | yes |  |
| `networks[].endpointId` | string | no |  |
| `networks[].ipv4` | string | no |  |
| `networks[].ipv6` | string | no |  |
| `networks[].network` | string | yes |  |
| `networks[].networkId` | string | yes |  |
| `owner` | string | no |  |
| `ports` | array of object | yes |  |
| `ports[].ip` | string | no |  |
| `ports[].privatePort` | integer | yes |  |
| `ports[].proto` | string | yes |  |
| `ports[].publicPort` | integer | no |  |
| `project` | string | no |  |
| `restartPolicy` | string | no |  |
| `running` | boolean | yes |  |
| `service` | string | no |  |
| `startedAt` | string | no |  |
| `state` | string | yes |  |
| `user` | string | no |  |
| `workingDir` | string | no |  |

## docker.container.logs v1

Read the last lines of a container's logs.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/docker.container.logs/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `container` | string | yes |  |
| `since` | string | no |  |
| `tail` | integer | no |  |
| `timestamps` | boolean | no |  |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `lines` | array of object | yes |  |
| `lines[].stream` | string | yes |  |
| `lines[].text` | string | yes |  |

## docker.container.remove v1

Remove a container.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.container.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.container.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `container` | string | yes |  |
| `force` | boolean | no |  |
| `volumes` | boolean | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |

## docker.container.restart v1

Restart a container.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.container.restart/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.container.restart/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `container` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |
| `running` | boolean | yes |  |

## docker.container.reveal-env v1

Reveal a container's environment values.

| Operation | Request |
|---|---|
| reveal | `POST /api/v1/hosts/{host}/actions/docker.container.reveal-env/v1/reveal` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `container` | string | yes |  |

### Output

Returned in the `value` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `env` | array of object | yes | Secret. |
| `env[].name` | string | yes |  |
| `env[].value` | string | yes |  |

## docker.container.start v1

Start a container.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.container.start/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.container.start/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `container` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |
| `running` | boolean | yes |  |

## docker.container.stop v1

Stop a container.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.container.stop/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.container.stop/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `container` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |
| `running` | boolean | yes |  |

## docker.containers

Read docker.containers

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/docker/containers` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `containers` | array of object | yes |  |
| `containers[].created` | string (date-time) | yes |  |
| `containers[].id` | string | yes |  |
| `containers[].image` | string | yes |  |
| `containers[].imageId` | string | yes |  |
| `containers[].labels` | map of string | no |  |
| `containers[].name` | string | yes |  |
| `containers[].owner` | string | no |  |
| `containers[].ports` | array of object | yes |  |
| `containers[].ports[].ip` | string | no |  |
| `containers[].ports[].privatePort` | integer | yes |  |
| `containers[].ports[].proto` | string | yes |  |
| `containers[].ports[].publicPort` | integer | no |  |
| `containers[].project` | string | no |  |
| `containers[].service` | string | no |  |
| `containers[].state` | string | yes |  |
| `containers[].status` | string | yes |  |
| `containers[].usage` | object or null | no |  |

## docker.engine

Read docker.engine

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/docker/engine` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `apiVersion` | string | no |  |
| `arch` | string | no |  |
| `available` | boolean | yes |  |
| `containers` | integer | yes |  |
| `images` | integer | yes |  |
| `os` | string | no |  |
| `reason` | string | no |  |
| `running` | integer | yes |  |
| `version` | string | no |  |

## docker.image.prune v1

Remove the dangling images no container uses.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.image.prune/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.image.prune/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `images` | array of object | yes |  |
| `images[].id` | string | yes |  |
| `images[].size` | integer | yes |  |
| `images[].tags` | array of string | yes |  |
| `reclaimed` | integer | yes |  |

## docker.image.pull v1

Pull an image from its registry.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.image.pull/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.image.pull/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `image` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `digests` | array of string | yes |  |
| `id` | string | yes |  |
| `image` | string | yes |  |

## docker.image.remove v1

Remove an image or one of its tags.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.image.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.image.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `force` | boolean | no |  |
| `image` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `deleted` | array of string | yes |  |
| `untagged` | array of string | yes |  |

## docker.images

Read docker.images

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/docker/images` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `images` | array of object | yes |  |
| `images[].containers` | array of string | yes |  |
| `images[].created` | string (date-time) | yes |  |
| `images[].dangling` | boolean | yes |  |
| `images[].digests` | array of string | yes |  |
| `images[].id` | string | yes |  |
| `images[].size` | integer | yes |  |
| `images[].tags` | array of string | yes |  |

## docker.network.connect v1

Connect a container to a network.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.network.connect/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.network.connect/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `aliases` | array of string | no |  |
| `container` | string | yes |  |
| `ipv4` | string | no |  |
| `ipv6` | string | no |  |
| `network` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `aliases` | array of string | no |  |
| `container` | string | yes |  |
| `endpointId` | string | no |  |
| `ipv4` | string | no |  |
| `ipv6` | string | no |  |
| `network` | string | yes |  |
| `networkId` | string | yes |  |

## docker.network.create v1

Create a network.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.network.create/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.network.create/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `attachable` | boolean | no |  |
| `driver` | string | no |  |
| `gateway` | string | no |  |
| `internal` | boolean | no |  |
| `ipv6` | boolean | no |  |
| `labels` | map of string | no | Secret. |
| `name` | string | yes |  |
| `subnet` | string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |

## docker.network.disconnect v1

Disconnect a container from a network.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.network.disconnect/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.network.disconnect/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `container` | string | yes |  |
| `force` | boolean | no |  |
| `network` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `aliases` | array of string | no |  |
| `container` | string | yes |  |
| `endpointId` | string | no |  |
| `ipv4` | string | no |  |
| `ipv6` | string | no |  |
| `network` | string | yes |  |
| `networkId` | string | yes |  |

## docker.network.remove v1

Remove a network.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.network.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.network.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `network` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |

## docker.networks

Read docker.networks

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/docker/networks` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `networks` | array of object | yes |  |
| `networks[].containers` | array of string | yes |  |
| `networks[].created` | string (date-time) | yes |  |
| `networks[].driver` | string | yes |  |
| `networks[].id` | string | yes |  |
| `networks[].internal` | boolean | yes |  |
| `networks[].ipv6` | boolean | yes |  |
| `networks[].name` | string | yes |  |
| `networks[].owner` | string | no |  |
| `networks[].scope` | string | yes |  |
| `networks[].subnets` | array of string | yes |  |

## docker.projects

Read docker.projects

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/docker/projects` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `projects` | array of object | yes |  |
| `projects[].configHash` | string | yes |  |
| `projects[].containers` | array of string | yes |  |
| `projects[].directory` | string | yes |  |
| `projects[].envFiles` | array of string | yes |  |
| `projects[].files` | array of string | yes |  |
| `projects[].firstSeen` | string (date-time) | yes |  |
| `projects[].hashChanged` | boolean | yes |  |
| `projects[].lastRun` | string (date-time) or null | no |  |
| `projects[].name` | string | yes |  |
| `projects[].profiles` | array of string | yes |  |
| `projects[].running` | integer | yes |  |

## docker.volume.create v1

Create a volume.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.volume.create/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.volume.create/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `driver` | string | no |  |
| `labels` | map of string | no | Secret. |
| `name` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |

## docker.volume.remove v1

Remove a volume and the data in it.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/docker.volume.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/docker.volume.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `volume` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes |  |
| `name` | string | yes |  |

## docker.volumes

Read docker.volumes

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/docker/volumes` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `volumes` | array of object | yes |  |
| `volumes[].containers` | array of string | yes |  |
| `volumes[].created` | string (date-time) | yes |  |
| `volumes[].driver` | string | yes |  |
| `volumes[].labels` | map of string | no |  |
| `volumes[].mountpoint` | string | yes |  |
| `volumes[].name` | string | yes |  |
| `volumes[].owner` | string | no |  |
