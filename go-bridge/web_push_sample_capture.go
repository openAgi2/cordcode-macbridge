package gobridge

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// web_push_sample_capture.go — 真实样本采集钩子（设计 delta §3，监工指令 1 号）。
//
// 环境变量 CCCODE_WEB_PUSH_SAMPLE_CAPTURE=1 时启用（缺省关闭，生产零行为差异）。
// 产物：<dataDir>/web-push-samples/<GATE-ID>.jsonl（0600 追加写，异步缓冲——
// 采集绝不反向阻塞事件转发或投递主路径）。记录只含脱敏字段：id 一律 8 字符前缀+
// 长度，rawShape 递归脱敏所有字符串叶子（只证明字段形状，不保留正文）。
// 用途：EVT-TURN-1/EVT-PERM-1/WP-SUB-*/WP-RESP-* 真实样本归档与样本门翻转证据。

var webPushSampleCaptureEnabled atomic.Bool

type webPushSampleRecord struct {
	gateID string
	fields map[string]interface{}
}

type webPushSampleWriter struct {
	dir  string
	ch   chan webPushSampleRecord
	done chan struct{}
}

var webPushSamples *webPushSampleWriter

// initWebPushSampleCapture 在启动时（dataDir 已知后）调用一次。
func initWebPushSampleCapture(dataDir string) {
	if os.Getenv("CCCODE_WEB_PUSH_SAMPLE_CAPTURE") != "1" {
		return
	}
	dir := filepath.Join(dataDir, "web-push-samples")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("web-push: sample capture disabled (mkdir failed)", "error", err.Error())
		return
	}
	writer := &webPushSampleWriter{
		dir:  dir,
		ch:   make(chan webPushSampleRecord, 64),
		done: make(chan struct{}),
	}
	go writer.run()
	webPushSamples = writer
	webPushSampleCaptureEnabled.Store(true)
	slog.Info("web-push: sample capture enabled", "dir", dir)
}

func (w *webPushSampleWriter) run() {
	defer close(w.done)
	for record := range w.ch {
		w.append(record)
	}
}

func (w *webPushSampleWriter) append(record webPushSampleRecord) {
	raw, err := json.Marshal(record.fields)
	if err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(w.dir, record.gateID+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(raw, '\n'))
}

// captureWebPushSample 提交一条脱敏采样记录。非阻塞：缓冲满即丢弃（采集是诊断，
// 绝不让样本采集影响主路径）。
func captureWebPushSample(gateID string, fields map[string]interface{}) {
	if !webPushSampleCaptureEnabled.Load() || webPushSamples == nil {
		return
	}
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields["capturedAt"] = time.Now().UTC().Format(time.RFC3339)
	select {
	case webPushSamples.ch <- webPushSampleRecord{gateID: gateID, fields: fields}:
	default:
	}
}

// drainWebPushSamplesForTest 等待后台 writer 清空缓冲（测试用）。
func drainWebPushSamplesForTest() {
	if webPushSamples == nil {
		return
	}
	for len(webPushSamples.ch) > 0 {
		time.Sleep(time.Millisecond)
	}
}

// webPushRedactID 把识别字段脱敏为「8 字符前缀:总长度」；空串保持空。
func webPushRedactID(s string) string {
	if s == "" {
		return ""
	}
	prefix := []rune(s)
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return string(prefix) + ":" + strconv.Itoa(len([]rune(s)))
}

// webPushRedactShape 递归脱敏结构化副本：所有字符串叶子变为前缀:长度，
// 保留字段名/布尔/数值以证明字段形状；深度≤4、map≤16 键、slice≤8 元素。
func webPushRedactShape(v interface{}, depth int) interface{} {
	if depth > 4 {
		return "<depth-cap>"
	}
	switch t := v.(type) {
	case string:
		return webPushRedactID(t)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		n := 0
		for key, value := range t {
			if n >= 16 {
				out["<truncated-keys>"] = "<more>"
				break
			}
			out[key] = webPushRedactShape(value, depth+1)
			n++
		}
		return out
	case []interface{}:
		limit := len(t)
		if limit > 8 {
			limit = 8
		}
		out := make([]interface{}, 0, limit)
		for i := 0; i < limit; i++ {
			out = append(out, webPushRedactShape(t[i], depth+1))
		}
		if len(t) > limit {
			out = append(out, "<more-elems:"+strconv.Itoa(len(t)-limit)+">")
		}
		return out
	default:
		return v // bool / float64 / nil
	}
}
