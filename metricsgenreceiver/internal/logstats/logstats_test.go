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
