<div align="center">
  <h1>otelmonitara-exporter</h1>

<br>

`otelmonitara-exporter` is a small, dependency-free library for exports opentelemetry logs to monitara APM . 

</div>

## Installation

```bash
$ go get github.com/hamzaabujarad/otelmonitara-exporter
```

## Usage
```go
//Monitara Opentelemetry end points
	hosts := []string{"YOUR_END_POINT"}
	//Monitara open telemetry apiKey
	apiKey := "YOUR_API_KEY"
	//Max documents size per task
	maxBatchSize := 100
	//debug mode on or off
	verbose := true
	//How often should new tasks be executed (in ms)
	interval := 5000
	//calling the monitara exporter
	exporter, err := monitaraexporter.New(hosts, apiKey, maxBatchSize, verbose, interval)
	if err != nil {
		return nil, err
	}
```

## API

#### `exporter, err := monitaraexporter.New(hosts, apiKey, maxBatchSize, verbose, interval)`

| Option         | Default | Description                                    |
| :------------- | :------ | :--------------------------------------------- |
| `hosts`        | ``      | Monitara Opentelemetry end points              |
| `apiKey`       | ``      | Monitara open telemetry apiKey                 |
| `maxBatchSize` | `200`   | Max documents size per task                    |
| `interval`     | `2000`  | How often should new tasks be executed (in ms) |
| `verbose`      | `true`  | Verbose mode                                   |


## Example With Gin WebFrameWork
```go
import (
	"net/http"

	"github.com/gin-gonic/gin"
	//monitara exporter
	monitaraexporter "github.com/hamzaabujarad/otelmonitara-exporter"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

func initTracer() (*sdktrace.TracerProvider, error) {
	//Monitara Opentelemetry end points
	hosts := []string{"YOUR_END_POINT"}
	//Monitara open telemetry apiKey
	apiKey := "YOUR_API_KEY"
	//Max documents size per task
	maxBatchSize := 100
	//debug mode on or off
	verbose := true
	//How often should new tasks be executed (in ms)
	interval := 5000
	//calling the monitara exporter
	exporter, err := monitaraexporter.New(hosts, apiKey, maxBatchSize, verbose, interval)
	if err != nil {
		return nil, err
	}
	// For the demonstration, use sdktrace.AlwaysSample sampler to sample all traces.
	// In a production application, use sdktrace.ProbabilitySampler with a desired probability.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("YourServicesName"))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp, err
}

func main() {
	tp, _ := initTracer()
	r := gin.Default()
	otel.SetTracerProvider(tp)
	r.Use(otelgin.Middleware("todo-service")) // Your service name will be shown in tags.
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": "hello world"})
	})
	r.Run()
}

```

