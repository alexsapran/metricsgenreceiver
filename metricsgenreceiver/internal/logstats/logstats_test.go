package logstats

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestFormatNumber(t *testing.T) {
	assert.Equal(t, "0", formatNumber(0))
	assert.Equal(t, "999", formatNumber(999))
	assert.Equal(t, "1,000", formatNumber(1000))
	assert.Equal(t, "1,234,567", formatNumber(1234567))
}

func TestLogStats_Summary(t *testing.T) {
	stats := NewLogStats()

	// Create a resource with attributes
	res := pcommon.NewResource()
	res.Attributes().PutStr("service.name", "nginx")
	res.Attributes().PutStr("k8s.node.name", "node-0")
	res.Attributes().PutStr("k8s.namespace.name", "default")
	res.Attributes().PutStr("k8s.pod.name", "pod-1")

	logs := plog.NewLogs()
	lr := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.SetSeverityText("INFO")
	lr.Attributes().PutStr("http.status_code", "200")

	stats.Record("INFO", res, lr)
	stats.Record("INFO", res, lr)
	stats.Record("WARN", res, lr)

	summary := stats.Summary(map[string]uint64{"upstream_timeout": 1})
	lines := strings.Split(summary, "\n")

	require.Contains(t, summary, "Log Generation Summary:")
	require.Contains(t, summary, "Total logs: 3")
	require.Contains(t, summary, "Severity distribution:")
	require.Contains(t, summary, "INFO:")
	require.Contains(t, summary, "WARN:")
	require.Contains(t, summary, "By application:")
	require.Contains(t, summary, "nginx:")
	require.Contains(t, summary, "By node:")
	require.Contains(t, summary, "node-0:")
	require.Contains(t, summary, "By namespace:")
	require.Contains(t, summary, "default:")
	require.Contains(t, summary, "Field cardinality:")
	require.Contains(t, summary, "Needles:")
	require.Contains(t, summary, "upstream_timeout:")

	// Verify we have expected number of lines
	assert.Greater(t, len(lines), 5)
}

func TestLogStats_Summary_Empty(t *testing.T) {
	stats := NewLogStats()
	summary := stats.Summary(nil)
	assert.Equal(t, "Log Generation Summary:\n  Total logs: 0", summary)
}

func TestLogStats_Summary_NoNeedles(t *testing.T) {
	stats := NewLogStats()
	res := pcommon.NewResource()
	res.Attributes().PutStr("service.name", "test")
	logs := plog.NewLogs()
	lr := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.SetSeverityText("INFO")
	stats.Record("INFO", res, lr)

	summary := stats.Summary(nil)
	assert.Contains(t, summary, "Total logs: 1")
	assert.NotContains(t, summary, "Needles:")
}

func TestNewShardedLogStats(t *testing.T) {
	s := NewShardedLogStats(0, nil)
	require.NotNil(t, s)
	assert.Len(t, s.shards, 1, "n<1 should default to 1 shard")

	s = NewShardedLogStats(5, nil)
	require.NotNil(t, s)
	assert.Len(t, s.shards, 5)
	for i := range s.shards {
		assert.NotNil(t, s.shards[i])
	}
}

func TestShardedLogStats_Shard(t *testing.T) {
	s := NewShardedLogStats(3, nil)
	res := pcommon.NewResource()
	logs := plog.NewLogs()
	lr := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.SetSeverityText("INFO")
	s.Shard(0).Record("INFO", res, lr)
	s.Shard(1).Record("INFO", res, lr)
	s.Shard(3).Record("INFO", res, lr) // Shard(3) wraps to Shard(0)
	merged := s.Merge()
	assert.Equal(t, uint64(3), merged.TotalLogs) // 2 on shard 0 (indices 0 and 3), 1 on shard 1
}

