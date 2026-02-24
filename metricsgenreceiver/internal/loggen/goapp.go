package loggen

import (
	"go.opentelemetry.io/collector/pdata/plog"
)

var goAppHTTPMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
var goAppPaths = []string{"/api/v1/users", "/api/v1/orders", "/api/v1/products", "/health", "/metrics"}
var goAppServices = []string{"user-service", "order-service", "payment-service", "notification-service"}
var goAppGrpcMethods = []string{"GetUser", "CreateOrder", "ProcessPayment", "SendNotification"}
var goAppQueues = []string{"orders", "notifications", "emails", "jobs"}
var goAppErrors = []string{"connection refused", "timeout", "context deadline exceeded", "connection reset by peer"}
var goAppDbHosts = []string{"mysql-primary:3306", "postgres:5432", "localhost:5432"}

func GoAppProfile() *AppProfile {
	return &AppProfile{
		Name:             "goapp",
		SeverityWeights:  [4]int{70, 90, 98, 100},
		EmitTraceContext: true,
		Messages: append(
			goAppInfoLogs(),
			goAppWarnLogs()...,
		),
	}
}

func goAppInfoLogs() []MessageTemplate {
	tsLayout := "2006-01-02T15:04:05.000Z0700"
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","ts":"%s","caller":"server/handler.go:%d","msg":"request completed","method":"%s","path":"%s","status":%d,"duration":"%dms","request_id":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppHTTPMethods), RandomPath(goAppPaths),
				RandomFromInt(200, 201, 204, 304), RandomDuration(10, 500),
				RandomID(16),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","ts":"%s","caller":"db/connection.go:%d","msg":"database connection established","host":"%s","port":5432,"database":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(50, 80),
				RandomPath(goAppDbHosts), RandomPath(mysqlDBNames),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","ts":"%s","caller":"grpc/client.go:%d","msg":"grpc call completed","service":"%s","method":"%s","duration":"%dms","code":"OK"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(60, 95),
				RandomPath(goAppServices), RandomPath(goAppGrpcMethods),
				RandomDuration(5, 200),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","ts":"%s","caller":"worker/processor.go:%d","msg":"job processed","job_id":"%s","queue":"%s","duration":"%dms"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(70, 110),
				RandomID(12), RandomPath(goAppQueues),
				RandomDuration(50, 2000),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   `{"level":"info","ts":"%s","caller":"server/handler.go:%d","msg":"request started","method":"%s","path":"%s","request_id":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppHTTPMethods), RandomPath(goAppPaths),
				RandomID(16),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
	}
}

func goAppWarnLogs() []MessageTemplate {
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
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   `{"level":"warn","ts":"%s","caller":"cache/redis.go:%d","msg":"cache miss","key":"%s","fallback":"database"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(30, 60),
				RandomFrom("user:123", "session:abc", "config:global", "product:456"),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   `{"level":"warn","ts":"%s","caller":"grpc/client.go:%d","msg":"grpc retry","service":"%s","method":"%s","attempt":2,"max_retries":3}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(60, 95),
				RandomPath(goAppServices), RandomPath(goAppGrpcMethods),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   `{"level":"error","ts":"%s","caller":"server/handler.go:%d","msg":"request failed","method":"%s","path":"%s","status":500,"error":"%s","request_id":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(45, 120),
				RandomPath(goAppHTTPMethods), RandomPath(goAppPaths),
				RandomPath(goAppErrors), RandomID(16),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   `{"level":"error","ts":"%s","caller":"db/query.go:%d","msg":"query execution failed","query":"SELECT * FROM %s","error":"%s","duration":"%dms"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(80, 120),
				RandomPath(mysqlTables), RandomPath(goAppErrors),
				RandomDuration(100, 5000),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   `{"level":"error","ts":"%s","caller":"grpc/client.go:%d","msg":"grpc call failed","service":"%s","method":"%s","code":"Unavailable","error":"%s"}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(60, 95),
				RandomPath(goAppServices), RandomPath(goAppGrpcMethods),
				RandomPath(goAppErrors),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   `{"level":"fatal","ts":"%s","caller":"main.go:42","msg":"failed to start server","error":"listen tcp :8080: bind: address already in use"}`,
			Args:     []ArgGenerator{Timestamp(tsLayout)},
			Attrs:    map[string]ArgGenerator{"service.language": Static("go")},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   `{"level":"fatal","ts":"%s","caller":"db/connection.go:%d","msg":"database connection lost","host":"%s","error":"connection refused","retry_count":10}`,
			Args: []ArgGenerator{
				Timestamp(tsLayout), RandomInt(50, 80),
				RandomPath(goAppDbHosts),
			},
			Attrs: map[string]ArgGenerator{"service.language": Static("go")},
		},
	}
}
