package collector

import (
	"testing"
)

func TestGatewayCollectorBadMetrics(t *testing.T) {
	g := newGatewayCollector()
	g.parsePostMetrics(`
# HELP badm Metric with a bad name.
# TYPE badm counter
badm}{pid="31579",host="ip-172-31-44-208"} 1466740
`)
	if len(g.counters) > 0 || len(g.gauges) > 0 || len(g.labels) > 0 {
		t.Fail()
	}
}
