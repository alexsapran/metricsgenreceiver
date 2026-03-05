package logstats

import (
	"fmt"
	"sort"
	"strconv"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

type LogStats struct {
	TotalLogs        uint64
	BySeverity       map[string]uint64
	ByApp            map[string]uint64
	ByNode           map[string]uint64
	ByNamespace      map[string]uint64
	FieldCardinality map[string]map[string]struct{}
	trackCardinality bool
	cappedFields     map[string]struct{}
}

func newLogStats(trackCardinality bool) *LogStats {
	s := &LogStats{
		BySeverity:       make(map[string]uint64),
		ByApp:            make(map[string]uint64),
		ByNode:           make(map[string]uint64),
		ByNamespace:      make(map[string]uint64),
		trackCardinality: trackCardinality,
	}
	if trackCardinality {
		s.FieldCardinality = make(map[string]map[string]struct{})
		s.cappedFields = make(map[string]struct{})
	}
	return s
}

// NewLogStats creates a LogStats that tracks field cardinality.
func NewLogStats() *LogStats {
	return newLogStats(true)
}

func (s *LogStats) Record(severityText string, resource pcommon.Resource, logRecord plog.LogRecord) {
	s.TotalLogs++

	s.BySeverity[severityText]++

	if v, ok := resource.Attributes().Get("service.name"); ok {
		s.ByApp[valueToString(v)]++
	}

	if v, ok := resource.Attributes().Get("k8s.node.name"); ok {
		s.ByNode[valueToString(v)]++
	}

	if v, ok := resource.Attributes().Get("k8s.namespace.name"); ok {
		s.ByNamespace[valueToString(v)]++
	}

	if !s.trackCardinality {
		return
	}

	resource.Attributes().Range(func(k string, v pcommon.Value) bool {
		s.addCardinality(k, v)
		return true
	})

	logRecord.Attributes().Range(func(k string, v pcommon.Value) bool {
		s.addCardinality(k, v)
		return true
	})
}

const maxFieldCardinality = 500

func (s *LogStats) addCardinality(key string, v pcommon.Value) {
	if _, capped := s.cappedFields[key]; capped {
		return
	}
	existing := s.FieldCardinality[key]
	if existing != nil && len(existing) >= maxFieldCardinality {
		s.cappedFields[key] = struct{}{}
		return
	}
	valStr := valueToString(v)
	if existing == nil {
		existing = make(map[string]struct{})
		s.FieldCardinality[key] = existing
	}
	existing[valStr] = struct{}{}
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
	// Summary is called once on merged stats after all goroutines are done.
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

// ShardedLogStats holds per-goroutine shards to avoid mutex contention.
// Each shard is written by only one goroutine; Merge() combines them for Summary().
// Only designated cardinality shards track FieldCardinality to avoid duplicating
// large string sets across all workers.
type ShardedLogStats struct {
	shards []*LogStats
}

// NewShardedLogStats creates n shards. cardinalityShards lists the indices
// that should track FieldCardinality (typically shard 0 for the sequential
// path plus the first shard of each concurrent scenario group). Passing nil
// or empty enables cardinality on shard 0 only.
func NewShardedLogStats(n int, cardinalityShards []int) *ShardedLogStats {
	if n < 1 {
		n = 1
	}
	track := make(map[int]struct{}, len(cardinalityShards))
	if len(cardinalityShards) == 0 {
		track[0] = struct{}{}
	} else {
		for _, idx := range cardinalityShards {
			track[idx] = struct{}{}
		}
	}
	shards := make([]*LogStats, n)
	for i := 0; i < n; i++ {
		_, ok := track[i]
		shards[i] = newLogStats(ok)
	}
	return &ShardedLogStats{shards: shards}
}

func (s *ShardedLogStats) Shard(i int) *LogStats {
	return s.shards[i%len(s.shards)]
}

func (s *ShardedLogStats) Merge() *LogStats {
	merged := NewLogStats()
	for _, shard := range s.shards {
		merged.TotalLogs += shard.TotalLogs
		for k, v := range shard.BySeverity {
			merged.BySeverity[k] += v
		}
		for k, v := range shard.ByApp {
			merged.ByApp[k] += v
		}
		for k, v := range shard.ByNode {
			merged.ByNode[k] += v
		}
		for k, v := range shard.ByNamespace {
			merged.ByNamespace[k] += v
		}
		if shard.FieldCardinality != nil {
			for k, vals := range shard.FieldCardinality {
				if merged.FieldCardinality[k] == nil {
					merged.FieldCardinality[k] = make(map[string]struct{})
				}
				for v := range vals {
					merged.FieldCardinality[k][v] = struct{}{}
				}
			}
		}
	}
	return merged
}
