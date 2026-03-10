package loggen

import (
	"math/rand"
	"strconv"
	"strings"
)

// commonErrorMessages are realistic error messages seen across production
// services. Used by ErrorMessageAttrs to populate error.message at ~70%.
var commonErrorMessages = []string{
	"connection refused",
	"context deadline exceeded",
	"connection reset by peer",
	"i/o timeout",
	"TLS handshake timeout",
	"no such host",
	"broken pipe",
	"connection timed out",
	"request canceled",
	"EOF",
	"permission denied",
	"resource temporarily unavailable",
	"too many open files",
}

// buildLargeErrorMessage generates a single large error payload with a mix of
// realistic patterns: multi-line stack traces, JSON error responses, and
// connection error cascades.
func buildLargeErrorMessage(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)

	switch r.Intn(3) {
	case 0: // JSON error response
		b.WriteString(`{"error":{"root_cause":[{"type":"`)
		causes := [...]string{"search_phase_execution_exception", "index_not_found_exception", "mapper_parsing_exception", "resource_already_exists_exception"}
		b.WriteString(causes[r.Intn(len(causes))])
		b.WriteString(`","reason":"`)
		reasons := [...]string{
			"all shards failed", "no such index", "failed to parse field",
			"resource already exists", "circuit breaking exception: [request] Data too large",
		}
		b.WriteString(reasons[r.Intn(len(reasons))])
		b.WriteString(`"}],"type":"`)
		b.WriteString(causes[r.Intn(len(causes))])
		b.WriteString(`","reason":"`)
		b.WriteString(reasons[r.Intn(len(reasons))])
		b.WriteString(`","phase":"query","grouped":true,"failed_shards":[`)
		for b.Len() < targetLen {
			if b.Len() > 300 {
				b.WriteByte(',')
			}
			b.WriteString(`{"shard":`)
			b.WriteString(strconv.Itoa(r.Intn(10)))
			b.WriteString(`,"index":"logs-`)
			b.WriteString(strconv.Itoa(r.Intn(100)))
			b.WriteString(`","node":"`)
			b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffffffff, 16))
			b.WriteString(`","reason":{"type":"`)
			b.WriteString(causes[r.Intn(len(causes))])
			b.WriteString(`","reason":"`)
			b.WriteString(reasons[r.Intn(len(reasons))])
			b.WriteString(`"}}`)
		}
		b.WriteString(`]},"status":500}`)

	case 1: // Connection error cascade
		services := [...]string{"elasticsearch", "kibana", "apm-server", "fleet-server", "logstash"}
		errs := [...]string{
			"connection refused", "connection reset by peer", "i/o timeout",
			"TLS handshake timeout", "no route to host", "context deadline exceeded",
		}
		for b.Len() < targetLen {
			b.WriteString("error connecting to ")
			b.WriteString(services[r.Intn(len(services))])
			b.WriteString(" at 10.")
			b.WriteString(strconv.Itoa(r.Intn(256)))
			b.WriteByte('.')
			b.WriteString(strconv.Itoa(r.Intn(256)))
			b.WriteByte('.')
			b.WriteString(strconv.Itoa(r.Intn(256)))
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(r.Intn(10000) + 9000))
			b.WriteString(": ")
			b.WriteString(errs[r.Intn(len(errs))])
			b.WriteString("; attempt ")
			b.WriteString(strconv.Itoa(r.Intn(10) + 1))
			b.WriteString(" of 10\n")
		}

	case 2: // Stack trace with wrapped errors
		b.WriteString("github.com/elastic/cloud-on-k8s/pkg/controller")
		b.WriteString(": reconciliation failed: ")
		wraps := [...]string{
			"transient error", "context canceled", "connection lost",
			"timeout waiting for condition", "resource version conflict",
		}
		b.WriteString(wraps[r.Intn(len(wraps))])
		b.WriteByte('\n')
		b.WriteString(buildGoStackTrace(r, targetLen-b.Len()))
	}
	return b.String()
}

// LargeErrorMessage pre-generates a pool of large error message payloads.
func LargeErrorMessage(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildLargeErrorMessage(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

// ErrorMessageAttrs returns the two cross-cutting AttrGen entries that every
// profile should append to each MessageTemplate.Attrs:
//   - error.message at ~70% presence (pool-based, 13 messages)
//   - log.origin.stack_trace at ~25% presence (pool-based stack traces)
//
// stackTraceGen should be a pre-pooled generator such as GoStackTrace or
// JavaStackTrace. Pass nil to use a default Go stack trace pool.
func ErrorMessageAttrs(rng *rand.Rand, stackTraceGen ArgGenerator) []AttrGen {
	shortGen := RandomFrom(commonErrorMessages...)
	mediumGen := LargeErrorMessage(200, 2000, rng)
	largeGen := LargeErrorMessage(2000, 80000, rng)

	// ~93% short, ~5% medium, ~2% large
	mixedErrorMsgGen := func(r *rand.Rand, ctx GenContext) any {
		roll := r.Intn(100)
		switch {
		case roll < 93:
			return shortGen(r, ctx)
		case roll < 98:
			return mediumGen(r, ctx)
		default:
			return largeGen(r, ctx)
		}
	}

	if stackTraceGen == nil {
		stackTraceGen = GoStackTrace(800, 8500, rng)
	}
	return []AttrGen{
		{"error.message", OptionalAttr(0.60, mixedErrorMsgGen)},
		{"log.origin.stack_trace", OptionalAttr(0.20, stackTraceGen)},
	}
}

// appendCrossCutting appends the cross-cutting attrs to every MessageTemplate
// in the slice and returns the modified slice.
func appendCrossCutting(msgs []MessageTemplate, extra []AttrGen) []MessageTemplate {
	for i := range msgs {
		msgs[i].Attrs = append(msgs[i].Attrs, extra...)
	}
	return msgs
}
