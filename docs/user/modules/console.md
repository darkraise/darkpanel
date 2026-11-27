# Console: terminal and files

## Overview

The `console` module gives an admin a root terminal on a host and a file
manager for the host's files, in the browser. It is the one place where
Darkpanel runs a shell for you, so it is fenced more tightly than any other
module:

- It is off on every host until an admin turns it on for that host.
- Every console request needs the `admin` scope on the host. A member never
  sees it.
- Opening a terminal always asks for your authenticator code. Reading a file's
  content and every file change need a session elevated by a code within the
  last 15 minutes.
- API tokens cannot step up, so the console works only from a signed-in
  browser session.
- The agent records every terminal open and close and every file change in the
  audit log. No terminal content is stored anywhere.

On a host where the console is on, whoever controls the controller can reach
root there. If that matters for a host, turn the console and jobs off on the
host itself, as "Turn it off on the host" describes; the controller cannot
turn them back on.

## Before you start

- Your user has a confirmed authenticator. Without one, the step-up answers
  `auth.step_up_unavailable` with "Set up an authenticator to use the console
  and jobs".
- The host's agent runs this release. An older agent shows "Update the agent
  to use the terminal".
- When you open the panel through Nginx Proxy Manager with the
  [panel](panel.md) module, the proxy must pass WebSockets. Exposures made
  before it did show **Terminal needs WebSocket passthrough** on the panel
  page; **Repair** plans the change.
- Turn the module on for the host. In the web UI, the **Console** tab of a
  host where it is off shows **Off on this host** with **Enable** for an
  admin. From the CLI:

```sh
darkpanel module enable --host-id <host-id> --module-id console
```

That runs `core.module.enable`; `darkpanel module disable` turns it off
again and closes every open terminal and transfer of the host.

## Use it in the web UI

The **Console** tab has two parts, **Terminal** and **Files**.

### Terminal

