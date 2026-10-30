package overview

import (
	"github.com/grafana/grafana-foundation-sdk/go/cog"
	dashboard "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// sankeyPlugin — Business Charts (Apache ECharts): встроенной Sankey в Grafana нет,
// плагин ставится в stack. netsage-sankey-panel не подходит: его узел — пара имени и
// колонки, поэтому цепочка состояний распадается на две несвязанные колонки.
const sankeyPlugin = "volkovlabs-echarts-panel"

// sankeyHeight — высота row с Sankey в пикселях: потоки не читаются в обычной панели.
const sankeyHeight = 500

// sankeyOption строит граф по сериям instant-запроса: узлы связи — в labels значения.
// Нет серий — пустая панель.
const sankeyOption = `const links = [];
const names = new Set();
for (const frame of context.panel.data.series) {
  const field = frame.fields.find((candidate) => candidate.type === "number");
  const source = field.labels.nomnom_fsm_from_state;
  const target = field.labels.nomnom_fsm_to_state;
  links.push({ source, target, value: field.values[field.values.length - 1] });
  names.add(source);
  names.add(target);
}
return {
  animation: false,
  backgroundColor: "transparent",
  tooltip: { trigger: "item" },
  series: [{
    type: "sankey",
    nodeAlign: "left",
    emphasis: { focus: "adjacency" },
    lineStyle: { color: "gradient", curveness: 0.5 },
    label: { color: "#d8d9da" },
    data: Array.from(names).map((name) => ({ name })),
    links,
  }],
};`

// sankeyVisualization — панель плагина; у него нет builder в foundation SDK.
type sankeyVisualization struct{}

var _ cog.Builder[dashboard.VizConfigKind] = sankeyVisualization{}

func (sankeyVisualization) Build() (dashboard.VizConfigKind, error) {
	return dashboard.VizConfigKind{
		Kind:  "VizConfig",
		Group: sankeyPlugin,
		Spec: dashboard.VizConfigSpec{
			Options:     map[string]any{"editorMode": "code", "getOption": sankeyOption},
			FieldConfig: *dashboard.NewFieldConfigSource(),
		},
	}, nil
}
