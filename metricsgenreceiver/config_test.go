package metricsgenreceiver

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/distribution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap/confmaptest"
	"go.opentelemetry.io/collector/confmap/xconfmap"
)

func TestLoadConfig(t *testing.T) {
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig().(*Config)
	sub, err := cm.Sub("metricsgen")
	require.NoError(t, err)
	require.NoError(t, sub.Unmarshal(cfg))

	assert.NoError(t, xconfmap.Validate(cfg))
	assert.Equal(t, testdataConfigYamlAsMap(), cfg)
}

func testdataConfigYamlAsMap() *Config {
	startTime, _ := time.Parse(time.RFC3339, "2024-12-17T00:00:00Z")
	endTime, _ := time.Parse(time.RFC3339, "2024-12-17T00:00:31Z")
	interval, _ := time.ParseDuration("30s")
	return &Config{
		StartTime:    startTime,
		EndTime:      endTime,
		Interval:     interval,
		Seed:         123,
		Scenarios:    []ScenarioCfg{{Path: "testdata/metricstemplate", Scale: 10}},
		LogScenarios: []LogScenarioCfg{},
		Distribution: distribution.DistributionCfg{
			MedianMonotonicSum: 100,
			StdDevGaugePct:     0.01,
			StdDev:             1.0,
		},
	}
}

func TestConfig_ExponentialHistogramsTemplatePath(t *testing.T) {
	t.Run("custom path", func(t *testing.T) {
		cfg := &Config{
			ExponentialHistogramsTemplatePath: "custom/path/histograms.ndjson",
		}
		assert.Equal(t, "custom/path/histograms.ndjson", cfg.GetExponentialHistogramsTemplatePath())
	})

	t.Run("default path when empty", func(t *testing.T) {
		cfg := &Config{
			ExponentialHistogramsTemplatePath: "",
		}
		assert.Equal(t, "builtin/exponential-histograms-low-frequency.ndjson", cfg.GetExponentialHistogramsTemplatePath())
	})

	t.Run("default path when not set", func(t *testing.T) {
		cfg := &Config{}
		assert.Equal(t, "builtin/exponential-histograms-low-frequency.ndjson", cfg.GetExponentialHistogramsTemplatePath())
	})
}

func TestScenarioCfg_ForceExponentialHistograms(t *testing.T) {
	t.Run("histogram_override set to exponential", func(t *testing.T) {
		scenario := ScenarioCfg{
			HistogramOverride: "exponential",
		}
		assert.True(t, scenario.ForceExponentialHistograms())
	})

	t.Run("histogram_override set to other value", func(t *testing.T) {
		scenario := ScenarioCfg{
			HistogramOverride: "normal",
		}
		assert.False(t, scenario.ForceExponentialHistograms())
	})

	t.Run("histogram_override empty", func(t *testing.T) {
		scenario := ScenarioCfg{
			HistogramOverride: "",
		}
		assert.False(t, scenario.ForceExponentialHistograms())
	})

	t.Run("histogram_override not set", func(t *testing.T) {
		scenario := ScenarioCfg{}
		assert.False(t, scenario.ForceExponentialHistograms())
	})
}

func validBaseConfig() *Config {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	return &Config{
		StartTime: start,
		EndTime:   start.Add(10 * time.Second),
		Interval:  1 * time.Second,
	}
}

