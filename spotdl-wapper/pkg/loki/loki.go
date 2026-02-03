package loki

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap/zapcore"
)

// LokiCore is a custom zap core that pushes logs to Grafana Loki
type LokiCore struct {
	url           string
	labels        map[string]string
	client        *http.Client
	encoder       zapcore.Encoder
	level         zapcore.Level
	batch         []lokiEntry
	batchMu       sync.Mutex
	batchSize     int
	flushInterval time.Duration
	stopCh        chan struct{}
}

type lokiEntry struct {
	ts  time.Time
	msg string
}

type lokiPushRequest struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

// NewLokiCore creates a new LokiCore that pushes logs to the specified Loki URL
func NewLokiCore(lokiURL string, labels map[string]string, level zapcore.Level) *LokiCore {
	cfg := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// Normalize the URL - handle both base URL and full path
	url := strings.TrimSuffix(lokiURL, "/")
	if !strings.HasSuffix(url, "/loki/api/v1/push") {
		url = url + "/loki/api/v1/push"
	}

	core := &LokiCore{
		url:           url,
		labels:        labels,
		client:        &http.Client{Timeout: 10 * time.Second},
		encoder:       zapcore.NewJSONEncoder(cfg),
		level:         level,
		batch:         make([]lokiEntry, 0, 100),
		batchSize:     100,
		flushInterval: 5 * time.Second,
		stopCh:        make(chan struct{}),
	}

	fmt.Printf("[loki] initialized, pushing to: %s\n", core.url)
	go core.flushLoop()
	return core
}

// Enabled returns true if the given level is enabled
func (c *LokiCore) Enabled(level zapcore.Level) bool {
	return level >= c.level
}

// With adds structured context to the core
func (c *LokiCore) With(fields []zapcore.Field) zapcore.Core {
	clone := &LokiCore{
		url:           c.url,
		labels:        c.labels,
		client:        c.client,
		encoder:       c.encoder.Clone(),
		level:         c.level,
		batch:         c.batch,
		batchMu:       sync.Mutex{},
		batchSize:     c.batchSize,
		flushInterval: c.flushInterval,
		stopCh:        c.stopCh,
	}
	for _, f := range fields {
		f.AddTo(clone.encoder)
	}
	return clone
}

// Check determines whether the supplied entry should be logged
func (c *LokiCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
}

// Write serializes the entry and adds it to the batch
func (c *LokiCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	buf, err := c.encoder.EncodeEntry(entry, fields)
	if err != nil {
		return err
	}

	c.batchMu.Lock()
	c.batch = append(c.batch, lokiEntry{ts: entry.Time, msg: buf.String()})
	shouldFlush := len(c.batch) >= c.batchSize
	c.batchMu.Unlock()

	if shouldFlush {
		go c.flush()
	}

	return nil
}

// Sync flushes buffered logs
func (c *LokiCore) Sync() error {
	c.flush()
	return nil
}

// Stop stops the flush loop
func (c *LokiCore) Stop() {
	close(c.stopCh)
	c.flush()
}

func (c *LokiCore) flushLoop() {
	ticker := time.NewTicker(c.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.flush()
		case <-c.stopCh:
			return
		}
	}
}

func (c *LokiCore) flush() {
	c.batchMu.Lock()
	if len(c.batch) == 0 {
		c.batchMu.Unlock()
		return
	}
	toSend := c.batch
	c.batch = make([]lokiEntry, 0, 100)
	c.batchMu.Unlock()

	values := make([][]string, len(toSend))
	for i, e := range toSend {
		values[i] = []string{strconv.FormatInt(e.ts.UnixNano(), 10), e.msg}
	}

	payload := lokiPushRequest{
		Streams: []lokiStream{{Stream: c.labels, Values: values}},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("loki marshal error: %v\n", err)
		return
	}

	req, err := http.NewRequest("POST", c.url, bytes.NewReader(data))
	if err != nil {
		fmt.Printf("loki request error: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		fmt.Printf("[loki] push error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("[loki] push failed with status %d: %s\n", resp.StatusCode, string(body))
	} else {
		fmt.Printf("[loki] pushed %d log entries\n", len(toSend))
	}
}
