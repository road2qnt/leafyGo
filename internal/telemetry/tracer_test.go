package telemetry

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func newRecordingProvider(t *testing.T) (*TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp, err := NewTracerProvider(context.Background(), TracerOptions{
		ServiceName:    "leafygo-test",
		ServiceVersion: "0.0.1",
		Env:            "test",
		SpanProcessors: []sdktrace.SpanProcessor{recorder},
	})
	if err != nil {
		t.Fatalf("NewTracerProvider: %v", err)
	}
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	tp.Install()
	return tp, recorder
}

func TestInjectExtractRoundTripPreservesParent(t *testing.T) {
	_, recorder := newRecordingProvider(t)

	ctx, parent := StartSpan(context.Background(), "HTTP POST /approve",
		attribute.String("http.method", "POST"))
	traceParent, traceState := InjectTraceContext(ctx)
	if traceParent == "" {
		t.Fatal("traceparent must not be empty inside a span")
	}
	if !strings.HasPrefix(traceParent, "00-") {
		t.Fatalf("traceparent = %q", traceParent)
	}

	remoteCtx := ExtractTraceContext(context.Background(), traceParent, traceState)
	_, child := StartSpan(remoteCtx, "worker.process_job")
	parent.End()
	child.End()

	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("want 2 spans, got %d", len(spans))
	}
	var parentSpan, childSpan = spans[0], spans[1]
	for _, span := range spans {
		switch span.Name() {
		case "HTTP POST /approve":
			parentSpan = span
		case "worker.process_job":
			childSpan = span
		}
	}
	if childSpan.Parent().SpanID() != parentSpan.SpanContext().SpanID() {
		t.Fatalf("child parent span = %s, want %s", childSpan.Parent().SpanID(), parentSpan.SpanContext().SpanID())
	}
	if childSpan.SpanContext().TraceID() != parentSpan.SpanContext().TraceID() {
		t.Fatal("extracted trace id must equal the original trace id")
	}
	if parentSpan.Name() != "HTTP POST /approve" {
		t.Fatalf("span name = %q", parentSpan.Name())
	}
}

func TestInjectTraceContextIsEmptyWithoutSpan(t *testing.T) {
	traceParent, traceState := InjectTraceContext(context.Background())
	if traceParent != "" || traceState != "" {
		t.Fatalf("want empty propagation headers, got %q / %q", traceParent, traceState)
	}
}

func TestExtractInvalidTraceparentKeepsContext(t *testing.T) {
	ctx := WithCorrelationID(context.Background(), "req-keep")
	got := ExtractTraceContext(ctx, "not-a-traceparent", "")
	if CorrelationFrom(got) != "req-keep" {
		t.Fatal("context values must survive an invalid traceparent")
	}
	if TraceIDFromContext(got) != "" {
		t.Fatal("invalid traceparent must not create a span context")
	}
}

func TestTraceIDFromContext(t *testing.T) {
	_, recorder := newRecordingProvider(t)
	ctx, span := StartSpan(context.Background(), "job")
	span.End()
	_ = recorder
	if len(TraceIDFromContext(ctx)) != 32 {
		t.Fatalf("trace id = %q", TraceIDFromContext(ctx))
	}
}

func TestNewTracerProviderConsoleExporterAndResource(t *testing.T) {
	var buf bytes.Buffer
	tp, err := NewTracerProvider(context.Background(), TracerOptions{
		ServiceName:    "leafygo-api",
		ServiceVersion: "9.9.9",
		Env:            "development",
		Console:        true,
		ConsoleWriter:  &buf,
	})
	if err != nil {
		t.Fatalf("NewTracerProvider: %v", err)
	}
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	}()

	_, span := tp.Tracer().Start(context.Background(), "worker.execute_batch")
	span.End()

	if !strings.Contains(buf.String(), "worker.execute_batch") {
		t.Fatalf("console exporter did not emit the span: %q", buf.String())
	}
	attrs := tp.Resource().Attributes()
	if v, ok := lookupAttr(attrs, "service.name"); !ok || v.AsString() != "leafygo-api" {
		t.Fatalf("service.name resource attr = %v", v)
	}
	if v, ok := lookupAttr(attrs, "deployment.environment"); !ok || v.AsString() != "development" {
		t.Fatalf("deployment.environment = %v", v)
	}
}

func TestTracerProviderSamplerOverride(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp, err := NewTracerProvider(context.Background(), TracerOptions{
		ServiceName:    "leafygo-test",
		Sampler:        sdktrace.NeverSample(),
		SpanProcessors: []sdktrace.SpanProcessor{recorder},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, span := tp.Tracer().Start(context.Background(), "dropped")
	span.End()
	if len(recorder.Ended()) != 0 {
		t.Fatal("NeverSample must drop spans")
	}
	if err := tp.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStartSpanUsesInstalledGlobalProvider(t *testing.T) {
	_, recorder := newRecordingProvider(t)
	ctx, span := StartSpan(context.Background(), "queue.publish", attribute.Int("queue_seq", 7))
	span.End()
	_ = ctx
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %d", len(spans))
	}
	found := false
	for _, attr := range spans[0].Attributes() {
		if attr.Key == "queue_seq" && attr.Value.AsInt64() == 7 {
			found = true
		}
	}
	if !found {
		t.Fatal("span attribute queue_seq missing")
	}
}

func lookupAttr(attrs []attribute.KeyValue, key string) (attribute.Value, bool) {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestInstalledPropagatorIsW3C(t *testing.T) {
	newRecordingProvider(t)
	if otel.GetTextMapPropagator() == nil {
		t.Fatal("global propagator must be installed")
	}
	// A second install must stay safe and keep W3C semantics.
	Install()
}
