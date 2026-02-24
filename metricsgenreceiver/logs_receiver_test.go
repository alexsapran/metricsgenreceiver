package metricsgenreceiver

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

func testLogsConfig(seed int64, scale, logsPerInterval int, needles []NeedleCfg) *Config {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	return &Config{
		StartTime:   startTime,
		EndTime:     startTime.Add(2 * time.Second),
		Interval:    1 * time.Second,
		Seed:        seed,
		RealTime:    false,
		LogScenarios: []LogScenarioCfg{
			{
				Path:            "builtin/simple",
				Scale:           scale,
				LogsPerInterval: logsPerInterval,
				Needles:         needles,
			},
		},
	}
}

func runLogsReceiver(t *testing.T, cfg *Config) []plog.Logs {
	sink := new(consumertest.LogsSink)
	factory := NewFactory()
	rcv, err := factory.CreateLogs(context.Background(), receivertest.NewNopSettings(typ), cfg, sink)
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), nil))

	// 2 intervals * scale * logsPerInterval
	expectedLogs := 2 * cfg.LogScenarios[0].Scale * cfg.LogScenarios[0].LogsPerInterval
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int(expectedLogs), sink.LogRecordCount())
	}, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, rcv.Shutdown(context.Background()))
	return sink.AllLogs()
}

func marshalLogsToJSON(logs []plog.Logs) (string, error) {
	combined := plog.NewLogs()
	for _, batch := range logs {
		for i := 0; i < batch.ResourceLogs().Len(); i++ {
			batch.ResourceLogs().At(i).CopyTo(combined.ResourceLogs().AppendEmpty())
		}
	}
	marshaler := &plog.JSONMarshaler{}
	data, err := marshaler.MarshalLogs(combined)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return "", err
	}
	// Re-marshal for deterministic key ordering
	out, err := json.Marshal(m)
	return string(out), err
}

func TestLogsGenReceiver_Deterministic(t *testing.T) {
	cfg := testLogsConfig(42, 2, 3, nil)
	logs1 := runLogsReceiver(t, cfg)
	logs2 := runLogsReceiver(t, cfg)

	json1, err := marshalLogsToJSON(logs1)
	require.NoError(t, err)
	json2, err := marshalLogsToJSON(logs2)
	require.NoError(t, err)
	assert.Equal(t, json1, json2, "same seed and config must produce identical log output")
}

func TestLogsGenReceiver_NeedleDeterministic(t *testing.T) {
	cfg := testLogsConfig(42, 1, 50, []NeedleCfg{
		{Name: "test-needle", Message: "NEEDLE_INJECTED", Rate: 0.1, Severity: "ERROR"},
	})
	logs1 := runLogsReceiver(t, cfg)
	logs2 := runLogsReceiver(t, cfg)

	// Extract which log indices have the needle in each run
	needleIndices := func(logs []plog.Logs) []int {
		var out []int
		idx := 0
		for _, batch := range logs {
			for i := 0; i < batch.ResourceLogs().Len(); i++ {
				rl := batch.ResourceLogs().At(i)
				for j := 0; j < rl.ScopeLogs().Len(); j++ {
					sl := rl.ScopeLogs().At(j)
					for k := 0; k < sl.LogRecords().Len(); k++ {
						lr := sl.LogRecords().At(k)
						if v, ok := lr.Attributes().Get("needle.name"); ok && v.Str() == "test-needle" {
							out = append(out, idx)
						}
						idx++
					}
				}
			}
		}
		return out
	}
	indices1 := needleIndices(logs1)
	indices2 := needleIndices(logs2)
	assert.Equal(t, indices1, indices2, "needle must appear at same positions with same seed")
	assert.NotEmpty(t, indices1, "needle with rate 0.1 over 100 logs should appear at least once")
}

func TestLogsGenReceiver_Scale(t *testing.T) {
	cfg := testLogsConfig(42, 5, 2, nil)
	logs := runLogsReceiver(t, cfg)

	podNames := make(map[string]struct{})
	for _, batch := range logs {
		for i := 0; i < batch.ResourceLogs().Len(); i++ {
			rl := batch.ResourceLogs().At(i)
			if v, ok := rl.Resource().Attributes().Get("k8s.pod.name"); ok {
				podNames[v.Str()] = struct{}{}
			}
		}
	}
	assert.Len(t, podNames, 5, "scale=5 must produce 5 distinct k8s.pod.name values")
}

func TestLogsGenReceiver_DifferentSeeds(t *testing.T) {
	cfg1 := testLogsConfig(42, 1, 20, nil)
	cfg2 := testLogsConfig(99, 1, 20, nil)
	logs1 := runLogsReceiver(t, cfg1)
	logs2 := runLogsReceiver(t, cfg2)

	json1, err := marshalLogsToJSON(logs1)
	require.NoError(t, err)
	json2, err := marshalLogsToJSON(logs2)
	require.NoError(t, err)
	assert.NotEqual(t, json1, json2, "different seeds must produce different output")
}

