package server

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// metricSeries returns the number of series of the metric name in g
func metricSeries(t *testing.T, g prometheus.Gatherer, name string) int {
	t.Helper()

	if family := gatherFamily(t, g, name); family != nil {
		return len(family.GetMetric())
	}

	return 0
}

// metricValue returns the value of the series of the metric name in g with
// the given label name and value pairs, or 0 if there is no such series, as
// for a counter that has not been incremented
func metricValue(t *testing.T, g prometheus.Gatherer, name string, labels ...string) float64 {
	t.Helper()

	if len(labels)%2 != 0 {
		t.Fatalf("metricValue() labels %v are not name and value pairs", labels)
	}

	family := gatherFamily(t, g, name)
	if family == nil {
		return 0
	}

	for _, m := range family.GetMetric() {
		if !hasLabels(m, labels) {
			continue
		}

		switch {
		case m.GetCounter() != nil:
			return m.GetCounter().GetValue()
		case m.GetGauge() != nil:
			return m.GetGauge().GetValue()
		default:
			t.Fatalf("metric %s is not a counter or gauge", name)
		}
	}

	return 0
}

// gatherFamily returns the metric family name from g, or nil if it has no
// series
func gatherFamily(t *testing.T, g prometheus.Gatherer, name string) *dto.MetricFamily {
	t.Helper()

	families, err := g.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}

	return nil
}

// hasLabels reports whether m has every label name and value pair in labels
func hasLabels(m *dto.Metric, labels []string) bool {
	for i := 0; i < len(labels); i += 2 {
		found := false
		for _, l := range m.GetLabel() {
			if l.GetName() == labels[i] && l.GetValue() == labels[i+1] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}
