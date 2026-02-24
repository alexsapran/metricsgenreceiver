package loggen

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestAllProfiles(t *testing.T) {
	profiles := []struct {
		name    string
		profile *AppProfile
	}{
		{"nginx", NginxProfile(nil)},
		{"mysql", MySQLProfile(nil)},
		{"redis", RedisProfile(nil)},
		{"goapp", GoAppProfile()},
	}
	rng := rand.New(rand.NewSource(42))
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	for _, p := range profiles {
		t.Run(p.name, func(t *testing.T) {
			require.NotNil(t, p.profile)
			body, sev, attrs := GenerateLogRecord(rng, *p.profile, ts)
			assert.NotEmpty(t, body, "body must be non-empty")
			assert.True(t, sev >= plog.SeverityNumberUnspecified && sev <= plog.SeverityNumberFatal,
				"severity must be valid")
			_ = attrs // may be nil
		})
	}
}

func TestGenerateLogRecord_Deterministic(t *testing.T) {
	profile := *NginxProfile(nil)
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	rng1 := rand.New(rand.NewSource(42))
	rng2 := rand.New(rand.NewSource(42))

	// Generate 100 records from each RNG
	for i := 0; i < 100; i++ {
		body1, sev1, attrs1 := GenerateLogRecord(rng1, profile, ts)
		body2, sev2, attrs2 := GenerateLogRecord(rng2, profile, ts)
		assert.Equal(t, body1, body2, "record %d: body must match", i)
		assert.Equal(t, sev1, sev2, "record %d: severity must match", i)
		assert.Equal(t, attrs1, attrs2, "record %d: attrs must match", i)
	}
}

func TestSeverityDistribution(t *testing.T) {
	profile := *NginxProfile(nil)
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(999))

	const n = 10000
	counts := map[plog.SeverityNumber]int{}

	for i := 0; i < n; i++ {
		_, sev, _ := GenerateLogRecord(rng, profile, ts)
		counts[sev]++
	}

	// DefaultSeverityWeights [0, 2, 87, 94, 99, 100]:
	// TRACE ~0%, DEBUG ~2%, INFO ~85%, WARN ~7%, ERROR ~5%, FATAL ~1%
	assert.InDelta(t, 0.02, float64(counts[plog.SeverityNumberDebug])/n, 0.02, "DEBUG ~2%%")
	assert.InDelta(t, 0.85, float64(counts[plog.SeverityNumberInfo])/n, 0.05, "INFO ~85%%")
	assert.InDelta(t, 0.07, float64(counts[plog.SeverityNumberWarn])/n, 0.05, "WARN ~7%%")
	assert.InDelta(t, 0.05, float64(counts[plog.SeverityNumberError])/n, 0.03, "ERROR ~5%%")
	assert.InDelta(t, 0.01, float64(counts[plog.SeverityNumberFatal])/n, 0.02, "FATAL ~1%%")
}

func TestParseSeverity(t *testing.T) {
	assert.Equal(t, plog.SeverityNumberTrace, ParseSeverity("TRACE"))
	assert.Equal(t, plog.SeverityNumberTrace, ParseSeverity("trace"))
	assert.Equal(t, plog.SeverityNumberDebug, ParseSeverity("DEBUG"))
	assert.Equal(t, plog.SeverityNumberDebug, ParseSeverity("debug"))
	assert.Equal(t, plog.SeverityNumberInfo, ParseSeverity("INFO"))
	assert.Equal(t, plog.SeverityNumberInfo, ParseSeverity("info"))
	assert.Equal(t, plog.SeverityNumberWarn, ParseSeverity("WARN"))
	assert.Equal(t, plog.SeverityNumberError, ParseSeverity("ERROR"))
	assert.Equal(t, plog.SeverityNumberFatal, ParseSeverity("FATAL"))
	assert.Equal(t, plog.SeverityNumberError, ParseSeverity(""))      // default
	assert.Equal(t, plog.SeverityNumberError, ParseSeverity("unknown")) // default
}

func TestGenericProfile(t *testing.T) {
	profile := GenericProfile("my-service")
	require.NotNil(t, profile)
	assert.Equal(t, "generic", profile.Name)
	rng := rand.New(rand.NewSource(1))
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	body, sev, _ := GenerateLogRecord(rng, *profile, ts)
	assert.NotEmpty(t, body)
	assert.Contains(t, body, "my-service")
	assert.Equal(t, plog.SeverityNumberInfo, sev)
}
