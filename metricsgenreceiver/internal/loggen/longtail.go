package loggen

import (
	"math/rand"
	"strconv"
)

// LongTailField holds a pre-generated pool of values for a rare field.
// Hot-path cost: one rng.Int63() to decide emit + pool index.
type LongTailField struct {
	Key       string
	Pool      []string // pre-generated string values
	Threshold int64    // emit when rng.Int63n(longTailDenominator) < threshold
}

const longTailPoolBits = 8 // pool size = 256
const longTailPoolSize = 1 << longTailPoolBits
const longTailPoolMask = longTailPoolSize - 1
const longTailDenominator = 10000 // probability denominator (threshold 50 = 0.5%)

// LongTailSet is a pre-computed batch of long-tail fields. The hot path
// uses a pre-generated schedule of firing positions to avoid per-field
// probability rolls, consuming exactly one rng.Int63() per record.
type LongTailSet struct {
	Fields   []LongTailField
	schedule [][]scheduleEntry // pre-computed: for each schedule slot, which fields fire
}

type scheduleEntry struct {
	fieldIdx int
	poolIdx  int
}

const longTailScheduleSize = 65536
const longTailScheduleMask = longTailScheduleSize - 1

// buildStrPool creates a pool of string values from choices.
func buildStrPool(rng *rand.Rand, choices []string) []string {
	pool := make([]string, longTailPoolSize)
	for i := range pool {
		pool[i] = choices[rng.Intn(len(choices))]
	}
	return pool
}

// buildIntStrPool creates a pool of string-encoded ints in [min, max].
func buildIntStrPool(rng *rand.Rand, min, max int) []string {
	pool := make([]string, longTailPoolSize)
	span := max - min + 1
	for i := range pool {
		pool[i] = strconv.Itoa(rng.Intn(span) + min)
	}
	return pool
}

// buildIntChoiceStrPool creates a pool of string-encoded ints from choices.
func buildIntChoiceStrPool(rng *rand.Rand, choices []int) []string {
	pool := make([]string, longTailPoolSize)
	for i := range pool {
		pool[i] = strconv.Itoa(choices[rng.Intn(len(choices))])
	}
	return pool
}

// buildHexStrPool creates a pool of random hex strings.
func buildHexStrPool(rng *rand.Rand, length int) []string {
	pool := make([]string, longTailPoolSize)
	b := make([]byte, length)
	for i := range pool {
		for j := range b {
			b[j] = hexChars[rng.Intn(16)]
		}
		pool[i] = string(b)
	}
	return pool
}

// EmitLongTail consumes exactly one rng.Int63() per call and uses a pre-
// computed schedule to decide which fields fire. The schedule was generated
// at construction time by simulating each field's probability independently.
func (lt *LongTailSet) EmitLongTail(rng *rand.Rand, attrsOut map[string]any) {
	r := rng.Int63()
	slot := int(r) & longTailScheduleMask
	entries := lt.schedule[slot]
	for _, e := range entries {
		f := &lt.Fields[e.fieldIdx]
		attrsOut[f.Key] = f.Pool[e.poolIdx]
	}
}

