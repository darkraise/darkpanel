# Host information

The `hostinfo` module shows what a host is: its operating system, kernel,
processor, memory, load and uptime. It reads the host's own files, so it runs
in the agent on every host, the controller's own host included. In the web
interface it is the summary card on the host's page at `/hosts/<id>/hostinfo`.
It is enabled on every host by default and it only reads: it changes nothing.

## Requirements

The host must have a readable `/proc/meminfo`. Without it the module reports
itself unavailable on that host, and the other values would be missing as well.
Every supported operating system has it.

## Actions and resources

The module has one resource and no actions. A resource only reads.

| Name | Command | Kind | Minimum scope | What it does |
|---|---|---|---|---|
| `hostinfo.summary` | `darkpanel hostinfo summary get` | Read | `read` | Shows the host summary described below. |

The command runs on the agent of the host that `--host <host>` names. You can
leave `--host` out when you have exactly one host. With `--json` it prints the
answer as JSON.

## The summary

The summary card shows these values. With `--json`, the answer holds the
fields below under the names in the first column.

| Field | Meaning | Unit | Read from |
|---|---|---|---|
| `hostname` | The host name. | text | The host's own name, as the kernel reports it. |
| `os` | The distribution, with the members `id`, `versionId` and `prettyName`. | text | `/etc/os-release`, from `ID`, `VERSION_ID` and `PRETTY_NAME`. |
| `kernel` | The kernel release. | text | `/proc/sys/kernel/osrelease`. |
| `arch` | The processor architecture, `amd64` or `arm64`. | text | The architecture of the running Darkpanel binary. |
| `uptimeSeconds` | How long the host has been up. | seconds | `/proc/uptime`. |
| `bootTime` | When the host started, in UTC. | RFC 3339 time | The agent's clock minus `uptimeSeconds`, to the second. |
| `cpu` | The processor, with the members `model` and `logicalCount`. | text and count | `/proc/cpuinfo`: the first `model name`, `Hardware` or CPU implementer line, and the number of `processor` entries. |
| `memory` | Physical memory, with the members `totalBytes` and `availableBytes`. | bytes | `/proc/meminfo`, from `MemTotal` and `MemAvailable`. |
| `swap` | Swap space, with the members `totalBytes` and `freeBytes`. | bytes | `/proc/meminfo`, from `SwapTotal` and `SwapFree`. |
| `load` | The load averages, with the members `one`, `five` and `fifteen`. | runnable tasks, averaged | `/proc/loadavg`, the 1, 5 and 15 minute values. |
| `rootFs` | The filesystem mounted at `/`, with the members `totalBytes` and `availableBytes`. | bytes | `statfs` on `/`. The available size is what an ordinary user can still use. |
| `darkpanelVersion` | The version of Darkpanel running on the host. | text | The agent's own build. |

The web card shows the same values in readable units. Swap shows `None` when
the host has none.

## Reading a host the controller cannot reach

When the controller is down or unreachable, ask the local agent instead. Run
this as root on the host:

```sh
sudo darkpanel hostinfo summary get --local
```

`--local` sends the command to the agent's socket on this host, so it works
without the controller and without the network.

## Troubleshooting

The module defines no error codes of its own.

- If the card shows an error, the agent could not read one of the files above
  or run `statfs`. The message names the file. Check that `/proc` is mounted
  and readable by the agent, which runs as root.
- If the controller shows no summary for a host, run
  `sudo darkpanel hostinfo summary get --local` on that host. An answer there
  means the agent reads the host correctly, so look at its link to the
  controller.
