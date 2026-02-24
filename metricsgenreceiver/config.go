package metricsgenreceiver

import (
	"fmt"
	"strings"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/distribution"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

type Config struct {
	StartTime                         time.Time                    `mapstructure:"start_time"`
	StartNowMinus                     time.Duration                `mapstructure:"start_now_minus"`
	EndTime                           time.Time                    `mapstructure:"end_time"`
	EndNowMinus                       time.Duration                `mapstructure:"end_now_minus"`
	Interval                          time.Duration                `mapstructure:"interval"`
	IntervalJitterStdDev              time.Duration                `mapstructure:"interval_jitter_std_dev"`
	RealTime                          bool                         `mapstructure:"real_time"`
	ExitAfterEnd                      bool                         `mapstructure:"exit_after_end"`
	ExitAfterEndTimeout               time.Duration                `mapstructure:"exit_after_end_timeout"`
	Seed                              int64                        `mapstructure:"seed"`
	Scenarios                         []ScenarioCfg                `mapstructure:"scenarios"`
	LogScenarios                      []LogScenarioCfg             `mapstructure:"log_scenarios"`
	Distribution                      distribution.DistributionCfg `mapstructure:"distribution"`
	ExponentialHistogramsTemplatePath string                       `mapstructure:"exponential_histograms_template_path"`
}

type ScenarioCfg struct {
	Path                string         `mapstructure:"path"`
	Scale               int            `mapstructure:"scale"`
	Concurrency         int            `mapstructure:"concurrency"`
	Churn               int            `mapstructure:"churn"`
	TemplateVars        map[string]any `mapstructure:"template_vars"`
	TemporalityOverride string         `mapstructure:"temporality_override"`
	HistogramOverride   string         `mapstructure:"histogram_override"`
}

type LogScenarioCfg struct {
	Path             string            `mapstructure:"path"`
	Scale            int               `mapstructure:"scale"`
	Concurrency      int               `mapstructure:"concurrency"`
	TemplateVars     map[string]any    `mapstructure:"template_vars"`
	LogsPerInterval  int               `mapstructure:"logs_per_interval"`
	EmitTraceContext bool              `mapstructure:"emit_trace_context"`
	Needles          []NeedleCfg       `mapstructure:"needles"`
	VolumeProfile    *VolumeProfileCfg `mapstructure:"volume_profile"`
	// SeverityWeights overrides the profile's default severity distribution.
	// Cumulative percentages for [TRACE, DEBUG, INFO, WARN, ERROR, FATAL].
	// e.g. [0, 2, 87, 94, 99, 100] = 0% TRACE, 2% DEBUG, 85% INFO, 7% WARN, 5% ERROR, 1% FATAL.
	// If nil or all zeros, the profile default is used.
	SeverityWeights *[6]int `mapstructure:"severity_weights"`
}

type VolumeProfileCfg struct {
	BurstProbability  float64 `mapstructure:"burst_probability"`
	BurstMultiplierMin float64 `mapstructure:"burst_multiplier_min"`
	BurstMultiplierMax float64 `mapstructure:"burst_multiplier_max"`
	BurstDurationMin  int     `mapstructure:"burst_duration_min"`
	BurstDurationMax  int     `mapstructure:"burst_duration_max"`
	QuietProbability  float64 `mapstructure:"quiet_probability"`
	QuietMultiplier   float64 `mapstructure:"quiet_multiplier"`
	QuietDurationMin  int     `mapstructure:"quiet_duration_min"`
	QuietDurationMax  int     `mapstructure:"quiet_duration_max"`
}

type NeedleCfg struct {
	Name       string            `mapstructure:"name"`
	Message    string            `mapstructure:"message"`
	Rate       float64           `mapstructure:"rate"`
	Severity   string            `mapstructure:"severity"`
	Attributes map[string]string `mapstructure:"attributes"`
}

func (c ScenarioCfg) AggregationTemporalityOverride() pmetric.AggregationTemporality {
	switch c.TemporalityOverride {
	case "cumulative":
		return pmetric.AggregationTemporalityCumulative
	case "delta":
		return pmetric.AggregationTemporalityDelta
	default:
		return pmetric.AggregationTemporalityUnspecified
	}
}

func (c Config) GetExponentialHistogramsTemplatePath() string {
	if c.ExponentialHistogramsTemplatePath != "" {
		return c.ExponentialHistogramsTemplatePath
	}
	return "builtin/exponential-histograms-low-frequency.ndjson"
}

func (c ScenarioCfg) ForceExponentialHistograms() bool {
	return c.HistogramOverride == "exponential"
}

func createDefaultConfig() component.Config {
	return &Config{
		Seed:         0,
		Scenarios:    make([]ScenarioCfg, 0),
		LogScenarios: make([]LogScenarioCfg, 0),
		Distribution: distribution.DefaultDistribution,
	}
}

func (cfg *Config) Validate() error {
	if cfg.Interval.Seconds() < 1 {
		return fmt.Errorf("the interval has to be set to at least 1 second (1s)")
	}

	if cfg.StartTime.After(cfg.EndTime) {
		return fmt.Errorf("start_time must be before end_time")
	}

	for _, scn := range cfg.Scenarios {
		if scn.Concurrency != 0 && scn.Scale%scn.Concurrency != 0 {
			return fmt.Errorf("scale must be a multiple of concurrency")
		}
		if scn.Concurrency < 0 {
			return fmt.Errorf("concurrency must be a positive number")
		}
	}
	for _, scn := range cfg.LogScenarios {
		if scn.Scale < 0 {
			return fmt.Errorf("log_scenarios: scale must be non-negative")
		}
		if scn.Concurrency != 0 && scn.Scale > 0 && scn.Scale%scn.Concurrency != 0 {
			return fmt.Errorf("log_scenarios: scale must be a multiple of concurrency")
		}
		if scn.Concurrency < 0 {
			return fmt.Errorf("log_scenarios: concurrency must be non-negative")
		}
		if scn.LogsPerInterval < 0 {
			return fmt.Errorf("log_scenarios: logs_per_interval must be non-negative")
		}
		for _, needle := range scn.Needles {
			if needle.Name == "" {
				return fmt.Errorf("log_scenarios: needle name must not be empty")
			}
			if needle.Message == "" {
				return fmt.Errorf("log_scenarios: needle %q message must not be empty", needle.Name)
			}
			if needle.Rate < 0.0 || needle.Rate > 1.0 {
				return fmt.Errorf("log_scenarios: needle %q rate must be between 0.0 and 1.0", needle.Name)
			}
			sev := strings.ToUpper(strings.TrimSpace(needle.Severity))
			if sev != "" {
				valid := sev == "TRACE" || sev == "DEBUG" || sev == "INFO" || sev == "WARN" || sev == "ERROR" || sev == "FATAL"
				if !valid {
					return fmt.Errorf("log_scenarios: needle %q severity must be TRACE, DEBUG, INFO, WARN, ERROR, or FATAL", needle.Name)
				}
			}
		}
		if err := validateVolumeProfile(scn.VolumeProfile); err != nil {
			return fmt.Errorf("log_scenarios: %w", err)
		}
		if err := validateSeverityWeights(scn.SeverityWeights); err != nil {
			return fmt.Errorf("log_scenarios: %w", err)
		}
	}
	return nil
}

func validateSeverityWeights(sw *[6]int) error {
	if sw == nil {
		return nil
	}
	prev := 0
	for i, w := range sw {
		if w < prev {
			return fmt.Errorf("severity_weights: values must be non-decreasing (index %d: %d < %d)", i, w, prev)
		}
		if w < 0 || w > 100 {
			return fmt.Errorf("severity_weights: values must be between 0 and 100 (index %d: %d)", i, w)
		}
		prev = w
	}
	if sw[5] != 100 {
		return fmt.Errorf("severity_weights: last value must be 100 (got %d)", sw[5])
	}
	return nil
}

func validateVolumeProfile(vp *VolumeProfileCfg) error {
	if vp == nil {
		return nil
	}
	if vp.BurstProbability < 0 || vp.BurstProbability > 1 {
		return fmt.Errorf("volume_profile: burst_probability must be between 0.0 and 1.0")
	}
	if vp.QuietProbability < 0 || vp.QuietProbability > 1 {
		return fmt.Errorf("volume_profile: quiet_probability must be between 0.0 and 1.0")
	}
	if vp.BurstProbability+vp.QuietProbability > 1 {
		return fmt.Errorf("volume_profile: burst_probability + quiet_probability must not exceed 1.0")
	}
	if vp.BurstMultiplierMin < 0 {
		return fmt.Errorf("volume_profile: burst_multiplier_min must be non-negative")
	}
	if vp.BurstMultiplierMax < vp.BurstMultiplierMin {
		return fmt.Errorf("volume_profile: burst_multiplier_max must be >= burst_multiplier_min")
	}
	if vp.BurstDurationMin < 1 && vp.BurstProbability > 0 {
		return fmt.Errorf("volume_profile: burst_duration_min must be >= 1 when burst_probability > 0")
	}
	if vp.BurstDurationMax < vp.BurstDurationMin {
		return fmt.Errorf("volume_profile: burst_duration_max must be >= burst_duration_min")
	}
	if vp.QuietMultiplier < 0 {
		return fmt.Errorf("volume_profile: quiet_multiplier must be non-negative")
	}
	if vp.QuietDurationMin < 1 && vp.QuietProbability > 0 {
		return fmt.Errorf("volume_profile: quiet_duration_min must be >= 1 when quiet_probability > 0")
	}
	if vp.QuietDurationMax < vp.QuietDurationMin {
		return fmt.Errorf("volume_profile: quiet_duration_max must be >= quiet_duration_min")
	}
	return nil
}
