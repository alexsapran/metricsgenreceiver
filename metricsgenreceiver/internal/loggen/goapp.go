package loggen

import (
	"math/rand"

	"go.opentelemetry.io/collector/pdata/plog"
)

var goAppHTTPMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
var goAppPaths = []string{"/api/v1/users", "/api/v1/orders", "/api/v1/products", "/health", "/metrics"}
var goAppServices = []string{"user-service", "order-service", "payment-service", "notification-service"}
var goAppGrpcMethods = []string{"GetUser", "CreateOrder", "ProcessPayment", "SendNotification"}
var goAppQueues = []string{"orders", "notifications", "emails", "jobs"}
var goAppErrors = []string{"connection refused", "timeout", "context deadline exceeded", "connection reset by peer"}
var goAppDbHosts = []string{"mysql-primary:3306", "postgres:5432", "localhost:5432"}

func GoAppProfile(rng *rand.Rand) *AppProfile {
	if rng == nil {
		rng = rand.New(rand.NewSource(0))
	}
	crossCutting := ErrorMessageAttrs(rng, GoStackTrace(800, 8500, rng))
	longTail := LongTailAttrs(rng)
	msgs := append(
		append(goAppInfoLogs(), goAppDebugLogs(rng)...),
		goAppWarnLogs(rng)...,
	)
	return &AppProfile{
		Name:             "goapp",
		ScopeName:        "io.opentelemetry.goapp",
		SeverityWeights:  DefaultSeverityWeights(),
		EmitTraceContext: true,
		Messages:         appendCrossCutting(msgs, crossCutting),
		LongTail:         longTail,
	}
}

