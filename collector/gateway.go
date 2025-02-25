//go:build !ngateway
// +build !ngateway

package collector

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

type expiringMetricValue struct {
	values         []string
	expiryDeadline time.Time
}

// GatewayCollector collects posted metrics
type GatewayCollector struct {
	postedCollectors []prometheus.Collector
	counters         map[string]*prometheus.CounterVec
	gauges           map[string]*prometheus.GaugeVec
	labels           map[string][]string
	expiringMetrics  map[string][]*expiringMetricValue
	lastUpdate       time.Time
	sampleInterval   float64
	nextHandler      http.HandlerFunc
	metricsMu        sync.Mutex
}

// DefaultGatewayCollector is the singleton gateway collector
var DefaultGatewayCollector = newGatewayCollector()

func init() {
	registerCollector("gateway", defaultEnabled, func() (Collector, error) { return DefaultGatewayCollector, nil })
}

// NewGatewayCollector returns a new Collector exposing gateway stats.
func newGatewayCollector() *GatewayCollector {
	return &GatewayCollector{
		counters:        make(map[string]*prometheus.CounterVec),
		gauges:          make(map[string]*prometheus.GaugeVec),
		labels:          make(map[string][]string),
		expiringMetrics: make(map[string][]*expiringMetricValue),
		lastUpdate:      time.Unix(0, 0),
		sampleInterval:  0.0,
		metricsMu:       sync.Mutex{},
	}
}

// SetNextHandler sets the next http handler
func (c *GatewayCollector) SetNextHandler(nextHandler http.HandlerFunc) {
	c.nextHandler = nextHandler
}

func (c *GatewayCollector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r == nil {
		return
	}
	if r.Method == "POST" && c.nextHandler != nil {
		c.postHandler(w, r)
		return
	}
	c.nextHandler(w, r)
}

// Update we use this function to measure the update rates.
func (c *GatewayCollector) Update(ch chan<- prometheus.Metric) error {
	now := time.Now()
	timeSince := now.Sub(c.lastUpdate)
	if timeSince.Seconds() < 3600.0 && timeSince.Seconds() > 0.0 {
		c.sampleInterval = timeSince.Seconds()
	}
	c.lastUpdate = now
	return nil
}

// Describe provide the description for all the metrics.
func (c *GatewayCollector) Describe(ch chan<- *prometheus.Desc) {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	for _, col := range c.postedCollectors {
		col.Describe(ch)
	}
}

// Collect collects the metrics from the underlaying collectors.
func (c *GatewayCollector) Collect(ch chan<- prometheus.Metric) {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	// clear all expired metrics.
	now := time.Now()
	remainingExpiringMetrics := map[string][]*expiringMetricValue{}
	for metricName, expiringMeticsList := range c.expiringMetrics {
		remainingMetrics := []*expiringMetricValue{}
		for _, expirity := range expiringMeticsList {
			if now.After(expirity.expiryDeadline) {
				if counterVec, isCounter := c.counters[metricName]; isCounter {
					counterVec.DeleteLabelValues(expirity.values...)
					//fmt.Printf("Removing values for label '%s', now=%v expire=%v\n", metricName, now, expirity.expiryDeadline)
				} else if gaugeVec, isGauge := c.gauges[metricName]; isGauge {
					gaugeVec.DeleteLabelValues(expirity.values...)
					//fmt.Printf("Removing values for label '%s', now=%v expire=%v\n", metricName, now, expirity.expiryDeadline)
				} else {
					remainingMetrics = append(remainingMetrics, expirity)
				}
			} else {
				remainingMetrics = append(remainingMetrics, expirity)
			}
		}
		if len(remainingMetrics) > 0 {
			remainingExpiringMetrics[metricName] = remainingMetrics
		}
	}
	c.expiringMetrics = remainingExpiringMetrics

	// iterate on collectors.
	for _, col := range c.postedCollectors {
		col.Collect(ch)
	}
}

func (c *GatewayCollector) postHandler(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Errorln("Error reading body: ", err)
		http.Error(w, "can't read body", http.StatusBadRequest)
		return
	}

	if c.sampleInterval > 0.0 {
		w.Header()["SampleRate"] = []string{fmt.Sprintf("%3.2f", c.sampleInterval)}
		go c.parsePostMetrics(string(body))
	}

	return
}

type labelPair struct {
	k string
	v string
}

type labelPairs []labelPair

func (a labelPairs) Len() int           { return len(a) }
func (a labelPairs) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a labelPairs) Less(i, j int) bool { return strings.Compare(a[i].k, a[j].k) < 0 }

func (c *GatewayCollector) parseLabels(labelsBlock string) (labels []string, values []string) {
	//fmt.Printf("parsing labels block '%s'\n", labelsBlock)
	pairs := strings.Split(labelsBlock, ",")

	results := labelPairs{}
	for _, pair := range pairs {
		if !strings.Contains(pair, "=") {
			continue
		}
		key := pair[0:strings.Index(pair, "=")]
		value := pair[strings.Index(pair, "=")+1:]
		if len(value) < 2 {
			continue
		}
		if value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		results = append(results, labelPair{k: key, v: value})
	}

	sort.Sort(labelPairs(results))
	for _, r := range results {
		labels = append(labels, r.k)
		values = append(values, r.v)
	}
	return
}

