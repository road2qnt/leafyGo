package telemetry

import (
	"context"
	"fmt"
	"io"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "leafygo"

// TracerOptions configures the tracing pipeline.
type TracerOptions struct {
	ServiceName    string
	ServiceVersion string
	Env            string

	// Console emits spans to ConsoleWriter (stdout by default). This is the
	// mandatory, collector-free proof required by the milestone.
	Console       bool
	ConsoleWriter io.Writer

	// OTLPEndpoint is a host:port (or http(s)://host:port) for the OTLP/HTTP
	// exporter. Empty disables the exporter.
	OTLPEndpoint string
	OTLPInsecure bool
	OTLPHeaders  map[string]string

	// SampleRatio in (0,1) enables ratio-based sampling. Zero keeps the default
	// parent-based always-on sampler. Sampler overrides both when set.
	SampleRatio float64
	Sampler     sdktrace.Sampler

	// SpanProcessors is appended after the configured exporters. It exists for
	// tests and for embedding custom processors.
	SpanProcessors []sdktrace.SpanProcessor
}

// TracerProvider owns the SDK provider and its resource.
type TracerProvider struct {
	provider *sdktrace.TracerProvider
	resource *resource.Resource
}

// NewTracerProvider builds the tracer provider with a console exporter, an
// optional OTLP/HTTP exporter, W3C propagation, and service resource metadata.
func NewTracerProvider(ctx context.Context, opts TracerOptions) (*TracerProvider, error) {
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		serviceName = "leafygo"
	}

	attrs := []attribute.KeyValue{attribute.String("service.name", serviceName)}
	if opts.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", opts.ServiceVersion))
	}
	if opts.Env != "" {
		attrs = append(attrs, attribute.String("deployment.environment", opts.Env))
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes("", attrs...))
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	var processors []sdktrace.SpanProcessor
	if opts.Console {
		writer := opts.ConsoleWriter
		if writer == nil {
			writer = io.Discard
		}
		exporter, err := stdouttrace.New(stdouttrace.WithWriter(writer))
		if err != nil {
			return nil, fmt.Errorf("build stdout span exporter: %w", err)
		}
		// Console output is low-volume and used as demo evidence, so spans must
		// appear immediately rather than waiting for a batch flush.
		processors = append(processors, sdktrace.NewSimpleSpanProcessor(exporter))
	}

	if endpoint := strings.TrimSpace(opts.OTLPEndpoint); endpoint != "" {
		insecure := opts.OTLPInsecure
		switch {
		case strings.HasPrefix(endpoint, "http://"):
			endpoint = strings.TrimPrefix(endpoint, "http://")
			insecure = true
		case strings.HasPrefix(endpoint, "https://"):
			endpoint = strings.TrimPrefix(endpoint, "https://")
		}
		exporterOpts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(endpoint)}
		if insecure {
			exporterOpts = append(exporterOpts, otlptracehttp.WithInsecure())
		}
		if len(opts.OTLPHeaders) > 0 {
			exporterOpts = append(exporterOpts, otlptracehttp.WithHeaders(opts.OTLPHeaders))
		}
		exporter, err := otlptracehttp.New(ctx, exporterOpts...)
		if err != nil {
			return nil, fmt.Errorf("build otlp span exporter: %w", err)
		}
		processors = append(processors, sdktrace.NewBatchSpanProcessor(exporter))
	}
	processors = append(processors, opts.SpanProcessors...)

	providerOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(resolveSampler(opts)),
	}
	for _, processor := range processors {
		if processor != nil {
			providerOpts = append(providerOpts, sdktrace.WithSpanProcessor(processor))
		}
	}

	return &TracerProvider{
		provider: sdktrace.NewTracerProvider(providerOpts...),
		resource: res,
	}, nil
}

func resolveSampler(opts TracerOptions) sdktrace.Sampler {
	if opts.Sampler != nil {
		return opts.Sampler
	}
	if opts.SampleRatio > 0 && opts.SampleRatio < 1 {
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(opts.SampleRatio))
	}
	return sdktrace.ParentBased(sdktrace.AlwaysSample())
}

// Install makes this provider and W3C propagation process-global.
func (p *TracerProvider) Install() {
	if p == nil || p.provider == nil {
		return
	}
	otel.SetTracerProvider(p.provider)
	otel.SetTextMapPropagator(W3CPropagator())
}

// Install installs W3C propagation without replacing the global provider.
func Install() {
	otel.SetTextMapPropagator(W3CPropagator())
}

// W3CPropagator returns the trace-context + baggage propagator used by LeafyGo.
func W3CPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

// Tracer returns a tracer from this provider.
func (p *TracerProvider) Tracer() trace.Tracer {
	if p == nil || p.provider == nil {
		return otel.Tracer(instrumentationName)
	}
	return p.provider.Tracer(instrumentationName)
}

// Resource exposes the provider resource for diagnostics and tests.
func (p *TracerProvider) Resource() *resource.Resource {
	if p == nil {
		return resource.Empty()
	}
	return p.resource
}

// Shutdown flushes and stops the provider.
func (p *TracerProvider) Shutdown(ctx context.Context) error {
	if p == nil || p.provider == nil {
		return nil
	}
	return p.provider.Shutdown(ctx)
}

// StartSpan starts a span on the process-global tracer provider.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName).Start(ctx, name, trace.WithAttributes(attrs...))
}

// InjectTraceContext serializes the active span into W3C traceparent/tracestate
// strings so it can be stored in migration_jobs and resumed by a worker.
func InjectTraceContext(ctx context.Context) (traceParent string, traceState string) {
	carrier := propagation.HeaderCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier.Get("traceparent"), carrier.Get("tracestate")
}

// ExtractTraceContext restores a remote span context from stored strings.
func ExtractTraceContext(ctx context.Context, traceParent, traceState string) context.Context {
	if strings.TrimSpace(traceParent) == "" {
		return ctx
	}
	carrier := propagation.HeaderCarrier{}
	carrier.Set("traceparent", traceParent)
	if traceState != "" {
		carrier.Set("tracestate", traceState)
	}
	return propagation.TraceContext{}.Extract(ctx, carrier)
}

// TraceIDFromContext returns the active trace id, or an empty string.
func TraceIDFromContext(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// SpanIDFromContext returns the active span id, or an empty string.
func SpanIDFromContext(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.SpanID().String()
}