func goAppInfoLogs() []MessageTemplate {
	tsLayout := "2006-01-02T15:04:05.000Z0700"
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","msg":"ok"}`,
			Args:     []ArgGenerator{},
			Attrs:    []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","ts":"%s","caller":"server/handler.go:%d","msg":"request completed","method":"%s","path":"%s","status":%d,"duration":"%dms","request_id":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppHTTPMethods), RandomPath(goAppPaths),
				RandomFromInt(200, 201, 204, 304), RandomDuration(10, 500),
				RandomID(16),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","ts":"%s","caller":"db/connection.go:%d","msg":"database connection established","host":"%s","port":5432,"database":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(50, 80),
				RandomPath(goAppDbHosts), RandomPath(mysqlDBNames),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","ts":"%s","caller":"grpc/client.go:%d","msg":"grpc call completed","service":"%s","method":"%s","duration":"%dms","code":"OK"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(60, 95),
				RandomPath(goAppServices), RandomPath(goAppGrpcMethods),
				RandomDuration(5, 200),
			},
			AttrFromArg: map[string]int{"rpc.service": 2, "rpc.method": 3},
			Attrs:       []AttrGen{{"telemetry.sdk.language", Static("go")}, {"rpc.system", Static("grpc")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","ts":"%s","caller":"worker/processor.go:%d","msg":"job processed","job_id":"%s","queue":"%s","duration":"%dms"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(70, 110),
				RandomID(12), RandomPath(goAppQueues),
				RandomDuration(50, 2000),
			},
			AttrFromArg: map[string]int{"messaging.destination.name": 3},
			Attrs:       []AttrGen{{"telemetry.sdk.language", Static("go")}, {"messaging.system", Static("rabbitmq")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","ts":"%s","caller":"server/handler.go:%d","msg":"request started","method":"%s","path":"%s","request_id":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppHTTPMethods), RandomPath(goAppPaths),
				RandomID(16),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
	}
}

func goAppDebugLogs(rng *rand.Rand) []MessageTemplate {
	tsLayout := "2006-01-02T15:04:05.000Z0700"
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberDebug,
			Format:   `{"level":"debug","ts":"%s","caller":"server/handler.go:%d","msg":"request headers","method":"%s","path":"%s","content_type":"application/json","accept":"application/json","user_agent":"%s","request_id":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppHTTPMethods), RandomPath(goAppPaths),
				RandomUserAgent, RandomID(16),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberDebug,
			Format:   `{"level":"debug","ts":"%s","caller":"db/query.go:%d","msg":"executing query","query":"SELECT * FROM %s WHERE id = $1","params":["%%s"],"duration":"%dms"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(80, 120),
				RandomPath(mysqlTables), RandomID(8),
				RandomDuration(1, 50),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberDebug,
			Format:   `{"level":"debug","ts":"%s","caller":"server/handler.go:%d","msg":"request body","method":"%s","path":"%s","body":%q,"request_id":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppHTTPMethods), RandomPath(goAppPaths),
				LargeJSONPayload(800, 2500, rng), RandomID(16),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberDebug,
			Format:   `{"level":"debug","ts":"%s","caller":"server/handler.go:%d","msg":"response body","path":"%s","body":%q,"request_id":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppPaths), LargeJSONPayload(1000, 3000, rng), RandomID(16),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
	}
}

func goAppWarnLogs(rng *rand.Rand) []MessageTemplate {
	tsLayout := "2006-01-02T15:04:05.000Z0700"
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberWarn,
			Format:   `{"level":"warn","ts":"%s","caller":"server/handler.go:%d","msg":"slow request detected","method":"%s","path":"%s","duration":"%dms","threshold":"500ms"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppHTTPMethods), RandomPath(goAppPaths),
				RandomDuration(500, 3000),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   `{"level":"warn","ts":"%s","caller":"cache/redis.go:%d","msg":"cache miss","key":"%s","fallback":"database"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(30, 60),
				RandomFrom("user:123", "session:abc", "config:global", "product:456"),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   `{"level":"warn","ts":"%s","caller":"grpc/client.go:%d","msg":"grpc retry","service":"%s","method":"%s","attempt":2,"max_retries":3}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(60, 95),
				RandomPath(goAppServices), RandomPath(goAppGrpcMethods),
			},
			AttrFromArg: map[string]int{"rpc.service": 2, "rpc.method": 3},
			Attrs:       []AttrGen{{"telemetry.sdk.language", Static("go")}, {"rpc.system", Static("grpc")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   `{"level":"error","ts":"%s","caller":"server/handler.go:%d","msg":"request failed","method":"%s","path":"%s","status":500,"error":"%s","request_id":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppHTTPMethods), RandomPath(goAppPaths),
				RandomPath(goAppErrors), RandomID(16),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   `{"level":"error","ts":"%s","caller":"db/query.go:%d","msg":"query execution failed","query":"SELECT * FROM %s","error":"%s","duration":"%dms"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(80, 120),
				RandomPath(mysqlTables), RandomPath(goAppErrors),
				RandomDuration(100, 5000),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   `{"level":"error","ts":"%s","caller":"grpc/client.go:%d","msg":"grpc call failed","service":"%s","method":"%s","code":"Unavailable","error":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(60, 95),
				RandomPath(goAppServices), RandomPath(goAppGrpcMethods),
				RandomPath(goAppErrors),
			},
			AttrFromArg: map[string]int{"rpc.service": 2, "rpc.method": 3},
			Attrs:       []AttrGen{{"telemetry.sdk.language", Static("go")}, {"rpc.system", Static("grpc")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   `{"level":"error","ts":"%s","caller":"server/handler.go:%d","msg":"panic recovered","error":"%s","stacktrace":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppErrors), GoStackTrace(1500, 8500, rng),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   `{"level":"error","ts":"%s","caller":"server/handler.go:%d","msg":"handler panic","error":"%s","request_id":"%s","stacktrace":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppErrors), RandomID(16), GoStackTrace(1500, 8500, rng),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   `{"level":"error","ts":"%s","caller":"db/query.go:%d","msg":"query panic","query":"SELECT * FROM %s","error":"%s","stacktrace":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(80, 120),
				RandomPath(mysqlTables), RandomPath(goAppErrors), GoStackTrace(1500, 8500, rng),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   `{"level":"error","ts":"%s","caller":"grpc/client.go:%d","msg":"grpc panic","service":"%s","method":"%s","error":"%s","stacktrace":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(60, 95),
				RandomPath(goAppServices), RandomPath(goAppGrpcMethods),
				RandomPath(goAppErrors), JavaStackTrace(1500, 8500, rng),
			},
			AttrFromArg: map[string]int{"rpc.service": 2, "rpc.method": 3},
			Attrs:       []AttrGen{{"telemetry.sdk.language", Static("go")}, {"rpc.system", Static("grpc")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   `{"level":"fatal","ts":"%s","caller":"main.go:42","msg":"failed to start server","error":"listen tcp :8080: bind: address already in use"}`,
			Args:     []ArgGenerator{Timestamp(tsLayout)},
			Attrs:    []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   `{"level":"fatal","ts":"%s","caller":"db/connection.go:%d","msg":"database connection lost","host":"%s","error":"connection refused","retry_count":10}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(50, 80),
				RandomPath(goAppDbHosts),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   `{"level":"fatal","ts":"%s","caller":"main.go:%d","msg":"unrecoverable panic","error":"%s","stacktrace":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(40, 50),
				RandomPath(goAppErrors), GoStackTrace(2000, 8500, rng),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   `{"level":"fatal","ts":"%s","caller":"server/handler.go:%d","msg":"fatal: out of memory","error":"runtime: out of memory","stacktrace":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				GoStackTrace(2500, 8500, rng),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   `{"level":"fatal","ts":"%s","caller":"db/connection.go:%d","msg":"fatal: database unreachable","host":"%s","error":"%s","stacktrace":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(50, 80),
				RandomPath(goAppDbHosts), RandomPath(goAppErrors), GoStackTrace(1500, 8500, rng),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   `{"level":"fatal","ts":"%s","caller":"worker/processor.go:%d","msg":"fatal: worker panic","job_id":"%s","error":"%s","stacktrace":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(70, 110),
				RandomID(12), RandomPath(goAppErrors), GoStackTrace(2000, 8500, rng),
			},
			Attrs: []AttrGen{{"telemetry.sdk.language", Static("go")}, {"messaging.system", Static("rabbitmq")}},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   `{"level":"fatal","ts":"%s","caller":"grpc/client.go:%d","msg":"fatal: grpc connection lost","service":"%s","error":"%s","stacktrace":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(60, 95),
				RandomPath(goAppServices), RandomPath(goAppErrors), JavaStackTrace(2000, 8500, rng),
			},
			AttrFromArg: map[string]int{"rpc.service": 2},
			Attrs:       []AttrGen{{"telemetry.sdk.language", Static("go")}, {"rpc.system", Static("grpc")}},
		},
	}
}