func (c *GatewayCollector) parsePostMetrics(metrics string) {
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()

	lines := strings.Split(metrics, "\n")
	metricToHelp := make(map[string]string)
	metricToType := make(map[string]string)
	for _, l := range lines {
		if strings.HasPrefix(l, "# HELP ") {
			l = l[len("# HELP "):]
			space := strings.Index(l, " ")
			metricName := l[0:space]
			metricHelp := l[space+1:]
			metricToHelp[metricName] = metricHelp
			continue
		}
		if strings.HasPrefix(l, "# TYPE ") {
			l = l[len("# TYPE "):]
			space := strings.Index(l, " ")
			metricName := l[0:space]
			metricType := l[space+1:]
			metricToType[metricName] = metricType
			continue
		}
		if !strings.Contains(l, " ") {
			continue
		}
		var metricName string
		var labelsBlock string
		var value string
		//fmt.Printf(" original string '%s'\n", l)
		if strings.Contains(l, "{") {
			metricName = l[0:strings.Index(l, "{")]
			if !strings.Contains(l, "}") {
				continue
			}
			if strings.Index(l, "}") < strings.Index(l, "{") {
				continue
			}
			labelsBlock = l[strings.Index(l, "{")+1 : strings.Index(l, "}")]
		} else {
			metricName = l[0:strings.Index(l, " ")]
		}
		l = l[strings.Index(l, " ")+1:]
		if strings.Contains(l, " ") {
			value = l[0:strings.Index(l, " ")]
		} else {
			value = l
		}
		//fmt.Printf(" found value string '%s'\n", value)
		if _, hasHelp := metricToHelp[metricName]; !hasHelp {
			continue
		}
		if _, hasType := metricToType[metricName]; !hasType {
			continue
		}

		labelsKey, labelsValues := c.parseLabels(labelsBlock)
		switch metricToType[metricName] {
		case "counter":
			var metric *prometheus.CounterVec
			if _, has := c.counters[metricName]; has {
				metric = c.counters[metricName]
			} else {
				metric = prometheus.NewCounterVec(prometheus.CounterOpts{
					Name: metricName,
					Help: metricToHelp[metricName],
				}, labelsKey)
				c.counters[metricName] = metric
				c.postedCollectors = append(c.postedCollectors, *metric)
				c.labels[metricName] = labelsKey
			}
			if fValue, err := strconv.ParseFloat(value, 64); err == nil {
				metric.DeleteLabelValues(labelsValues...)
				if counter, err := metric.GetMetricWithLabelValues(labelsValues...); err == nil {
					counter.Add(fValue)
					c.addExpiringMetric(metricName, labelsValues)
				}
			}
		case "gauge":
			var metric *prometheus.GaugeVec
			if _, has := c.gauges[metricName]; has {
				metric = c.gauges[metricName]
			} else {
				metric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
					Name: metricName,
					Help: metricToHelp[metricName],
				}, labelsKey)
				c.gauges[metricName] = metric
				c.postedCollectors = append(c.postedCollectors, metric)
				c.labels[metricName] = labelsKey
			}
			if gauge, err := metric.GetMetricWithLabelValues(labelsValues...); err == nil {
				if fValue, err := strconv.ParseFloat(value, 64); err == nil {
					gauge.Set(fValue)
					c.addExpiringMetric(metricName, labelsValues)
				}
			}
		default:
			continue
		}
	}
	return
}

func (c *GatewayCollector) addExpiringMetric(metricName string, labelValues []string) {
	var deadLine time.Time
	now := time.Now()
	if sampleDuration, err := time.ParseDuration(fmt.Sprintf("%2.2fs", c.sampleInterval*2)); err != nil {
		return
	} else {
		deadLine = now.Add(sampleDuration)
	}
	if _, has := c.expiringMetrics[metricName]; !has {
		c.expiringMetrics[metricName] = []*expiringMetricValue{&expiringMetricValue{values: labelValues, expiryDeadline: deadLine}}
		//fmt.Printf("adding metric for '%s' with exp=%v now=%v\n", metricName, deadLine, now)
		return
	}
	for i, expiringValue := range c.expiringMetrics[metricName] {
		if c.compareLabelValues(expiringValue.values, labelValues) {
			// we found the entry, all we need is to update the expiration.
			//fmt.Printf("updating metric for '%s' with exp=%v to exp=%v on now=%v\n", metricName, c.expiringMetrics[metricName][i].expiryDeadline, deadLine, now)
			c.expiringMetrics[metricName][i].expiryDeadline = deadLine
			return
		}
	}
	// can't find it in the list. add to the list.
	c.expiringMetrics[metricName] = append(c.expiringMetrics[metricName], &expiringMetricValue{values: labelValues, expiryDeadline: deadLine})
	//fmt.Printf("appending metric for '%s' with exp=%v now=%v\n", metricName, deadLine, now)
	return
}

func (c *GatewayCollector) compareLabelValues(labelValues1 []string, labelValues2 []string) bool {
	if len(labelValues1) != len(labelValues2) {
		return false
	}
	for i, s1 := range labelValues1 {
		if s1 != labelValues2[i] {
			return false
		}
	}
	return true
}
