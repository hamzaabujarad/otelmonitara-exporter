package monitaraexporter

import (
	"bytes"
	"context"
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dustin/go-humanize"
	elasticsearch "github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/elastic/go-elasticsearch/v8/esutil"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type CustomSpan struct {
	ActivityId string                 `json:"activityId"`
	ParentId   *string                `json:"parentId"`
	TraceId    string                 `json:"traceId"`
	SpanId     string                 `json:"spanId"`
	SourceName string                 `json:"sourceName"`
	Status     string                 `json:"status"`
	Duration   time.Duration          `json:"duration"`
	Kind       int                    `json:"kind"`
	Timestamp  time.Time              `json:"timestamp"`
	Tags       map[string]interface{} `json:"tags"`
}

var (
	numWorkers      int
	flushBytes      int
	numItems        int
	customSpans            = []CustomSpan{}
	mBatchSize      int    = 200
	documentIndex   string = ""
	buf             bytes.Buffer
	raw             map[string]interface{}
	_               trace.SpanExporter = &Exporter{}
	countSuccessful uint64
	logTag          string = "[Monitara-Exporter]"
	ticker          int64  = 2000
)

// New creates an Exporter with the passed options.
func New(hosts []string, apiKey string, maxBatchSize int, verbose bool, interval int) (*Exporter, error) {
	if !verbose {
		log.SetOutput(ioutil.Discard)
	}
	if interval > 0 {
		_ticker := int64(interval)
		ticker = _ticker
	}

	if len(hosts) == 0 {
		log.Fatalf("%s At least one host is required.", logTag)
	}

	if len(apiKey) == 0 {
		log.Fatalf("%s ApiKey is required.", logTag)
	}
	if maxBatchSize > 0 {
		mBatchSize = maxBatchSize
	}
	//generate document index
	GetIndexNamePrefixFromApiKey(apiKey)

	esCfg := elasticsearch.Config{
		Addresses: hosts,
		APIKey:    b64.StdEncoding.EncodeToString([]byte(apiKey)),
		// ...
	}
	es, err := elasticsearch.NewClient(esCfg)
	if err != nil {
		log.Fatalf("%s Error creating the client: %s", logTag, err)
	}

	res, err := es.Info()
	if err != nil {
		log.Fatalf("%s Error getting response: %s", logTag, err)
	}

	defer res.Body.Close()
	log.Println(fmt.Sprintf("%s Exporting has been Starting", logTag))

	return &Exporter{
		elastic: *es,
	}, nil
}

// Exporter is an implementation
type Exporter struct {
	encoderMu sync.Mutex
	elastic   elasticsearch.Client
	stoppedMu sync.RWMutex
	stopped   bool
}

// ExportSpans writes spans in custom format
func (e *Exporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {

	e.stoppedMu.RLock()
	stopped := e.stopped
	e.stoppedMu.RUnlock()
	if stopped {
		return nil
	}

	if len(spans) == 0 {
		return nil
	}

	stubs := tracetest.SpanStubsFromReadOnlySpans(spans)

	e.encoderMu.Lock()
	defer e.encoderMu.Unlock()

	for _, stub := range stubs {

		numAttrs := len(stub.Attributes) + stub.Resource.Len() + 2

		attrs := make(map[string]interface{}, numAttrs)
		for _, kv := range stub.Attributes {
			attrs[string(kv.Key)] = kv.Value.AsInterface()
		}
		span := CustomSpan{
			ActivityId: stub.SpanContext.SpanID().String(),
			TraceId:    stub.SpanContext.TraceID().String(),
			SpanId:     stub.SpanContext.SpanID().String(),
			SourceName: stub.Name,
			Status:     "Unset",
			Duration:   stub.EndTime.Sub(stub.StartTime),
			Kind:       int(stub.SpanKind),
			Timestamp:  stub.StartTime.UTC(),
			Tags:       attrs,
		}

		var parentSpanID string
		if stub.Parent.SpanID().IsValid() {
			parentSpanID = stub.Parent.SpanID().String()
			span.ParentId = &parentSpanID
		}
		customSpans = append(customSpans, span)

	}

	log.Println(fmt.Sprintf("%s Added %d spans to the container with total %d spans", logTag, len(stubs), len(customSpans)))

	if len(customSpans) >= mBatchSize {
		chunkSpans := chunkSpans(customSpans, mBatchSize)
		for i, chunk := range chunkSpans {
			if i > 0 {
				time.Sleep(time.Duration(ticker) * time.Millisecond)
			}

			InsertBulk(&e.elastic, chunk)
		}

	}
	return nil
}

func InsertBulk(es *elasticsearch.Client, spans []CustomSpan) error {
	log.Println(fmt.Sprintf("%s strating indexing the spans", logTag))
	var (
		countSuccessful uint64
		res             *esapi.Response
	)

	bi, err := esutil.NewBulkIndexer(esutil.BulkIndexerConfig{
		Index:  documentIndex, // The default index name
		Client: es,            // The Elasticsearch client
	})
	if err != nil {
		log.Fatalf("%s Error creating the indexer: %s", logTag, err)
	}

	res, err = es.Indices.Create(documentIndex)
	if err != nil {
		log.Fatalf("%s Cannot create index: %s", logTag, err)
	}
	res.Body.Close()

	start := time.Now().UTC()

	//
	for _, span := range spans {
		// Prepare the data payload: encode article to JSON
		//
		data, err := json.Marshal(span)
		if err != nil {
			log.Fatalf("%s Cannot encode spans %s: %s", logTag, span.SpanId, err)
		}
		// Add an item to the BulkIndexer
		err = bi.Add(
			context.Background(),
			esutil.BulkIndexerItem{
				// Action field configures the operation to perform (index, create, delete, update)
				Action:     "index",
				DocumentID: span.SpanId,

				// Body is an `io.Reader` with the payload
				Body: bytes.NewReader(data),

				// OnSuccess is called for each successful operation
				OnSuccess: func(ctx context.Context, item esutil.BulkIndexerItem, res esutil.BulkIndexerResponseItem) {
					atomic.AddUint64(&countSuccessful, 1)
				},

				// OnFailure is called for each failed operation
				OnFailure: func(ctx context.Context, item esutil.BulkIndexerItem, res esutil.BulkIndexerResponseItem, err error) {
					if err != nil {
						log.Printf("%sERROR: %s", logTag, err)
					} else {
						log.Printf("%s ERROR: %s: %s", logTag, res.Error.Type, res.Error.Reason)
					}
				},
			},
		)
		if err != nil {
			log.Fatalf("%s Unexpected error: %s", logTag, err)
		}
	}
	if err := bi.Close(context.Background()); err != nil {
		log.Fatalf("%s Unexpected error: %s", logTag, err)
	}

	biStats := bi.Stats()

	dur := time.Since(start)

	if biStats.NumFailed > 0 {
		log.Fatalf(
			"%s Indexed [%s] documents with [%s] errors in %s",
			logTag,
			humanize.Comma(int64(biStats.NumFlushed)),
			humanize.Comma(int64(biStats.NumFailed)),
			dur.Truncate(time.Millisecond),
		)
	} else {
		log.Printf(
			"%s Sucessfuly indexed [%s] documents in %s",
			logTag,
			humanize.Comma(int64(biStats.NumFlushed)),
			dur.Truncate(time.Millisecond),
		)
	}
	customSpans = []CustomSpan{}

	return nil
}

// Shutdown is called to stop the exporter, it preforms no action.
func (e *Exporter) Shutdown(ctx context.Context) error {
	e.stoppedMu.Lock()
	e.stopped = true
	e.stoppedMu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}

// generate Documents index
func GetIndexNamePrefixFromApiKey(apikey string) {
	parts := strings.Split(apikey, ".")
	documentIndex = fmt.Sprintf("account%s_logs_logapplication%s_", parts[1], parts[3])
}

func chunkSpans(slice []CustomSpan, chunkSize int) [][]CustomSpan {
	var chunks [][]CustomSpan
	for i := 0; i < len(slice); i += chunkSize {
		end := i + chunkSize

		// necessary check to avoid slicing beyond
		// slice capacity
		if end > len(slice) {
			end = len(slice)
		}

		chunks = append(chunks, slice[i:end])
	}

	return chunks
}