func TestShardedLogStats_Merge(t *testing.T) {
	res := pcommon.NewResource()
	res.Attributes().PutStr("service.name", "nginx")
	res.Attributes().PutStr("k8s.node.name", "node-0")
	res.Attributes().PutStr("k8s.namespace.name", "default")
	logs := plog.NewLogs()
	lr := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.SetSeverityText("INFO")
	lr.Attributes().PutStr("http.status_code", "200")

	s := NewShardedLogStats(3, nil)
	s.Shard(0).Record("INFO", res, lr)
	s.Shard(0).Record("INFO", res, lr)
	s.Shard(1).Record("WARN", res, lr)
	s.Shard(2).Record("ERROR", res, lr)

	merged := s.Merge()
	assert.Equal(t, uint64(4), merged.TotalLogs)
	assert.Equal(t, uint64(2), merged.BySeverity["INFO"])
	assert.Equal(t, uint64(1), merged.BySeverity["WARN"])
	assert.Equal(t, uint64(1), merged.BySeverity["ERROR"])
	assert.Equal(t, uint64(4), merged.ByApp["nginx"])
	assert.Equal(t, uint64(4), merged.ByNode["node-0"])
	assert.Equal(t, uint64(4), merged.ByNamespace["default"])
}

func TestShardedLogStats_MergeFieldCardinality(t *testing.T) {
	res1 := pcommon.NewResource()
	res1.Attributes().PutStr("service.name", "app1")
	res1.Attributes().PutStr("custom.key", "a")
	logs1 := plog.NewLogs()
	lr1 := logs1.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr1.SetSeverityText("INFO")
	lr1.Attributes().PutStr("attr", "x")

	res2 := pcommon.NewResource()
	res2.Attributes().PutStr("service.name", "app2")
	res2.Attributes().PutStr("custom.key", "b")
	logs2 := plog.NewLogs()
	lr2 := logs2.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr2.SetSeverityText("INFO")
	lr2.Attributes().PutStr("attr", "y")

	// Shards 0 and 2 track cardinality (simulating sequential + first worker of a scenario).
	s := NewShardedLogStats(3, []int{0, 2})
	s.Shard(0).Record("INFO", res1, lr1)
	s.Shard(2).Record("INFO", res2, lr2)

	merged := s.Merge()
	require.NotNil(t, merged.FieldCardinality["custom.key"])
	assert.Len(t, merged.FieldCardinality["custom.key"], 2, "union: a and b")
	assert.Contains(t, merged.FieldCardinality["custom.key"], "a")
	assert.Contains(t, merged.FieldCardinality["custom.key"], "b")
	require.NotNil(t, merged.FieldCardinality["attr"])
	assert.Len(t, merged.FieldCardinality["attr"], 2, "union: x and y")
	assert.Contains(t, merged.FieldCardinality["attr"], "x")
	assert.Contains(t, merged.FieldCardinality["attr"], "y")
}

func TestShardedLogStats_NonDesignatedShardsSkipCardinality(t *testing.T) {
	// Shard 0 and 2 track cardinality; shard 1 does not.
	s := NewShardedLogStats(3, []int{0, 2})
	assert.True(t, s.shards[0].trackCardinality, "shard 0 should track cardinality")
	assert.False(t, s.shards[1].trackCardinality, "shard 1 should not track cardinality")
	assert.True(t, s.shards[2].trackCardinality, "shard 2 should track cardinality")

	res := pcommon.NewResource()
	res.Attributes().PutStr("service.name", "app")
	res.Attributes().PutStr("custom.key", "val")
	logs := plog.NewLogs()
	lr := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.SetSeverityText("INFO")

	s.Shard(1).Record("INFO", res, lr)
	assert.Nil(t, s.shards[1].FieldCardinality, "shard 1 should not have cardinality data")

	assert.Equal(t, uint64(1), s.shards[1].TotalLogs)
	assert.Equal(t, uint64(1), s.shards[1].ByApp["app"])
}

func TestShardedLogStats_NilCardinalityShardsDefaultsToZero(t *testing.T) {
	s := NewShardedLogStats(3, nil)
	assert.True(t, s.shards[0].trackCardinality, "shard 0 should track cardinality by default")
	assert.False(t, s.shards[1].trackCardinality, "shard 1 should not track cardinality")
	assert.False(t, s.shards[2].trackCardinality, "shard 2 should not track cardinality")
}
