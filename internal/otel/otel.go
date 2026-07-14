// Package otel wires the OpenTelemetry SDK into dumpstore.
//
// Everything is driven by the standard OTEL environment variables
// (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_PROTOCOL,
// OTEL_SERVICE_NAME, OTEL_TRACES_SAMPLER, ...) via the contrib autoexport
// factories. When no OTLP endpoint is configured the SDK is never installed:
// traces and metrics stay on the default no-op globals and the logging
// pipeline carries only the journald exporter.
package otel

import (
	"context"
	"errors"
	"io"
	"os"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	"dumpstore/internal/logging"
)

// Tracer returns the shared tracer for manual spans. It resolves through the
// global provider, so it is a no-op unless Init installed the SDK.
func Tracer() trace.Tracer {
	return otel.Tracer("dumpstore")
}

// Enabled reports whether an OTLP endpoint is configured and the SDK is not
// explicitly disabled. This is the single switch for all OTEL machinery.
func Enabled() bool {
	if os.Getenv("OTEL_SDK_DISABLED") == "true" {
		return false
	}
	for _, v := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	} {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}

// Providers holds the constructed SDK providers. Logs is always non-nil (the
// journald exporter runs through it even with OTEL disabled); the trace and
// meter providers are only installed when Enabled().
type Providers struct {
	Logs      *sdklog.LoggerProvider
	shutdowns []func(context.Context) error
}

// Shutdown flushes and stops all providers.
func (p *Providers) Shutdown(ctx context.Context) error {
	var errs []error
	for _, fn := range p.shutdowns {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Init builds the logging pipeline (always) and, when Enabled(), installs the
// trace and meter providers plus the OTLP log branch. logOut is the journald
// destination (stdout in production, a buffer in tests); nil disables the
// local branch entirely (-log-stdout=false, OTLP-only setups).
func Init(ctx context.Context, version string, logOut io.Writer) (*Providers, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName()),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		return nil, err
	}

	p := &Providers{}

	logOpts := []sdklog.LoggerProviderOption{sdklog.WithResource(res)}
	if logOut != nil {
		// Synchronous and ordered — same delivery semantics as writing the
		// journald line directly from a slog handler.
		logOpts = append(logOpts,
			sdklog.WithProcessor(sdklog.NewSimpleProcessor(logging.NewJournalExporter(logOut))))
	}
	if Enabled() {
		logExp, err := autoexport.NewLogExporter(ctx)
		if err != nil {
			return nil, err
		}
		logOpts = append(logOpts, sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)))
	}
	p.Logs = sdklog.NewLoggerProvider(logOpts...)
	p.shutdowns = append(p.shutdowns, p.Logs.Shutdown)

	if !Enabled() {
		return p, nil
	}

	spanExp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(spanExp),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	p.shutdowns = append(p.shutdowns, tp.Shutdown)

	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)
	otel.SetMeterProvider(mp)
	p.shutdowns = append(p.shutdowns, mp.Shutdown)
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return nil, err
	}

	return p, nil
}

func serviceName() string {
	if n := os.Getenv("OTEL_SERVICE_NAME"); n != "" {
		return n
	}
	return "dumpstore"
}

// StatusInfo describes the effective OTEL configuration for display in the
// System tab. Endpoint URLs are shown as-is (admin-only UI); header env vars,
// which may carry credentials, are never included.
type StatusInfo struct {
	Enabled     bool   `json:"enabled"`
	Endpoint    string `json:"endpoint,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
}

// Status returns the effective OTEL configuration derived from the
// environment. Injected into the sysinfo response by main (internal/api must
// not import this package — cycle via internal/logging).
func Status() StatusInfo {
	if !Enabled() {
		return StatusInfo{}
	}
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		for _, v := range []string{
			"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
			"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		} {
			if e := os.Getenv(v); e != "" {
				endpoint = e
				break
			}
		}
	}
	protocol := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
	if protocol == "" {
		protocol = "http/protobuf"
	}
	return StatusInfo{
		Enabled:     true,
		Endpoint:    endpoint,
		Protocol:    protocol,
		ServiceName: serviceName(),
	}
}
