// Semconv metrics of the runtime section of the inventory
// (ops/grafana/inventory) that @opentelemetry/instrumentation-runtime-node
// does not provide: `process.cpu.time` by `cpu.mode`, `process.memory.usage`
// (resident set size) and `v8js.memory.heap.limit` by `v8js.heap.space.name`
// (deprecated in semconv for `v8js.memory.heap.space.size`, which
// runtime-node reports; kept while the inventory panels read it). Host
// metrics `system.*` are node signals, not service signals.
import { getHeapSpaceStatistics } from "node:v8";

import type { Meter } from "@opentelemetry/api";

export function registerProcessMetrics(meter: Meter): void {
  const cpuTime = meter.createObservableCounter("process.cpu.time", {
    description: "Total CPU seconds broken down by different CPU modes.",
    unit: "s",
  });
  const memoryUsage = meter.createObservableUpDownCounter(
    "process.memory.usage",
    {
      description: "The amount of physical memory in use.",
      unit: "By",
    },
  );
  cpuTime.addCallback((result) => {
    const usage = process.cpuUsage();
    result.observe(usage.user / 1e6, { "cpu.mode": "user" });
    result.observe(usage.system / 1e6, { "cpu.mode": "system" });
  });
  memoryUsage.addCallback((result) => {
    result.observe(process.memoryUsage.rss());
  });
  const heapLimit = meter.createObservableUpDownCounter(
    "v8js.memory.heap.limit",
    {
      description: "Total heap memory size pre-allocated.",
      unit: "By",
    },
  );
  heapLimit.addCallback((result) => {
    for (const space of getHeapSpaceStatistics()) {
      result.observe(space.space_size, {
        "v8js.heap.space.name": space.space_name,
      });
    }
  });
}