func TestLogsGenReceiver_ExternalTemplate(t *testing.T) {
	dir := t.TempDir()
	// External template uses path + "-resource-attributes" for GetLogResources
	resourceAttrs := `resourceLogs:
  - resource:
      attributes:
        - key: service.name
          value:
            stringValue: "external-{{.InstanceID}}"
        - key: k8s.pod.name
          value:
            stringValue: "ext-pod-{{.InstanceID}}"
    scopeLogs:
      - scope:
          name: "log-generator"
        logRecords: []
`
	templatePath := filepath.Join(dir, "custom-resource-attributes.yaml")
	require.NoError(t, os.WriteFile(templatePath, []byte(resourceAttrs), 0600))

	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := &Config{
		StartTime:   startTime,
		EndTime:     startTime.Add(2 * time.Second),
		Interval:    1 * time.Second,
		Seed:        42,
		RealTime:    false,
		LogScenarios: []LogScenarioCfg{
			{
				Path:            filepath.Join(dir, "custom"),
				Scale:           2,
				LogsPerInterval: 2,
			},
		},
	}
	require.NoError(t, cfg.Validate())

	logs := runLogsReceiver(t, cfg)
	require.NotEmpty(t, logs)

	// Verify we got logs from the external template (resource attributes from template)
	serviceNames := make(map[string]struct{})
	podNames := make(map[string]struct{})
	for _, batch := range logs {
		for i := 0; i < batch.ResourceLogs().Len(); i++ {
			rl := batch.ResourceLogs().At(i)
			if v, ok := rl.Resource().Attributes().Get("service.name"); ok {
				serviceNames[v.Str()] = struct{}{}
			}
			if v, ok := rl.Resource().Attributes().Get("k8s.pod.name"); ok {
				podNames[v.Str()] = struct{}{}
			}
		}
	}
	assert.Contains(t, serviceNames, "external-0", "external template must produce service.name from template")
	assert.Contains(t, serviceNames, "external-1", "external template must produce service.name from template")
	assert.Contains(t, podNames, "ext-pod-0", "external template must produce k8s.pod.name from template")
	assert.Contains(t, podNames, "ext-pod-1", "external template must produce k8s.pod.name from template")
}

func TestResolveVolumeMultiplier_NilProfile(t *testing.T) {
	vs := volumeState{}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 100; i++ {
		assert.Equal(t, 1.0, resolveVolumeMultiplier(&vs, rng, nil))
	}
}

func TestResolveVolumeMultiplier_BurstAndQuiet(t *testing.T) {
	vp := &VolumeProfileCfg{
		BurstProbability:   1.0, // always burst
		BurstMultiplierMin: 3.0,
		BurstMultiplierMax: 3.0,
		BurstDurationMin:   3,
		BurstDurationMax:   3,
	}
	vs := volumeState{}
	rng := rand.New(rand.NewSource(42))

	// First call triggers a burst with duration 3 (remainingIntervals = 2 after first call)
	m := resolveVolumeMultiplier(&vs, rng, vp)
	assert.Equal(t, 3.0, m)
	assert.Equal(t, 2, vs.remainingIntervals)

	// Next 2 calls continue the burst
	m = resolveVolumeMultiplier(&vs, rng, vp)
	assert.Equal(t, 3.0, m)
	assert.Equal(t, 1, vs.remainingIntervals)

	m = resolveVolumeMultiplier(&vs, rng, vp)
	assert.Equal(t, 3.0, m)
	assert.Equal(t, 0, vs.remainingIntervals)

	// Burst expired, next call rolls again (still probability=1.0, so new burst)
	m = resolveVolumeMultiplier(&vs, rng, vp)
	assert.Equal(t, 3.0, m)
}

func TestResolveVolumeMultiplier_QuietPeriod(t *testing.T) {
	vp := &VolumeProfileCfg{
		BurstProbability:   0.0,
		QuietProbability:   1.0, // always quiet
		QuietMultiplier:    0.2,
		QuietDurationMin:   2,
		QuietDurationMax:   2,
	}
	vs := volumeState{}
	rng := rand.New(rand.NewSource(42))

	m := resolveVolumeMultiplier(&vs, rng, vp)
	assert.Equal(t, 0.2, m)
	assert.Equal(t, 1, vs.remainingIntervals)

	m = resolveVolumeMultiplier(&vs, rng, vp)
	assert.Equal(t, 0.2, m)
	assert.Equal(t, 0, vs.remainingIntervals)
}

