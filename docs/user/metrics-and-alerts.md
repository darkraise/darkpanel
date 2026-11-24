# Metrics and alerts

Every host records its CPU, memory, swap, load, disk use per mounted
filesystem and network traffic per interface. The host's page shows them as
charts, and alert rules turn them into notifications.

## Metrics

The `metrics` module samples each host every 15 seconds and keeps the last
hour in memory. The controller collects 1-minute averages once a minute, so a
short outage of either side fills in afterwards. It keeps minutes for 7 days
and hourly averages for 90 days.

Open a host and scroll to Metrics. Choose a range of 1 hour, 24 hours, 7, 30
or 90 days. Each chart lists the latest, lowest and highest value of every
series below it. A disk chart draws the 8 fullest of up to 16 filesystems, and
a network chart the 4 busiest interfaces; the text below says when more exist.
A stretch with no samples is drawn as a gap, never as zero.

The section says why a chart is empty or stale: the first samples are still
coming, the host is offline, the module is off on the host, or the controller
has had no new samples since a time and gives the reason, such as an agent
clock more than 2 minutes off.

The module is on for every host. Turn it off for one host with
`darkpanel module disable --host-id <id> --module-id metrics`.
`darkpanel metrics history get --host-id <id> --range 24h` prints the same
series.

## Alert rules

A rule watches one metric on every host or on a host group, and fires when the
condition holds for its whole duration:

| Metric | Compares |
|---|---|
| cpu, memory, swap | the percent in use |
| disk | the percent in use of each filesystem |
| load | the 1-minute load per CPU |
| offline | fires when the host has no connection |

Two rules exist from the start: "Host offline" (5 minutes, critical) and "Disk
almost full" (above 90 % for 10 minutes, warning). Edit or delete them like
any other.

Create a rule with New rule on the Alerts page, or:

```sh
darkpanel metrics alert create --name "CPU busy" --metric cpu --comparator ">" \
  --threshold 85 --duration-min 15 --severity warning --target "*"
```

An alert resolves once its condition has not held for 2 minutes in a row. A
rule needs every minute of its window, so collected history never fires an
alert for a condition that already ended. Offline rules wait until the
controller has run longer than their duration, so a controller restart raises
none.

## The Alerts page

Alerts lists the firing alerts first, with severity, host, alert, value and
since, then the alerts resolved in the last 24 hours, and for admins the
rules, each with a switch that turns it on or off. A member sees only the
alerts of its own hosts. Alerts reach a phone or a chat through
[Notifications](notifications.md).
