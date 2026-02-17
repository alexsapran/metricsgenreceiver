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
	Path            string         `mapstructure:"path"`
	Scale           int            `mapstructure:"scale"`
	Concurrency     int            `mapstructure:"concurrency"`
	TemplateVars    map[string]any `mapstructure:"template_vars"`
	LogsPerInterval int            `mapstructure:"logs_per_interval"`
	Needles         []NeedleCfg    `mapstructure:"needles"`
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
				valid := sev == "INFO" || sev == "WARN" || sev == "ERROR" || sev == "FATAL"
				if !valid {
					return fmt.Errorf("log_scenarios: needle %q severity must be INFO, WARN, ERROR, or FATAL", needle.Name)
				}
			}
		}
	}
	return nil
}
