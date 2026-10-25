package overview

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// Selector — vector selector PromQL-выражения: имя серии и labels в matchers.
type Selector struct {
	Series string
	Labels []string
}

var promql = parser.NewParser(parser.Options{})

var grafanaVariable = regexp.MustCompile(`\$\{[^}]+\}|\$__\w+|\$\w+`)

// Normalize подставляет вместо Grafana variables значения, допустимые для parser.
func Normalize(expr string) string {
	return grafanaVariable.ReplaceAllStringFunc(expr, func(v string) string {
		if strings.HasPrefix(v, "$__") {
			return "5m"
		}
		return "x"
	})
}

// Analysis — vector selectors и labels группировки (by/on) выражения.
type Analysis struct {
	Selectors []Selector
	Grouping  []string
}

// Labels — все labels в matchers.
func (a Analysis) Labels() []string {
	var out []string
	for _, s := range a.Selectors {
		out = append(out, s.Labels...)
	}
	return out
}

// Analyze разбирает выражение PromQL после подстановки Grafana variables.
func Analyze(expr string) (Analysis, error) {
	node, err := promql.ParseExpr(Normalize(expr))
	if err != nil {
		return Analysis{}, fmt.Errorf("parse %q: %w", expr, err)
	}
	var a Analysis
	parser.Inspect(node, func(n parser.Node, _ []parser.Node) error {
		switch n := n.(type) {
		case *parser.VectorSelector:
			s := Selector{Series: n.Name}
			for _, m := range n.LabelMatchers {
				if m.Name != labels.MetricName {
					s.Labels = append(s.Labels, m.Name)
				}
			}
			a.Selectors = append(a.Selectors, s)
		case *parser.AggregateExpr:
			a.Grouping = append(a.Grouping, n.Grouping...)
		case *parser.BinaryExpr:
			if n.VectorMatching != nil {
				a.Grouping = append(a.Grouping, n.VectorMatching.MatchingLabels...)
			}
		}
		return nil
	})
	return a, nil
}