**Terminal** asks for your code ("Opening a terminal needs your authenticator
code"), then opens root's login shell on the host. The status reads
**Waiting for your code**, **Connecting…**, **Connected** or **Closed**. On a
narrow screen, a **Terminal keys** row adds the keys a touch keyboard lacks. When the
terminal closes, it says why, and **Reconnect** opens a new one:

- "Closed after 30 minutes without input".
- "Your sign-in session ended; sign in again to open a terminal".
- "Too many open terminals; close one, then reconnect": a host has at most 4
  open terminals and a user at most 8 (`console.limit`).
- "The agent restarted; open a new terminal".
- "Your access to this host was revoked": for example your user was disabled
  or demoted, or the module was turned off.
- "The shell exited", with its exit code.

The shell runs in its own systemd scope, `darkpanel-console-<id>.scope`, so a
restart of the agent's own unit never kills what you started. An agent stop,
update or crash still ends the terminal exactly as a dropped SSH connection
does: the shell sees a hang-up, foreground programs get SIGHUP, and programs
started with `nohup`, or inside `tmux` or `screen`, keep running. Use one of
those for anything that must outlive the terminal.

The controller pings the browser every 20 seconds and closes a terminal whose
browser has not answered for 60 seconds. The pings also keep proxies such as
Nginx Proxy Manager and Cloudflare tunnels from dropping an idle terminal.
Typing keeps your sign-in session alive, and so does a program that polls the
terminal, because the browser answers its queries as input: an open terminal
running such a program keeps the session alive with nobody typing, up to the
session's 12 hours. An open terminal with nothing running does not.

### Files

**Files** browses the host's file system from `/`, with a breadcrumb of the
folder you are in; the folder is part of the page address, so a link opens it
again. The table shows each entry's **Name**, **Size**, **Mode**, **Owner** and
**Modified**, directories first, at most 2000 entries a page. Each row's
actions menu has **Edit**, **Download**, **Rename**, **Permissions** and
**Delete**; the toolbar has **Upload** and **New folder**.

**Edit** opens a text file of at most 1 MiB that is valid UTF-8 in the
editor. **Save** writes it only when the file has not changed since you opened
it; otherwise it answers `files.conflict` and keeps your text, so you can
reload and merge. A file that is not text answers `files.not_text`; download
it instead. **Unsaved changes** shows while the text differs from what was
last saved.

**Upload** sends a file of at most 512 MiB with a progress bar, and is refused
when the host would keep less than 64 MiB free. A host runs at most 2 uploads
and downloads at once (`console.limit`). Deleting a folder deletes what it
holds, and asks you to type its path exactly.

Some paths are protected whatever you do. Writes, renames and deletes are
refused under `/proc`, `/sys`, `/dev`, `/run`, `/boot/efi`, `/etc/darkpanel`,
`/var/lib/darkpanel` and the Darkpanel binary, and a rename or recursive
delete of a folder that holds one of them, such as `/` or `/etc`, is refused
too. Reads and downloads are refused under `/proc`, `/sys`, `/dev`,
`/etc/darkpanel` and `/var/lib/darkpanel`. The file manager follows no
symbolic link: a path with a symlink in any part is refused, so a symlinked
folder never leads into a protected path, and you open the link's target by
its own path instead.

A write goes to a temporary file `.darkpanel-tmp-<random>` in the same folder,
then replaces the file in one step, keeping its mode and owner. A failed or
cancelled write removes the temporary file, and the agent removes any it
recorded and finds left at its next start.

## Use it from the CLI

The console has no CLI command: the terminal and the file manager are browser
features. Their routes are in the [API reference](../../api/README.md): `POST
/api/v1/auth/step-up`, the terminal ticket and channel under
`/api/v1/hosts/{host}/channels/console/terminal`, and
`/api/v1/hosts/{host}/files/{op}`. The terminal opens only with a single-use
ticket that a fresh code buys, valid 30 seconds and bound to your session and
the host, and only from the panel's own origin.

The audit log lists the console's entries with the other changes:

```sh
darkpanel audit list
```

- `console.terminal.open` (phase `open`) names the user, the host and the
  client address.
- `console.terminal.close` (phase `close`) gives the reason, the duration and
  the bytes in and out. A terminal still open when the agent stopped gets a
  close with reason `agent restarted` at the agent's next start.
- `console.file.<op>`, such as `console.file.write` or `console.file.delete`,
  has an `intent` entry before the first byte is written and a `result` entry
  with the size and SHA-256, or the error, before the answer. A change cut off
  by an agent crash gets the result `error`, with the error `interrupted`.

### Turn it off on the host

The controller judges who may use the console and whether it is on, so a
compromised controller could turn it on and use it. Root on the host can deny
the console and the jobs module in a policy file. Deny both: while jobs is
allowed, a compromised controller can run a root job that rewrites the file.
The file does not cover the users module, which can add a key to root:

- `sudo darkpanel agent policy deny console` denies the console on this host;
  `sudo darkpanel agent policy allow console` stops denying it. Both take
  `console`, `jobs` or both.
- The command writes `/etc/darkpanel/agent-policy.json`, for example
  `{"denyModules":["console","jobs"]}`, and tells the running agent to read it
  again; an agent that is not running reads it when it starts.
- A denied module is reported unavailable with
  "Turned off on this host by its policy file". The agent refuses to turn it on whatever the controller asks,
  and refuses every terminal and file request of it.

## What it changes on the host

- While a terminal is open, a transient systemd scope
  `darkpanel-console-<id>.scope` holding the shell and what it started; systemd
  removes the scope when its last process ends.
- The files you change, and temporary `.darkpanel-tmp-<random>` files while a
  write runs.
- `/etc/darkpanel/agent-policy.json`, when root denies a module on the host.

`sudo darkpanel uninstall` has nothing of this module to remove beyond
`/etc/darkpanel`, which it removes with Darkpanel's data.

## When something goes wrong

| Code | What it means here | What you do |
|---|---|---|
| `auth.step_up_required` | Reading or changing a file needs a session elevated within 15 minutes. | Enter your code when the web UI asks. |
| `auth.step_up_unavailable` | Your user has no confirmed authenticator, or the call came from an API token. | Set up an authenticator, and use a signed-in browser session. |
| `auth.step_up_wait` | "Wait for the next code": the code was already used, for example by a Reconnect within the same 30 seconds. | Wait for the next code. |
| `console.limit` | Too many terminals for the host or for you, or too many transfers on the host. | Close a terminal, or wait for a transfer to end. |
| `files.conflict` | The file changed on the host since you opened it. | Reload it, then apply your edit again. |
| `files.not_text` | The file is larger than 1 MiB or is not valid UTF-8. | Download it instead. |
| `module.unavailable` | The console is off on the host, the host's policy file denies it, or the agent is too old: "Update the agent to use the terminal". | Enable the module, allow it on the host, or update the agent. |
| `request.too_large` | A text write over 1 MiB, or an upload over 512 MiB. | Upload a large file instead of editing it; split a larger one. |
| `auth.forbidden` | You are not an admin on this host. | Ask an admin. |
| `request.invalid_input` | A path is protected or invalid, the host lacks the free space for an upload, or a folder delete got another path typed. | Read the message. |