// LongTailAttrs builds a LongTailSet with 61 rare fields (<1% presence)
// representing config-dump and diagnostic event patterns from production
// OTel/K8s workloads. Values are pre-stringified to avoid interface boxing.
func LongTailAttrs(rng *rand.Rand) *LongTailSet {
	fields := []LongTailField{
		// --- Config-dump fields (~0.1-0.5% presence) ---
		// Threshold N means N/10000 = N*0.01% probability.
		{"config.file.path", buildStrPool(rng, []string{"/etc/app/config.yaml", "/opt/config/settings.json", "/usr/local/etc/app.conf", "/app/config/production.yaml"}), 30},
		{"config.file.hash", buildHexStrPool(rng, 32), 30},
		{"config.reload.count", buildIntStrPool(rng, 1, 50), 20},
		{"config.reload.last_status", buildStrPool(rng, []string{"success", "failed", "skipped"}), 20},
		{"config.environment", buildStrPool(rng, []string{"production", "staging", "canary", "development"}), 50},
		{"config.feature_flags", buildStrPool(rng, []string{"dark_launch=true,new_ui=false", "dark_launch=false,new_ui=true", "beta=true"}), 20},
		{"config.max_connections", buildIntChoiceStrPool(rng, []int{100, 256, 512, 1024, 2048}), 30},
		{"config.worker_threads", buildIntChoiceStrPool(rng, []int{2, 4, 8, 16, 32}), 30},
		{"config.tls.enabled", buildStrPool(rng, []string{"true", "false"}), 40},
		{"config.tls.cert_expiry_days", buildIntStrPool(rng, 1, 365), 20},
		{"config.log_level", buildStrPool(rng, []string{"debug", "info", "warn", "error"}), 50},
		{"config.memory_limit_mb", buildIntChoiceStrPool(rng, []int{256, 512, 1024, 2048, 4096, 8192}), 30},
		{"config.cpu_limit_millicores", buildIntChoiceStrPool(rng, []int{250, 500, 1000, 2000, 4000}), 30},

		// --- Diagnostic / health-check fields ---
		{"process.runtime.jvm.gc.count", buildIntStrPool(rng, 0, 5000), 40},
		{"process.runtime.jvm.gc.pause_ms", buildIntStrPool(rng, 1, 500), 40},
		{"process.runtime.jvm.heap_used_mb", buildIntStrPool(rng, 64, 4096), 30},
		{"process.runtime.jvm.threads.count", buildIntStrPool(rng, 10, 500), 30},
		{"process.runtime.go.goroutines", buildIntStrPool(rng, 1, 10000), 50},
		{"process.runtime.go.mem.heap_alloc_mb", buildIntStrPool(rng, 8, 2048), 50},
		{"process.runtime.go.gc.pause_ns", buildIntStrPool(rng, 10000, 50000000), 30},
		{"process.cpu_seconds_total", buildIntStrPool(rng, 1, 100000), 40},
		{"process.memory_rss_mb", buildIntStrPool(rng, 32, 8192), 40},
		{"process.open_fds", buildIntStrPool(rng, 10, 65000), 30},
		{"process.uptime_seconds", buildIntStrPool(rng, 1, 2592000), 30},

		// --- K8s scheduling/lifecycle fields ---
		{"k8s.pod.restart_count", buildIntChoiceStrPool(rng, []int{0, 0, 0, 1, 1, 2, 3, 5}), 50},
		{"k8s.container.ready", buildStrPool(rng, []string{"true", "true", "true", "false"}), 40},
		{"k8s.pod.phase", buildStrPool(rng, []string{"Running", "Running", "Pending", "Succeeded", "Failed"}), 30},
		{"k8s.pod.qos_class", buildStrPool(rng, []string{"Guaranteed", "Burstable", "BestEffort"}), 30},
		{"k8s.node.condition.ready", buildStrPool(rng, []string{"True", "True", "True", "False"}), 20},
		{"k8s.node.condition.memory_pressure", buildStrPool(rng, []string{"False", "False", "True"}), 10},
		{"k8s.node.condition.disk_pressure", buildStrPool(rng, []string{"False", "False", "True"}), 10},
		{"k8s.deployment.revision", buildIntStrPool(rng, 1, 200), 30},
		{"k8s.hpa.current_replicas", buildIntStrPool(rng, 1, 50), 20},
		{"k8s.hpa.desired_replicas", buildIntStrPool(rng, 1, 50), 20},

		// --- Network / connectivity diagnostics ---
		{"net.sock.peer.addr", buildStrPool(rng, buildIPPool(rng, []string{"10.0.0.0/8"}, 200)), 40},
		{"net.sock.host.addr", buildStrPool(rng, []string{"0.0.0.0", "127.0.0.1", "10.0.0.1"}), 30},
		{"net.sock.host.port", buildIntChoiceStrPool(rng, []int{8080, 8443, 9090, 9200, 3306, 6379}), 30},
		{"net.host.connection.type", buildStrPool(rng, []string{"wifi", "cell", "wired", "unknown"}), 20},
		{"dns.lookup_duration_ms", buildIntStrPool(rng, 0, 200), 20},
		{"net.protocol.name", buildStrPool(rng, []string{"http", "https", "grpc", "amqp", "redis"}), 40},
		{"net.protocol.version", buildStrPool(rng, []string{"1.0", "1.1", "2.0", "3.0"}), 40},
		{"tls.client.server_name", buildStrPool(rng, []string{"api.example.com", "internal.svc.local", "search.cloud.internal", "dashboard.cloud.internal"}), 20},
		{"tls.client.certificate.serial", buildHexStrPool(rng, 20), 10},

		// --- Cloud / infrastructure metadata ---
		{"cloud.account.id", buildStrPool(rng, []string{"123456789012", "987654321098", "112233445566"}), 30},
		{"cloud.availability_zone", buildStrPool(rng, []string{"eu-west-1a", "eu-west-1b", "eu-west-1c", "us-east-1a", "us-east-1b"}), 40},
		{"cloud.machine.type", buildStrPool(rng, []string{"m5.xlarge", "m5.2xlarge", "c5.4xlarge", "r5.2xlarge", "t3.medium"}), 30},
		{"cloud.region", buildStrPool(rng, []string{"eu-west-1", "us-east-1", "ap-southeast-1"}), 40},
		{"host.cpu.utilization", buildIntStrPool(rng, 1, 100), 30},
		{"host.disk.io.read_bytes", buildIntStrPool(rng, 0, 1000000000), 20},
		{"host.disk.io.write_bytes", buildIntStrPool(rng, 0, 1000000000), 20},
		{"host.network.io.receive_bytes", buildIntStrPool(rng, 0, 1000000000), 20},
		{"host.network.io.transmit_bytes", buildIntStrPool(rng, 0, 1000000000), 20},

		// --- Distributed tracing / correlation ---
		{"session.id", buildHexStrPool(rng, 16), 50},
		{"enduser.id", buildHexStrPool(rng, 12), 30},
		{"enduser.role", buildStrPool(rng, []string{"admin", "user", "service-account", "readonly"}), 20},
		{"enduser.scope", buildStrPool(rng, []string{"read", "write", "admin", "monitoring"}), 20},
		{"thread.id", buildIntStrPool(rng, 1, 65535), 40},
		{"thread.name", buildStrPool(rng, []string{"main", "worker-0", "worker-1", "grpc-default-executor-0", "http-nio-8080-exec-1", "pool-1-thread-1"}), 40},
		{"code.function", buildStrPool(rng, []string{"handleRequest", "processMessage", "executeQuery", "serialize", "authenticate", "authorize", "validate"}), 50},
		{"code.namespace", buildStrPool(rng, []string{"com.example.service", "internal.handler", "net.http", "database.sql", "grpc.server"}), 40},
		{"code.filepath", buildStrPool(rng, []string{"handler.go:142", "service.py:89", "Controller.java:201", "middleware.ts:56", "query.go:77"}), 40},
		{"code.lineno", buildIntStrPool(rng, 1, 2000), 40},
	}
	schedule := buildLongTailSchedule(rng, fields)
	return &LongTailSet{Fields: fields, schedule: schedule}
}

// buildLongTailSchedule pre-computes which fields fire for each schedule slot.
// For each slot, it independently rolls each field's probability and records
// the ones that fire along with a pool index. This replaces per-record rng
// calls with a single schedule lookup.
func buildLongTailSchedule(rng *rand.Rand, fields []LongTailField) [][]scheduleEntry {
	schedule := make([][]scheduleEntry, longTailScheduleSize)
	for slot := range schedule {
		var entries []scheduleEntry
		for fi, f := range fields {
			if rng.Int63()%longTailDenominator < f.Threshold {
				poolIdx := rng.Intn(longTailPoolSize)
				entries = append(entries, scheduleEntry{fieldIdx: fi, poolIdx: poolIdx})
			} else {
				rng.Intn(longTailPoolSize) // consume for determinism
			}
		}
		schedule[slot] = entries
	}
	return schedule
}
