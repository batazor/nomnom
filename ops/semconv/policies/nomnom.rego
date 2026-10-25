package after_resolution

import rego.v1

# Правила nomnom поверх naming_conventions и stability из opentelemetry-weaver-packages
# (docs/observability.md#именование-метрик). Проверка: make semconv-check.

# Собственные метрики — в namespace nomnom: так они не пересекаются с semconv и
# метриками библиотек.
deny contains finding if {
	some metric in input.registry.metrics
	not startswith(metric.name, "nomnom.")
	finding := violation("nomnom_metric_namespace", sprintf("Metric '%s' must start with 'nomnom.'.", [metric.name]))
}

# Тип и unit не входят в имя: _total и unit suffix добавляет OTLP → Prometheus.
deny contains finding if {
	some metric in input.registry.metrics
	some segment in split(metric.name, ".")
	some word in split(segment, "_")
	word in {"total", "seconds", "milliseconds", "ms", "bytes"}
	finding := violation("nomnom_metric_name_suffix", sprintf("Metric '%s' must not contain '%s': the type and unit go to instrument and unit.", [metric.name, word]))
}

# Длительности и возраст — в секундах (unit s), не в миллисекундах.
deny contains finding if {
	some metric in input.registry.metrics
	segments := split(metric.name, ".")
	segments[count(segments) - 1] in {"duration", "age", "latency"}
	metric.unit != "s"
	finding := violation("nomnom_duration_unit", sprintf("Metric '%s' measures time and must use unit 's', got '%s'.", [metric.name, metric.unit]))
}

# Counter считает сущности: unit — annotation в фигурных скобках ({order}).
deny contains finding if {
	some metric in input.registry.metrics
	metric.instrument == "counter"
	not regex.match(`^\{[a-z_]+\}$`, metric.unit)
	finding := violation("nomnom_counter_unit", sprintf("Counter '%s' must use an annotation unit like '{order}', got '%s'.", [metric.name, metric.unit]))
}

# Идентификаторы и суммы не становятся attributes: high cardinality и PII (inventory.ForbiddenLabels).
deny contains finding if {
	some attr in input.registry.attributes
	some word in split(replace(attr.key, ".", "_"), "_")
	word in {"id", "amount"}
	finding := violation("nomnom_forbidden_attribute", sprintf("Attribute '%s' looks like an identifier or amount; use traces and logs instead.", [attr.key]))
}

violation(id, message) := {
	"id": id,
	"message": message,
	"level": "violation",
	"context": {},
}
