package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
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

// Log file path
const logFilePath = "otel_sla_logs.json"

// Struct for JSON log entry
type LogEntry struct {
	Message            string `json:"message"`
	CollectorTimestamp string `json:"collector_timestamp"`
}

// Function to write JSON log to a file
func logToFile(entry LogEntry) {
	file, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to open log file: %v", err)
		return
	}
	defer file.Close()

	jsonData, err := json.Marshal(entry)
	if err != nil {
		log.Printf("Failed to marshal JSON: %v", err)
		return
	}

	// Append newline for each log entry
	_, err = file.WriteString(string(jsonData) + "\n")
	if err != nil {
		log.Printf("Failed to write log entry: %v", err)
	}
}

// Creates and configures a new Meter Provider
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

// Creates a new Meter
func NewMeter(ctx context.Context) (metric.Meter, error) {
	provider, err := newMeterProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not create meter provider: %w", err)
	}

	otel.SetMeterProvider(provider)

	return provider.Meter("otel_sla"), nil
}

// Returns a resource with additional attributes
func getResource() (*resource.Resource, error) {
	hostname, _ := os.Hostname()
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("otel-sla"),
			attribute.String("host.name", hostname),
			attribute.String("os.type", runtime.GOOS),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("could not merge resources: %w", err)
	}
	return res, nil
}

// Creates an OTLP metrics exporter
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

// Collects and reports memory usage periodically
func collectMachineResourceMetrics(ctx context.Context, meter metric.Meter) {
	var Mb uint64 = 1_048_576 // Convert bytes to MB

	// Create observable gauge for memory usage
	memGauge, err := meter.Float64ObservableGauge(
		"otel.sla.metric",
		metric.WithDescription("Allocated memory in MB"),
		metric.WithUnit("MB"),
	)
	if err != nil {
		log.Printf("Failed to create memory gauge: %v", err)
		return
	}

	// Register callback to observe memory stats
	_, err = meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		allocatedMemoryInMB := float64(memStats.Alloc) / float64(Mb)
		o.ObserveFloat64(memGauge, allocatedMemoryInMB, metric.WithAttributes(
			attribute.String("metric_generation_time", time.Now().Format(time.RFC3339)),
		))
		return nil
	}, memGauge)

	if err != nil {
		log.Printf("Failed to register memory callback: %v", err)
	}
}

// Increments the memory counter periodically and logs JSON to a file
func startMemoryCounter(ctx context.Context, meter metric.Meter) {
	counter, err := meter.Int64Counter(
		"allocated_memory_in_mb",
		metric.WithDescription("Total allocated memory in MB"),
		metric.WithUnit("MB"),
	)
	if err != nil {
		log.Printf("Failed to create memory counter: %v", err)
		return
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				var memStats runtime.MemStats
				runtime.ReadMemStats(&memStats)
				allocatedMemoryInMB := int64(memStats.Alloc / 1_048_576)

				// Increment the counter
				counter.Add(ctx, allocatedMemoryInMB, metric.WithAttributes(
					attribute.String("metric_collection_time", time.Now().Format(time.RFC3339)),
				))

				// Log JSON entry to file
				logToFile(LogEntry{
					Message:            "otel-sla-logs",
					CollectorTimestamp: time.Now().Format(time.RFC3339),
				})

				time.Sleep(5 * time.Second)
			}
		}
	}()
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meter, err := NewMeter(ctx)
	if err != nil {
		log.Fatalf("Could not create meter: %v", err)
	}

	// Start collecting memory metrics
	collectMachineResourceMetrics(ctx, meter)

	// Start counter updates and JSON logging
	startMemoryCounter(ctx, meter)

	// Keep the application running
	select {}
}
