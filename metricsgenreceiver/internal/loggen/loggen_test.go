package loggen

import (
	"math"
	"math/rand"
	"regexp"
	"sort"
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
		{"nginx", NginxProfile(nil, nil)},
		{"mysql", MySQLProfile(nil, nil)},
		{"redis", RedisProfile(nil, nil)},
		{"goapp", GoAppProfile(nil)},
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
	profile := *NginxProfile(nil, nil)
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
	profile := *NginxProfile(nil, nil)
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(999))

	const n = 10000
	counts := map[plog.SeverityNumber]int{}

	for i := 0; i < n; i++ {
		_, sev, _ := GenerateLogRecord(rng, profile, ts)
		counts[sev]++
	}

	// DefaultSeverityWeights [0, 3, 85, 93, 100, 100]:
	// TRACE ~0%, DEBUG ~3%, INFO ~82%, WARN ~8%, ERROR ~7%, FATAL ~0%
	assert.InDelta(t, 0.03, float64(counts[plog.SeverityNumberDebug])/n, 0.02, "DEBUG ~3%%")
	assert.InDelta(t, 0.82, float64(counts[plog.SeverityNumberInfo])/n, 0.05, "INFO ~82%%")
	assert.InDelta(t, 0.08, float64(counts[plog.SeverityNumberWarn])/n, 0.05, "WARN ~8%%")
	assert.InDelta(t, 0.07, float64(counts[plog.SeverityNumberError])/n, 0.03, "ERROR ~7%%")
	assert.Equal(t, 0, counts[plog.SeverityNumberFatal], "FATAL ~0%%")
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

func TestAttrsOrderDeterminism(t *testing.T) {
	tsLayout := "2006-01-02T15:04:05Z"
	profile := AppProfile{
		Name:            "determinism-test",
		ScopeName:       "test",
		SeverityWeights: [6]int{0, 0, 100, 100, 100, 100},
		Messages: []MessageTemplate{
			{
				Severity: plog.SeverityNumberInfo,
				Format:   "test at %s",
				Args:     []ArgGenerator{Timestamp(tsLayout)},
				Attrs: []AttrGen{
					{"a", RandomFrom("a1", "a2", "a3", "a4", "a5")},
					{"b", RandomInt(1, 1000)},
					{"c", RandomFrom("c1", "c2", "c3")},
					{"d", RandomInt(1, 100)},
					{"e", RandomFrom("e1", "e2")},
				},
			},
		},
	}
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	const iterations = 500
	for run := 0; run < 5; run++ {
		rng1 := rand.New(rand.NewSource(12345))
		rng2 := rand.New(rand.NewSource(12345))
		for i := 0; i < iterations; i++ {
			body1, sev1, attrs1 := GenerateLogRecord(rng1, profile, ts)
			body2, sev2, attrs2 := GenerateLogRecord(rng2, profile, ts)
			assert.Equal(t, body1, body2, "run %d record %d: body", run, i)
			assert.Equal(t, sev1, sev2, "run %d record %d: severity", run, i)
			assert.Equal(t, attrs1, attrs2, "run %d record %d: attrs", run, i)
		}
	}
}

func TestPreparedProfileDeterminism(t *testing.T) {
	profiles := []*AppProfile{
		NginxProfile(rand.New(rand.NewSource(0)), nil),
		MySQLProfile(rand.New(rand.NewSource(0)), nil),
		RedisProfile(rand.New(rand.NewSource(0)), nil),
		GoAppProfile(rand.New(rand.NewSource(0))),
		ProxyProfile(rand.New(rand.NewSource(0)), nil),
	}
	ts := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

	for _, p := range profiles {
		t.Run(p.Name, func(t *testing.T) {
			pp := PrepareProfile(p)

			const iterations = 200
			for run := 0; run < 3; run++ {
				rng1 := rand.New(rand.NewSource(77))
				rng2 := rand.New(rand.NewSource(77))
				attrs1 := make(map[string]any, 8)
				attrs2 := make(map[string]any, 8)
				argsBuf1 := make([]any, pp.MaxArgs())
				argsBuf2 := make([]any, pp.MaxArgs())
				var bodyBuf1, bodyBuf2 []byte
				for i := 0; i < iterations; i++ {
					var body1, body2 string
					var sev1, sev2 plog.SeverityNumber
					body1, sev1, bodyBuf1 = GenerateFromPreparedInto(rng1, pp, ts, attrs1, argsBuf1, bodyBuf1)
					body2, sev2, bodyBuf2 = GenerateFromPreparedInto(rng2, pp, ts, attrs2, argsBuf2, bodyBuf2)
					assert.Equal(t, body1, body2, "run %d record %d: body", run, i)
					assert.Equal(t, sev1, sev2, "run %d record %d: severity", run, i)
					for k, v := range attrs1 {
						assert.Equal(t, v, attrs2[k], "run %d record %d: attr %s", run, i, k)
					}
					assert.Equal(t, len(attrs1), len(attrs2), "run %d record %d: attr count", run, i)
				}
			}
		})
	}
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

// Tests for ArgGenerator helpers (UUID, LogNormalInt, OptionalAttr, etc.)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestRandomUUID_Format(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ctx := GenContext{Timestamp: time.Now()}
	for i := 0; i < 1000; i++ {
		val := RandomUUID(rng, ctx)
		s, ok := val.(string)
		require.True(t, ok, "RandomUUID must return string")
		assert.Len(t, s, 36)
		assert.Regexp(t, uuidRe, s, "must be valid v4 UUID: %s", s)
	}
}

