# Duration-aware drain backfilling

Enable backfill when a node has a long maintenance window and spare capacity:

```shell
nomad node drain -enable -duration-aware -deadline 4h -backfill-buffer 1m -detach NODE_ID
```

The equivalent node drain API specification is:

```json
{
  "DrainSpec": {
    "Deadline": 14400000000000,
    "DurationAware": true,
    "BackfillBuffer": 60000000000
  }
}
```

API durations are nanoseconds. `BackfillBuffer` defaults to 30 seconds when zero
or omitted. Duration-aware draining requires a positive deadline and cannot be
combined with forced or unlimited drains.

Batch and sysbatch task groups with a positive `max_run_duration` can still be
placed when their full runtime, declared shutdown delays and kill timeouts, and
the backfill buffer fit strictly before the absolute drain deadline. Jobs with
plugins, services, and unbounded task groups are not admitted as backfill. Each
task group is checked independently. Backfill uses spare capacity without
preempting the workloads already draining; ordinary placement constraints and
ranking still apply.

Allocations already on the node retain normal drain semantics. Batch work can
finish naturally; services migrate normally. An allocation is not stopped just
because the time remaining until the node deadline becomes less than its
configured maximum runtime. Runtime limits remain anchored to allocation creation
time. Updates that extend a bounded allocation's lifetime must still fit before
the deadline; the scheduler can require replacement elsewhere instead.

Unlike ordinary drains, this mode deliberately remains active until the deadline,
even if the node becomes temporarily empty. At the deadline the server closes
admission, scans all remaining allocations, and initiates their removal. Admission
closure is persisted and fences stale plans, including after a leadership change.
Completion leaves the node ineligible. Cancel with the ordinary `-disable` command
and optionally `-keep-ineligible`.

The buffer provides headroom for propagation and cleanup. It cannot guarantee an
exact process-exit time under arbitrary hook, driver, or control-plane delays.
As with ordinary drains, the deadline initiates removal. Use a buffer appropriate
to the workloads and cluster. All servers must support this policy before enabling
it, and eligible clients must enforce `max_run_duration`.