func TestValidateVolumeProfile(t *testing.T) {
	t.Run("nil profile is valid", func(t *testing.T) {
		assert.NoError(t, validateVolumeProfile(nil))
	})

	t.Run("valid profile", func(t *testing.T) {
		assert.NoError(t, validateVolumeProfile(&VolumeProfileCfg{
			BurstProbability:   0.1,
			BurstMultiplierMin: 2.0,
			BurstMultiplierMax: 5.0,
			BurstDurationMin:   1,
			BurstDurationMax:   3,
			QuietProbability:   0.1,
			QuietMultiplier:    0.2,
			QuietDurationMin:   1,
			QuietDurationMax:   3,
		}))
	})

	t.Run("burst_probability out of range", func(t *testing.T) {
		assert.ErrorContains(t, validateVolumeProfile(&VolumeProfileCfg{
			BurstProbability: 1.5,
			BurstDurationMin: 1, BurstDurationMax: 1,
		}), "burst_probability")
	})

	t.Run("quiet_probability out of range", func(t *testing.T) {
		assert.ErrorContains(t, validateVolumeProfile(&VolumeProfileCfg{
			QuietProbability: -0.1,
			QuietDurationMin: 1, QuietDurationMax: 1,
		}), "quiet_probability")
	})

	t.Run("combined probability exceeds 1", func(t *testing.T) {
		assert.ErrorContains(t, validateVolumeProfile(&VolumeProfileCfg{
			BurstProbability: 0.6, BurstDurationMin: 1, BurstDurationMax: 1,
			QuietProbability: 0.6, QuietDurationMin: 1, QuietDurationMax: 1,
		}), "burst_probability + quiet_probability")
	})

	t.Run("burst_multiplier_max less than min", func(t *testing.T) {
		assert.ErrorContains(t, validateVolumeProfile(&VolumeProfileCfg{
			BurstProbability:   0.1,
			BurstMultiplierMin: 5.0,
			BurstMultiplierMax: 2.0,
			BurstDurationMin:   1,
			BurstDurationMax:   1,
		}), "burst_multiplier_max")
	})

	t.Run("burst_duration_min zero with positive probability", func(t *testing.T) {
		assert.ErrorContains(t, validateVolumeProfile(&VolumeProfileCfg{
			BurstProbability:   0.1,
			BurstMultiplierMin: 2.0,
			BurstMultiplierMax: 3.0,
			BurstDurationMin:   0,
			BurstDurationMax:   3,
		}), "burst_duration_min")
	})

	t.Run("quiet_duration_max less than min", func(t *testing.T) {
		assert.ErrorContains(t, validateVolumeProfile(&VolumeProfileCfg{
			QuietProbability: 0.1,
			QuietMultiplier:  0.2,
			QuietDurationMin: 5,
			QuietDurationMax: 2,
		}), "quiet_duration_max")
	})
}

func TestValidateDiurnalProfile(t *testing.T) {
	t.Run("nil profile is valid", func(t *testing.T) {
		assert.NoError(t, validateDiurnalProfile(nil))
	})

	t.Run("valid profile", func(t *testing.T) {
		assert.NoError(t, validateDiurnalProfile(&DiurnalProfileCfg{
			PeakHour:        14,
			TroughHour:      4,
			PeakMultiplier:  3.0,
			TroughMultiplier: 0.2,
		}))
	})

	t.Run("peak_hour out of range", func(t *testing.T) {
		assert.ErrorContains(t, validateDiurnalProfile(&DiurnalProfileCfg{
			PeakHour: 24, TroughHour: 4, PeakMultiplier: 3.0, TroughMultiplier: 0.2,
		}), "peak_hour")
	})

	t.Run("peak_hour equals trough_hour", func(t *testing.T) {
		assert.ErrorContains(t, validateDiurnalProfile(&DiurnalProfileCfg{
			PeakHour: 14, TroughHour: 14, PeakMultiplier: 3.0, TroughMultiplier: 0.2,
		}), "must differ")
	})

	t.Run("empty profile gets defaults", func(t *testing.T) {
		cfg := &DiurnalProfileCfg{PeakHour: 0, TroughHour: 0}
		require.NoError(t, validateDiurnalProfile(cfg))
		assert.Equal(t, 14, cfg.PeakHour)
		assert.Equal(t, 4, cfg.TroughHour)
		assert.Equal(t, 3.0, cfg.PeakMultiplier)
		assert.Equal(t, 0.2, cfg.TroughMultiplier)
	})

	t.Run("cron burst duration >= interval", func(t *testing.T) {
		assert.ErrorContains(t, validateDiurnalProfile(&DiurnalProfileCfg{
			PeakHour: 14, TroughHour: 4, PeakMultiplier: 3.0, TroughMultiplier: 0.2,
			CronBursts: []CronBurstCfg{
				{Interval: 15 * time.Minute, Multiplier: 5.0, Duration: 15 * time.Minute},
			},
		}), "duration must be < interval")
	})
}