func TestRandomUUID_Determinism(t *testing.T) {
	rng1 := rand.New(rand.NewSource(99))
	rng2 := rand.New(rand.NewSource(99))
	ctx := GenContext{}
	for i := 0; i < 500; i++ {
		assert.Equal(t, RandomUUID(rng1, ctx), RandomUUID(rng2, ctx), "record %d", i)
	}
}

func TestRandomUUID_Uniqueness(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ctx := GenContext{}
	seen := make(map[string]struct{}, 10000)
	for i := 0; i < 10000; i++ {
		s := RandomUUID(rng, ctx).(string)
		_, dup := seen[s]
		assert.False(t, dup, "duplicate UUID at iteration %d: %s", i, s)
		seen[s] = struct{}{}
	}
}

func TestLogNormalInt_Distribution(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ctx := GenContext{}
	gen := LogNormalInt(100, 0.5)

	const n = 50000
	vals := make([]int, n)
	sum := 0
	for i := 0; i < n; i++ {
		v := gen(rng, ctx).(int)
		assert.GreaterOrEqual(t, v, 0, "LogNormalInt must be non-negative")
		vals[i] = v
		sum += v
	}

	sort.Ints(vals)
	median := vals[n/2]
	assert.InDelta(t, 100, median, 15, "median should be near 100")

	mean := float64(sum) / float64(n)
	expectedMean := 100 * math.Exp(0.5*0.5/2)
	assert.InDelta(t, expectedMean, mean, 10, "mean should match log-normal expectation")
}

func TestLogNormalInt_Determinism(t *testing.T) {
	gen := LogNormalInt(500, 1.0)
	rng1 := rand.New(rand.NewSource(77))
	rng2 := rand.New(rand.NewSource(77))
	ctx := GenContext{}
	for i := 0; i < 500; i++ {
		assert.Equal(t, gen(rng1, ctx), gen(rng2, ctx), "record %d", i)
	}
}

func TestOptionalAttr_AlwaysConsumesRng(t *testing.T) {
	inner := RandomInt(0, 1000)
	gen := OptionalAttr(0.5, inner)
	rng1 := rand.New(rand.NewSource(42))
	rng2 := rand.New(rand.NewSource(42))
	ctx := GenContext{}

	for i := 0; i < 500; i++ {
		v1 := gen(rng1, ctx)
		v2 := gen(rng2, ctx)
		assert.Equal(t, v1, v2, "record %d: must be deterministic", i)
	}
}

func TestOptionalAttr_ProbabilityDistribution(t *testing.T) {
	gen := OptionalAttr(0.3, Static("present"))
	rng := rand.New(rand.NewSource(42))
	ctx := GenContext{}

	const n = 10000
	nilCount := 0
	for i := 0; i < n; i++ {
		if gen(rng, ctx) == nil {
			nilCount++
		}
	}

	rate := float64(n-nilCount) / float64(n)
	assert.InDelta(t, 0.3, rate, 0.03, "~30%% should be non-nil")
}

func TestOptionalAttr_NilValuesSkippedInGenerate(t *testing.T) {
	profile := AppProfile{
		Name:            "optional-test",
		ScopeName:       "test",
		SeverityWeights: [6]int{0, 0, 100, 100, 100, 100},
		Messages: []MessageTemplate{
			{
				Severity: plog.SeverityNumberInfo,
				Format:   "test",
				Attrs: []AttrGen{
					{"always", Static("yes")},
					{"sometimes", OptionalAttr(0.0, Static("no"))}, // 0% = always nil
				},
			},
		},
	}
	rng := rand.New(rand.NewSource(42))
	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 100; i++ {
		_, _, attrs := GenerateLogRecord(rng, profile, ts)
		assert.Equal(t, "yes", attrs["always"])
		_, hasSometimes := attrs["sometimes"]
		assert.False(t, hasSometimes, "nil attrs should be omitted")
	}
}

