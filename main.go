package main

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func newMeterProvider(ctx context.Context) (metric.MeterProvider, error) {
	interval := 10 * time.Second

	res, err := getResource()
	if err != nil {
		return nil, fmt.Errorf("could not get resource: %w", err)
	}

	collectorExporter, err := getOtelMetricsCollectorExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get collector exporter: %w", err)
	}

	periodicReader := metricsdk.NewPeriodicReader(collectorExporter, metricsdk.WithInterval(interval))

	provider := metricsdk.NewMeterProvider(
		metricsdk.WithResource(res),
		metricsdk.WithReader(periodicReader),
	)

	return provider, nil
}

func NewMeter(ctx context.Context) (metric.Meter, error) {
	provider, err := newMeterProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not create meter provider: %w", err)
	}

	// Set global Meter Provider
	otel.SetMeterProvider(provider)

	return provider.Meter("otel_sla"), nil
}

func getResource() (*resource.Resource, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("otel-sla"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("could not merge resources: %w", err)
	}
	return res, nil
}

func getOtelMetricsCollectorExporter(ctx context.Context) (metricsdk.Exporter, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	exporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint("localhost:4317"),
		otlpmetricgrpc.WithCompressor("gzip"),
		otlpmetricgrpc.WithInsecure(),
	)

	if err != nil {
		return nil, fmt.Errorf("could not create metric exporter: %w", err)
	}
	return exporter, nil
}

// Collects memory usage every 5 seconds
func collectMachineResourceMetrics(meter metric.Meter) {
	var Mb uint64 = 1_048_576 // Convert bytes to MB

	// Define the observable gauge metric

	memGauge, err := meter.Float64ObservableGauge(
		"otel.sla.metric",
		metric.WithDescription("otel-sla-metric"),
		metric.WithUnit("count"),
	)
	if err != nil {
		log.Fatalf("Failed to create metric: %v", err)
	}

	// Register callback to observe memory stats
	_, err = meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		allocatedMemoryInMB := float64(memStats.Alloc) / float64(Mb)
		timestamp := time.Now().Format(time.RFC3339)
		o.ObserveFloat64(memGauge, allocatedMemoryInMB, metric.WithAttributes(attribute.String("metric_generation_time", timestamp)))
		return nil
	}, memGauge)

	if err != nil {
		log.Fatalf("Failed to register callback: %v", err)
	}
}

func main() {
	ctx := context.Background()
	meter, err := NewMeter(ctx)
	if err != nil {
		fmt.Printf("Could not create meter: %v\n", err)
		return
	}

	// Start collecting memory metrics
	collectMachineResourceMetrics(meter)

	// Create a counter metric
	counter, err := meter.Int64Counter("allocated_memory_in_mb",
		metric.WithDescription("The total allocated memory in MB"), // Description of the metric
		metric.WithUnit("count"))
	if err != nil {
		log.Fatalf("Failed to create counter: %v", err)
	}

	// Increment the counter
	counter.Add(ctx, 1)

	// Keep the application running
	select {}
}
