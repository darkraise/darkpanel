#!/bin/bash
# Captures `docker compose config --format json` for each scenario as the
# golden files of the compose package's parser tests.
#
# Usage: capture.sh <scratch dir>
# <scratch dir> must be an empty directory inside the caller's TMPDIR; the
# scenario projects are written there and the JSON next to this script. The
# only docker command it runs is `docker compose ... config`, which reads
# files and talks to no daemon. Project paths in the output are rewritten to
# /srv/compose/<scenario> so the files do not depend on the capturing host.
set -euo pipefail

if [ $# -ne 1 ]; then
	echo "usage: $0 <scratch dir>" >&2
	exit 2
fi
scratch=$(cd "$1" && pwd)
out=$(cd "$(dirname "$0")" && pwd)
if [ -n "$(ls -A "$scratch")" ]; then
	echo "capture.sh: $scratch is not empty" >&2
	exit 2
fi
mkdir "$scratch/docker-config"
export COMPOSE_EXPERIMENTAL_GIT_REMOTE=false COMPOSE_EXPERIMENTAL_OCI_REMOTE=false DOCKER_CONFIG="$scratch/docker-config"

# capture <scenario> [extra compose flags]: runs config on <scratch>/<scenario>/compose.yaml, whose project name is <scenario>.
capture() {
	local name=$1
	shift
	local dir=$scratch/$name
	docker compose --project-directory "$dir" -p "$name" -f "$dir/compose.yaml" "$@" config --format json |
		sed "s#$dir#/srv/compose/$name#g" >"$out/$name.json"
}

mkdir -p "$scratch/simple/conf"
cat >"$scratch/simple/compose.yaml" <<'YAML'
services:
  web:
    image: nginx:1.27
    container_name: simple-web
    labels:
      com.example.tier: front
    ports:
      - "8080:80"
    networks: [front, back]
    volumes:
      - data:/var/lib/data
      - ./conf:/etc/conf:ro
    environment:
      MODE: prod
  db:
    image: postgres:16
    networks: [back]
networks:
  front: {}
  back:
    internal: true
    labels:
      com.example.net: back
volumes:
  data:
    labels:
      com.example.vol: data
YAML
capture simple

mkdir -p "$scratch/build/app"
echo 'FROM busybox' >"$scratch/build/app/Dockerfile"
cat >"$scratch/build/compose.yaml" <<'YAML'
services:
  app:
    build: ./app
  tagged:
    build: ./app
    image: example/tagged:1
  plain:
    image: busybox:1.36
YAML
capture build

mkdir -p "$scratch/provider"
cat >"$scratch/provider/compose.yaml" <<'YAML'
services:
  web:
    image: nginx:1.27
  ext:
    provider:
      type: awesomecloud
      options:
        type: mysql
YAML
capture provider

mkdir -p "$scratch/profiles"
cat >"$scratch/profiles/compose.yaml" <<'YAML'
services:
  web:
    image: nginx:1.27
  debug:
    image: busybox:1.36
    profiles: [debug]
YAML
capture profiles --profile debug

mkdir -p "$scratch/external"
cat >"$scratch/external/compose.yaml" <<'YAML'
x-note: kept
services:
  web:
    image: nginx:1.27
    network_mode: host
    configs: [cfg]
    secrets: [sec]
    volumes:
      - ext:/data
      - type: volume
        source: other
        target: /other
      - type: bind
        source: /srv/data
        target: /srv
      - type: tmpfs
        target: /tmp
  api:
    image: nginx:1.27
    networks: [shared]
    labels:
      - "a=b"
      - "c"
    volumes:
      - plain:/plain
networks:
  shared:
    external: true
    name: my-shared-net
volumes:
  ext:
    external: true
  other:
    external: true
    name: real-other
  plain: {}
configs:
  cfg:
    content: hello
    labels: {k: v}
secrets:
  sec:
    environment: HOME
YAML
capture external

mkdir -p "$scratch/include/part"
cat >"$scratch/include/compose.yaml" <<'YAML'
include:
  - path: ./part/compose.yaml
services:
  main:
    image: nginx:1.27
YAML
cat >"$scratch/include/part/compose.yaml" <<'YAML'
services:
  included:
    image: busybox:1.36
YAML
capture include
