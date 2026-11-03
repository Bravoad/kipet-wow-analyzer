package httptransport

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var durationBuckets = [...]float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

type metricKey struct {
	route, method string
	status        int
}
type durationMetric struct {
	count   uint64
	sum     float64
	buckets [len(durationBuckets)]uint64
}
type httpMetrics struct {
	mu   sync.Mutex
	rows map[metricKey]*durationMetric
}

func (m *httpMetrics) observe(route, method string, status int, seconds float64) {
	switch method {
	case "GET", "POST", "HEAD", "OPTIONS":
	default:
		method = "OTHER"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = make(map[metricKey]*durationMetric)
	}
	key := metricKey{route, method, status}
	row := m.rows[key]
	if row == nil {
		row = &durationMetric{}
		m.rows[key] = row
	}
	row.count++
	row.sum += seconds
	for i, bound := range durationBuckets {
		if seconds <= bound {
			row.buckets[i]++
		}
	}
}

// The router supplies templates. Names, query strings, job/report IDs and IPs
// are never labels; unknown URLs share the single unmatched series.
func (m *httpMetrics) prometheus() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out strings.Builder
	out.WriteString("# TYPE kipet_http_request_duration_seconds histogram\n")
	keys := make([]metricKey, 0, len(m.rows))
	for key := range m.rows {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.route != b.route {
			return a.route < b.route
		}
		if a.method != b.method {
			return a.method < b.method
		}
		return a.status < b.status
	})
	for _, key := range keys {
		row := m.rows[key]
		labels := fmt.Sprintf("route=%q,method=%q,status=%q", key.route, key.method, strconv.Itoa(key.status))
		for i, bound := range durationBuckets {
			fmt.Fprintf(&out, "kipet_http_request_duration_seconds_bucket{%s,le=%q} %d\n", labels, strconv.FormatFloat(bound, 'g', -1, 64), row.buckets[i])
		}
		fmt.Fprintf(&out, "kipet_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, row.count)
		fmt.Fprintf(&out, "kipet_http_request_duration_seconds_sum{%s} %g\nkipet_http_request_duration_seconds_count{%s} %d\n", labels, row.sum, labels, row.count)
	}
	return out.String()
}
