package overview

import (
	"github.com/grafana/grafana-foundation-sdk/go/cog"
	dashboard "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// sankeyPlugin — панель Sankey: встроенной в Grafana нет, плагин ставится в stack.
const sankeyPlugin = "netsage-sankey-panel"

// sankeyVisualization — панель плагина; у него нет builder в foundation SDK.
type sankeyVisualization struct{}

var _ cog.Builder[dashboard.VizConfigKind] = sankeyVisualization{}

func (sankeyVisualization) Build() (dashboard.VizConfigKind, error) {
	return dashboard.VizConfigKind{
		Kind:  "VizConfig",
		Group: sankeyPlugin,
		Spec: dashboard.VizConfigSpec{
			Options:     map[string]any{"valueField": "Value", "monochrome": false},
			FieldConfig: *dashboard.NewFieldConfigSource(),
		},
	}, nil
}

// sankeyColumns оставляет порядок, по которому плагин строит потоки: узлы от первой
// колонки к последней, без времени instant-запроса.
func sankeyColumns() *dashboard.TransformationBuilder {
	return dashboard.NewTransformationBuilder().
		Group("organize").
		Options(map[string]any{
			"excludeByName": map[string]bool{"Time": true},
			"indexByName":   map[string]int{"nomnom_fsm_from_state": 0, "nomnom_fsm_to_state": 1, "Value": 2},
			"renameByName":  map[string]string{"nomnom_fsm_from_state": "from", "nomnom_fsm_to_state": "to"},
		})
}