func TestResolveVolumeMultiplier_Deterministic(t *testing.T) {
	vp := &VolumeProfileCfg{
		BurstProbability:   0.3,
		BurstMultiplierMin: 2.0,
		BurstMultiplierMax: 5.0,
		BurstDurationMin:   1,
		BurstDurationMax:   4,
		QuietProbability:   0.2,
		QuietMultiplier:    0.1,
		QuietDurationMin:   1,
		QuietDurationMax:   3,
	}

	run := func(seed int64) []float64 {
		vs := volumeState{}
		rng := rand.New(rand.NewSource(seed))
		results := make([]float64, 50)
		for i := range results {
			results[i] = resolveVolumeMultiplier(&vs, rng, vp)
		}
		return results
	}

	assert.Equal(t, run(42), run(42), "same seed must produce same sequence")
	assert.NotEqual(t, run(42), run(99), "different seeds must produce different sequences")
}

func TestLogsGenReceiver_VolumeProfile_VariableVolume(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := &Config{
		StartTime: startTime,
		EndTime:   startTime.Add(20 * time.Second),
		Interval:  1 * time.Second,
		Seed:      42,
		RealTime:  false,
		LogScenarios: []LogScenarioCfg{
			{
				Path:            "builtin/simple",
				Scale:           1,
				LogsPerInterval: 10,
				VolumeProfile: &VolumeProfileCfg{
					BurstProbability:   0.3,
					BurstMultiplierMin: 3.0,
					BurstMultiplierMax: 5.0,
					BurstDurationMin:   1,
					BurstDurationMax:   3,
					QuietProbability:   0.2,
					QuietMultiplier:    0.2,
					QuietDurationMin:   1,
					QuietDurationMax:   2,
				},
			},
		},
	}
	require.NoError(t, cfg.Validate())

	sink := new(consumertest.LogsSink)
	factory := NewFactory()
	rcv, err := factory.CreateLogs(context.Background(), receivertest.NewNopSettings(typ), cfg, sink)
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), nil))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, sink.LogRecordCount(), 0)
	}, 5*time.Second, 50*time.Millisecond)

	// Wait for generation to complete (20 intervals)
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, rcv.Shutdown(context.Background()))

	// With volume_profile, per-batch log counts should vary (not all equal to 10).
	batchCounts := make(map[int]bool)
	for _, batch := range sink.AllLogs() {
		batchCounts[batch.LogRecordCount()] = true
	}
	assert.Greater(t, len(batchCounts), 1,
		"volume_profile should produce varying log counts per batch, got counts: %v", batchCounts)
}

func TestLogsGenReceiver_TraceContext_OnlyGoApp(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := &Config{
		StartTime: startTime,
		EndTime:   startTime.Add(2 * time.Second),
		Interval:  1 * time.Second,
		Seed:      42,
		RealTime:  false,
		LogScenarios: []LogScenarioCfg{
			{Path: "builtin/k8s-nginx", Scale: 1, LogsPerInterval: 5},
			{Path: "builtin/k8s-goapp", Scale: 1, LogsPerInterval: 5, EmitTraceContext: true},
		},
	}
	require.NoError(t, cfg.Validate())

	sink := new(consumertest.LogsSink)
	factory := NewFactory()
	rcv, err := factory.CreateLogs(context.Background(), receivertest.NewNopSettings(typ), cfg, sink)
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), nil))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 20, sink.LogRecordCount()) // 2 scenarios × 1 pod × 5 logs × 2 intervals
	}, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, rcv.Shutdown(context.Background()))

	var emptyTraceID [16]byte
	var emptySpanID [8]byte

	for _, batch := range sink.AllLogs() {
		for i := 0; i < batch.ResourceLogs().Len(); i++ {
			rl := batch.ResourceLogs().At(i)
			svcName := ""
			if v, ok := rl.Resource().Attributes().Get("service.name"); ok {
				svcName = v.Str()
			}
			isGoApp := svcName != "nginx"
			for j := 0; j < rl.ScopeLogs().Len(); j++ {
				sl := rl.ScopeLogs().At(j)
				for k := 0; k < sl.LogRecords().Len(); k++ {
					lr := sl.LogRecords().At(k)
					if isGoApp {
						assert.NotEqual(t, emptyTraceID, [16]byte(lr.TraceID()),
							"k8s-goapp logs (service=%s) must have trace_id set", svcName)
						assert.NotEqual(t, emptySpanID, [8]byte(lr.SpanID()),
							"k8s-goapp logs (service=%s) must have span_id set", svcName)
					} else {
						assert.Equal(t, emptyTraceID, [16]byte(lr.TraceID()),
							"nginx logs must not have trace_id")
						assert.Equal(t, emptySpanID, [8]byte(lr.SpanID()),
							"nginx logs must not have span_id")
					}
				}
			}
		}
	}
}