func TestSliceAttr_Length(t *testing.T) {
	gen := SliceAttr(RandomInt(1, 100), 2, 5)
	rng := rand.New(rand.NewSource(42))
	ctx := GenContext{}

	for i := 0; i < 500; i++ {
		val := gen(rng, ctx)
		sl, ok := val.([]any)
		require.True(t, ok, "SliceAttr must return []any")
		assert.GreaterOrEqual(t, len(sl), 2)
		assert.LessOrEqual(t, len(sl), 5)
		for _, elem := range sl {
			v, ok := elem.(int)
			require.True(t, ok, "elements should be int")
			assert.GreaterOrEqual(t, v, 1)
			assert.LessOrEqual(t, v, 100)
		}
	}
}

func TestSliceAttr_Determinism(t *testing.T) {
	gen := SliceAttr(RandomFrom("a", "b", "c"), 1, 4)
	rng1 := rand.New(rand.NewSource(42))
	rng2 := rand.New(rand.NewSource(42))
	ctx := GenContext{}

	for i := 0; i < 500; i++ {
		assert.Equal(t, gen(rng1, ctx), gen(rng2, ctx), "record %d", i)
	}
}

func TestRareAttrs_DeterministicAndConditional(t *testing.T) {
	profile := AppProfile{
		Name:            "rare-test",
		ScopeName:       "test",
		SeverityWeights: [6]int{0, 0, 100, 100, 100, 100},
		Messages: []MessageTemplate{
			{
				Severity: plog.SeverityNumberInfo,
				Format:   "test",
				RareAttrs: []RareAttrGen{
					{"rare_field", 0.01, RandomFrom("r1", "r2", "r3")},
				},
			},
		},
	}
	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	const n = 10000
	rng1 := rand.New(rand.NewSource(42))
	rng2 := rand.New(rand.NewSource(42))
	emitCount := 0
	for i := 0; i < n; i++ {
		_, _, a1 := GenerateLogRecord(rng1, profile, ts)
		_, _, a2 := GenerateLogRecord(rng2, profile, ts)
		assert.Equal(t, a1, a2, "record %d: must be deterministic", i)
		if _, ok := a1["rare_field"]; ok {
			emitCount++
		}
	}
	rate := float64(emitCount) / float64(n)
	assert.InDelta(t, 0.01, rate, 0.015, "~1%% should be emitted")
}

func TestRareAttrs_InPreparedProfile(t *testing.T) {
	profile := &AppProfile{
		Name:            "rare-prepared-test",
		ScopeName:       "test",
		SeverityWeights: [6]int{0, 0, 100, 100, 100, 100},
		Messages: []MessageTemplate{
			{
				Severity: plog.SeverityNumberInfo,
				Format:   "test",
				RareAttrs: []RareAttrGen{
					{"diag", 0.05, RandomInt(1, 999)},
				},
			},
		},
	}
	pp := PrepareProfile(profile)
	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	rng1 := rand.New(rand.NewSource(42))
	rng2 := rand.New(rand.NewSource(42))
	attrs1 := make(map[string]any, 4)
	attrs2 := make(map[string]any, 4)
	argsBuf1 := make([]any, pp.MaxArgs())
	argsBuf2 := make([]any, pp.MaxArgs())
	var bodyBuf1, bodyBuf2 []byte

	const n = 5000
	emitCount := 0
	for i := 0; i < n; i++ {
		var body1, body2 string
		var sev1, sev2 plog.SeverityNumber
		body1, sev1, bodyBuf1 = GenerateFromPreparedInto(rng1, pp, ts, attrs1, argsBuf1, bodyBuf1)
		body2, sev2, bodyBuf2 = GenerateFromPreparedInto(rng2, pp, ts, attrs2, argsBuf2, bodyBuf2)
		assert.Equal(t, body1, body2, "record %d: body", i)
		assert.Equal(t, sev1, sev2, "record %d: severity", i)
		assert.Equal(t, len(attrs1), len(attrs2), "record %d: attr count", i)
		for k, v := range attrs1 {
			assert.Equal(t, v, attrs2[k], "record %d: attr %s", i, k)
		}
		if _, ok := attrs1["diag"]; ok {
			emitCount++
		}
	}
	rate := float64(emitCount) / float64(n)
	assert.InDelta(t, 0.05, rate, 0.03, "~5%% should be emitted")
}
