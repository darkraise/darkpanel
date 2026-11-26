# Scheduled jobs

## Overview

The `jobs` module runs a command on a schedule, as root or as a login user.
Each job is a script and a pair of systemd units that Darkpanel writes, a
service that runs the script and a timer that starts the service, so its runs,
results and output are systemd's: the output is the service's journal. The
module also lists the host's other timers and its cron entries, read-only.

A job's command is a shell script that runs with the rights of its user, so
the module is fenced like the [console](console.md):

- It is off on every host until an admin turns it on for that host.
- Every job action needs the `admin` scope. A viewer sees the list without
  each job's command, which can hold secrets.
- Creating and changing a job need a session elevated by your authenticator
  code within the last 15 minutes; the web UI asks for the code, and the API
  answers `auth.step_up_required` until you give it. API tokens cannot step
  up. Deleting, enabling, disabling and running a job act on a command that
  already passed that check.

## Before you start

- Turn the module on for the host. In the web UI, the **Jobs** tab of a host
  where it is off shows **Off on this host** with **Enable** for an admin.
  From the CLI, this runs `core.module.enable`:

```sh
darkpanel module enable --host-id <host-id> --module-id jobs
```

- A job runs as root or as a login user: an account with a UID from 1000 to
  59999 whose shell `/etc/shells` lists and is not a no-login shell.
  [Users and SSH](users.md) creates one.
- Root on the host can deny the module whatever the controller asks, with
  `sudo darkpanel agent policy deny jobs`; the console guide's "Turn it off
  on the host" describes the policy file.

## Use it in the web UI

The **Jobs** tab lists the **Darkpanel jobs** with their **Name**,
**Schedule** in words, such as "Every day at 03:00", **User**, **Last
result**, such as "Failed (exit 2)", and **Next run**, or **Disabled**. With
no job yet it shows **No Darkpanel jobs yet** and **Add job**.

**Add job** opens the form: **Name**, **Description**, **Schedule**, **User**,
**Command**, **Timeout (seconds)**, **Catch up a run missed while the host was
off** and **Random delay (seconds)**. As you type a schedule, the form shows
its **Next runs**, or the error systemd gives for it. **Review change** shows
the plan.

Each job's actions menu has **Run now**, which starts it and shows
**Started**, **View output**, which opens the journal of its service, **Edit**,
**Enable** or **Disable**, and **Delete**.

**Other scheduled tasks** lists the host's other timers and cron entries, each
with its **Source**, such as `/etc/cron.d/certbot`. Darkpanel never changes
them.

## Use it from the CLI

Commands reach the controller with an API token, which [Create the first
admin](../first-admin.md#next-an-api-token-for-the-cli) explains how to
create. Each command below is the generated command of one action, and the
[jobs API reference](../../api/jobs.md) lists its input and output.
`--host <id or name>` picks the host. Create and update need a step-up, which
an API token cannot give, so the CLI can read, delete, enable, disable and run
jobs, while creating and changing them happen in the web UI.

| Action | Command | What it does | Asks you |
|---|---|---|---|
| `jobs.job.list` | `darkpanel jobs job list` | Lists the Darkpanel jobs with their last start, result, exit status and next run, and the host's other timers and cron entries. | Nothing; it only reads. |
| `jobs.schedule.check` | `darkpanel jobs schedule check` | Checks a calendar expression and shows its next three runs. | Nothing; it only reads. |
| `jobs.job.create` | `darkpanel jobs job create` | Creates a job: its script, its service and its timer. | A step-up code. Undo with `change undo`, which deletes it. |
| `jobs.job.update` | `darkpanel jobs job update` | Changes a job's schedule, user, command or limits. | A step-up code. Undo with `change undo`, which puts the previous job back. |
| `jobs.job.delete` | `darkpanel jobs job delete` | Stops a job's timer and removes its files. | Nothing. Undo with `change undo`. |
| `jobs.job.enable` | `darkpanel jobs job enable` | Starts a job's timer and starts it at boot. | Nothing. Undo with `change undo`. |
| `jobs.job.disable` | `darkpanel jobs job disable` | Stops a job's timer and keeps it off at boot. | Nothing. Undo with `change undo`. |
| `jobs.job.run` | `darkpanel jobs job run` | Runs a job now. | Nothing; a run cannot be undone. |

```sh
darkpanel jobs job list --host web-1
darkpanel jobs schedule check --host web-1 --schedule "Mon..Fri 02:30"
darkpanel jobs job run --host web-1 --slug db-backup
darkpanel jobs job disable --host web-1 --slug db-backup
darkpanel services journal query --host web-1 --unit darkpanel-job-db-backup.service
```

### Fields

- **Name** (`slug`): lower-case letters, digits and hyphens, at most 40,
  starting with a letter or digit.
- **Schedule**: a systemd calendar expression, such as `daily` or
  `*-*-* 03:00:00`, checked with `systemd-analyze calendar`.
- **User**: root or a login user, as above.
- **Command**: 1 byte to 16 KiB of shell, run by `/bin/sh`.
- **Timeout**: 1 minute to 24 hours, 1 hour by default; a run that takes
  longer is stopped.
- **Catch up**: run once at boot when a scheduled run was missed while the
  host was off.
- **Random delay**: delay each run by up to this many seconds, at most 1 hour.

### Turning the module off

When the module is turned off on a host, the agent stops every job timer and
remembers which were active; turning it on again starts those again, and an
agent that starts with the module off stops them. Off on a host means no
Darkpanel job runs there.

## What it changes on the host

For a job named `<slug>`:

- `/etc/systemd/system/darkpanel-job-<slug>.service`: `Type=oneshot`, `User=`,
  `TimeoutStartSec=` and `ExecStart=/bin/sh /var/lib/darkpanel/jobs/<slug>.sh`.
- `/etc/systemd/system/darkpanel-job-<slug>.timer`: `OnCalendar=`,
  `Persistent=` (catch up) and `RandomizedDelaySec=`.
- `/var/lib/darkpanel/jobs/<slug>.sh`, the command, mode 0750, owned by root
  with the group of the user's primary group, in `/var/lib/darkpanel/jobs`,
  which is root-owned with mode 0755 so a non-root user can open its script.

Darkpanel records each unit file in the host's ownership store when it writes
it, and removes the record when it deletes the file. `sudo darkpanel uninstall`
stops and disables every recorded timer, then removes the units, the scripts
and the directory.

Darkpanel reads, but never changes, `/etc/crontab`, `/etc/cron.d`,
`/var/spool/cron/crontabs` and the host's other timers.

## When something goes wrong

| Code | What it means here | What you do |
|---|---|---|
| `auth.step_up_required` | Create and update need a session elevated within 15 minutes. | Enter your code when the web UI asks. |
| `auth.step_up_unavailable` | Your user has no confirmed authenticator, or the call came from an API token. | Use a signed-in browser session of a user with an authenticator. |
| `module.unavailable` | The module is off on the host, or the host's policy file denies it. | Enable the module, or allow it on the host. |
| `request.invalid_input` | An input is invalid: a name that is taken or malformed, a schedule systemd cannot read (its message says why), a user that is not root or a login user, or a value out of range. | Read the message and correct the input. |