func runLogsReceiverUntilDone(t *testing.T, cfg *Config) []plog.Logs {
	sink := new(consumertest.LogsSink)
	factory := NewFactory()
	rcv, err := factory.CreateLogs(context.Background(), receivertest.NewNopSettings(typ), cfg, sink)
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), nil))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, sink.LogRecordCount(), 0)
	}, 5*time.Second, 50*time.Millisecond)
	// Allow generation to finish
	time.Sleep(300 * time.Millisecond)
	require.NoError(t, rcv.Shutdown(context.Background()))
	return sink.AllLogs()
}

func TestLogsGenReceiver_VolumeProfile_Deterministic(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	makeCfg := func() *Config {
		return &Config{
			StartTime: startTime,
			EndTime:   startTime.Add(5 * time.Second),
			Interval:  1 * time.Second,
			Seed:      42,
			RealTime:  false,
			LogScenarios: []LogScenarioCfg{
				{
					Path:            "builtin/simple",
					Scale:           1,
					LogsPerInterval: 10,
					VolumeProfile: &VolumeProfileCfg{
						BurstProbability:   0.3,
						BurstMultiplierMin: 2.0,
						BurstMultiplierMax: 4.0,
						BurstDurationMin:   1,
						BurstDurationMax:   2,
						QuietProbability:   0.2,
						QuietMultiplier:    0.3,
						QuietDurationMin:   1,
						QuietDurationMax:   2,
					},
				},
			},
		}
	}

	logs1 := runLogsReceiverUntilDone(t, makeCfg())
	logs2 := runLogsReceiverUntilDone(t, makeCfg())

	json1, err := marshalLogsToJSON(logs1)
	require.NoError(t, err)
	json2, err := marshalLogsToJSON(logs2)
	require.NoError(t, err)
	assert.Equal(t, json1, json2, "same seed and volume_profile must produce identical output")
}

func TestDiurnalMultiplier(t *testing.T) {
	t.Run("nil config returns 1.0", func(t *testing.T) {
		assert.Equal(t, 1.0, diurnalMultiplier(time.Now(), nil))
	})

	t.Run("cosine curve at peak hour", func(t *testing.T) {
		cfg := &DiurnalProfileCfg{
			PeakHour:        14,
			TroughHour:      4,
			PeakMultiplier:  3.0,
			TroughMultiplier: 0.2,
		}
		tPeak := time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)
		assert.InDelta(t, 3.0, diurnalMultiplier(tPeak, cfg), 0.001)
	})

	t.Run("cosine curve at trough hour", func(t *testing.T) {
		cfg := &DiurnalProfileCfg{
			PeakHour:        14,
			TroughHour:      4,
			PeakMultiplier:  3.0,
			TroughMultiplier: 0.2,
		}
		tTrough := time.Date(2024, 1, 15, 4, 0, 0, 0, time.UTC)
		assert.InDelta(t, 0.2, diurnalMultiplier(tTrough, cfg), 0.001)
	})

	t.Run("cosine curve midpoint", func(t *testing.T) {
		cfg := &DiurnalProfileCfg{
			PeakHour:        14,
			TroughHour:      4,
			PeakMultiplier:  3.0,
			TroughMultiplier: 0.2,
		}
		tMid := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
		midpoint := (3.0 + 0.2) / 2
		assert.InDelta(t, midpoint, diurnalMultiplier(tMid, cfg), 0.1)
	})

	t.Run("cron burst overrides when higher", func(t *testing.T) {
		cfg := &DiurnalProfileCfg{
			PeakHour:        14,
			TroughHour:      4,
			PeakMultiplier:  3.0,
			TroughMultiplier: 0.2,
			CronBursts: []CronBurstCfg{
				{Interval: 15 * time.Minute, Multiplier: 5.0, Duration: 1 * time.Minute},
			},
		}
		tBurst := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, 5.0, diurnalMultiplier(tBurst, cfg))
	})

	t.Run("cron burst does not override when diurnal higher", func(t *testing.T) {
		cfg := &DiurnalProfileCfg{
			PeakHour:        14,
			TroughHour:      4,
			PeakMultiplier:  10.0,
			TroughMultiplier: 0.2,
			CronBursts: []CronBurstCfg{
				{Interval: 15 * time.Minute, Multiplier: 5.0, Duration: 1 * time.Minute},
			},
		}
		tPeak := time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)
		assert.InDelta(t, 10.0, diurnalMultiplier(tPeak, cfg), 0.001)
	})

	t.Run("deterministic same time same result", func(t *testing.T) {
		cfg := &DiurnalProfileCfg{
			PeakHour:        14,
			TroughHour:      4,
			PeakMultiplier:  3.0,
			TroughMultiplier: 0.2,
		}
		t0 := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
		m1 := diurnalMultiplier(t0, cfg)
		m2 := diurnalMultiplier(t0, cfg)
		assert.Equal(t, m1, m2)
	})
}
