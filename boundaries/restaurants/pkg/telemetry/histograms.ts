// Every histogram of a restaurants process is a base2 exponential histogram
// (ADR 9, openspec use-exponential-histograms): Grafana Cloud stores it as a
// native histogram, one series per label set billed by non-empty buckets.
// Instruments do not set explicit bucket boundaries. The SDK keeps its
// default max scale (20) and lowers it when the spread of values grows.
import {
  AggregationType,
  InstrumentType,
  type ViewOptions,
} from "@opentelemetry/sdk-metrics";

/** Maximum buckets per sign of one histogram point: at most 10 billable series. */
export const HISTOGRAM_MAX_SIZE = 40;

export const HISTOGRAM_VIEWS: ViewOptions[] = [
  {
    instrumentType: InstrumentType.HISTOGRAM,
    aggregation: {
      type: AggregationType.EXPONENTIAL_HISTOGRAM,
      options: { maxSize: HISTOGRAM_MAX_SIZE },
    },
  },
];
