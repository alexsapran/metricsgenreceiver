package logstats

import (
	"fmt"
	"sort"
	"strconv"
	"sync"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

type LogStats struct {
	mu                sync.Mutex
	TotalLogs         uint64
	BySeverity        map[string]uint64
	ByApp             map[string]uint64
	ByNode            map[string]uint64
	ByNamespace       map[string]uint64
	FieldCardinality map[string]map[string]struct{}
}

func NewLogStats() *LogStats {
	return &LogStats{
		BySeverity:      make(map[string]uint64),
		ByApp:           make(map[string]uint64),
		ByNode:          make(map[string]uint64),
		ByNamespace:     make(map[string]uint64),
		FieldCardinality: make(map[string]map[string]struct{}),
	}
}

func (s *LogStats) Record(severityText string, resource pcommon.Resource, logRecord plog.LogRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.TotalLogs++

	// BySeverity
	s.BySeverity[severityText]++

	// ByApp (service.name)
	if v, ok := resource.Attributes().Get("service.name"); ok {
		app := valueToString(v)
		s.ByApp[app]++
	}

	// ByNode (k8s.node.name)
	if v, ok := resource.Attributes().Get("k8s.node.name"); ok {
		node := valueToString(v)
		s.ByNode[node]++
	}

	// ByNamespace (k8s.namespace.name)
	if v, ok := resource.Attributes().Get("k8s.namespace.name"); ok {
		ns := valueToString(v)
		s.ByNamespace[ns]++
	}

	// Field cardinality from resource attributes
	resource.Attributes().Range(func(k string, v pcommon.Value) bool {
		s.addCardinality(k, v)
		return true
	})

	// Field cardinality from log record attributes
	logRecord.Attributes().Range(func(k string, v pcommon.Value) bool {
		s.addCardinality(k, v)
		return true
	})
}

func (s *LogStats) addCardinality(key string, v pcommon.Value) {
	valStr := valueToString(v)
	if s.FieldCardinality[key] == nil {
		s.FieldCardinality[key] = make(map[string]struct{})
	}
	s.FieldCardinality[key][valStr] = struct{}{}
}

func valueToString(v pcommon.Value) string {
	switch v.Type() {
	case pcommon.ValueTypeStr:
		return v.Str()
	case pcommon.ValueTypeInt:
		return strconv.FormatInt(v.Int(), 10)
	case pcommon.ValueTypeDouble:
		return strconv.FormatFloat(v.Double(), 'f', -1, 64)
	case pcommon.ValueTypeBool:
		return strconv.FormatBool(v.Bool())
	default:
		return fmt.Sprintf("%v", v.AsRaw())
	}
}

func (s *LogStats) Summary(needleOccurrences map[string]uint64) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := s.TotalLogs
	if total == 0 {
		return "Log Generation Summary:\n  Total logs: 0"
	}

	var b []byte
	b = append(b, "Log Generation Summary:\n"...)
	b = append(b, "  Total logs: "+formatNumber(total)+"\n"...)

	// Severity distribution
	if len(s.BySeverity) > 0 {
		b = append(b, "  Severity distribution:\n"...)
		for _, key := range sortedKeys(s.BySeverity) {
			cnt := s.BySeverity[key]
			pct := 100.0 * float64(cnt) / float64(total)
			b = append(b, fmt.Sprintf("    %-6s %s (%.1f%%)\n", key+":", formatNumber(cnt), pct)...)
		}
	}

	// By application
	if len(s.ByApp) > 0 {
		b = append(b, "  By application:\n"...)
		for _, key := range sortedKeys(s.ByApp) {
			cnt := s.ByApp[key]
			pct := 100.0 * float64(cnt) / float64(total)
			b = append(b, fmt.Sprintf("    %-12s %s (%.1f%%)\n", key+":", formatNumber(cnt), pct)...)
		}
	}

	// By node
	if len(s.ByNode) > 0 {
		b = append(b, "  By node:\n"...)
		for _, key := range sortedKeys(s.ByNode) {
			cnt := s.ByNode[key]
			pct := 100.0 * float64(cnt) / float64(total)
			b = append(b, fmt.Sprintf("    %-12s %s (%.1f%%)\n", key+":", formatNumber(cnt), pct)...)
		}
	}

	// By namespace
	if len(s.ByNamespace) > 0 {
		b = append(b, "  By namespace:\n"...)
		for _, key := range sortedKeys(s.ByNamespace) {
			cnt := s.ByNamespace[key]
			pct := 100.0 * float64(cnt) / float64(total)
			b = append(b, fmt.Sprintf("    %-12s %s (%.1f%%)\n", key+":", formatNumber(cnt), pct)...)
		}
	}

	// Field cardinality
	if len(s.FieldCardinality) > 0 {
		b = append(b, "  Field cardinality:\n"...)
		for _, key := range sortedKeysMap(s.FieldCardinality) {
			card := uint64(len(s.FieldCardinality[key]))
			b = append(b, fmt.Sprintf("    %-24s %s\n", key+":", formatNumber(card))...)
		}
	}

	// Needles (from passed parameter - receiver has the canonical source)
	if len(needleOccurrences) > 0 {
		b = append(b, "  Needles:\n"...)
		for _, name := range sortedKeys(needleOccurrences) {
			cnt := needleOccurrences[name]
			b = append(b, fmt.Sprintf("    %-20s %s\n", name+":", formatNumber(cnt))...)
		}
	}

	return string(b)
}

func sortedKeys(m map[string]uint64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysMap(m map[string]map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatNumber(n uint64) string {
	if n < 1000 {
		return strconv.FormatUint(n, 10)
	}
	s := strconv.FormatUint(n, 10)
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
