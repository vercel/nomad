# Reserved cores with custom CPU capacity

`client.cpu_core_compute` sets the scheduler charge for each reserved core.
Use the same unit as `client.cpu_total_compute`. The default is zero, which
keeps the CPU value stored in each allocation. A positive value requires a
positive `cpu_total_compute`.

For example:

```hcl
client {
  cpu_total_compute = 16000
  cpu_core_compute = 2000
  reservable_cores = "0-7"
}
```

A task that reserves two cores uses 4000 units for placement. A task with
`cpu = 1000` uses 1000 units. The core count still controls exclusive core
placement. The scheduler does not change an allocation's stored CPU value or
the CPU set passed to its task driver.

Choose the per-core value for the capacity model in use. It is not a detected
CPU frequency, and it is not automatically multiplied by an SMT factor.
The `cpu.corecompute` node attribute exposes the configured scheduler charge
to API consumers. It is absent when the override is inactive.

Update servers before clients. Old servers do not apply the new charge.
Pause new placement and let in-flight plans settle before changing accounting
on an existing node. A client restart is not an atomic placement fence.
Restart each client after changing its configuration. Do not submit tasks
that use a new CPU unit until all eligible clients use that unit. Keep the
configuration unchanged while tasks that depend on it remain allocated.
