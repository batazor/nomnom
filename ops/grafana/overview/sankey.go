package overview

import (
	"github.com/grafana/grafana-foundation-sdk/go/cog"
	dashboard "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// sankeyPlugin — Business Charts (Apache ECharts): встроенной Sankey в Grafana нет,
// плагин ставится в stack. netsage-sankey-panel не подходит: его узел — пара имени и
// колонки, поэтому цепочка состояний распадается на две несвязанные колонки.
const sankeyPlugin = "volkovlabs-echarts-panel"

// sankeyOption строит граф по строкам from, to, Value; узлы — все упомянутые
// состояния. Нет строк — пустая панель.
const sankeyOption = `const frame = context.panel.data.series[0];
if (!frame) {
  return {};
}
const column = (name) => frame.fields.find((field) => field.name === name).values;
const from = column("from");
const to = column("to");
const value = column("Value");
const links = [];
const names = new Set();
for (let i = 0; i < frame.length; i++) {
  links.push({ source: from[i], target: to[i], value: value[i] });
  names.add(from[i]);
  names.add(to[i]);
}
return {
  backgroundColor: "transparent",
  tooltip: { trigger: "item" },
  series: [{
    type: "sankey",
    nodeAlign: "left",
    emphasis: { focus: "adjacency" },
    lineStyle: { color: "gradient", curveness: 0.5 },
    label: { color: context.grafana.theme.colors.text.primary },
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

// sankeyColumns даёт sankeyOption колонки from, to и Value без времени instant-запроса.
func sankeyColumns() *dashboard.TransformationBuilder {
	return dashboard.NewTransformationBuilder().
		Group("organize").
		Options(map[string]any{
			"excludeByName": map[string]bool{"Time": true},
			"renameByName":  map[string]string{"nomnom_fsm_from_state": "from", "nomnom_fsm_to_state": "to"},
		})
}
