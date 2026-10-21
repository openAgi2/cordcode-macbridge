//go:build realdata

package gobridge

// T7（修复方案 v2.1 §7，realdata tag；R4-5 重写口径）：本机真实 transcript 按
// **relay 内容入口**统计（admitted、非 interrupt、UUID+Message 齐备、user/assistant、
// 非 echo-only——即 handlers_relay.go 中进入 applyClaudeLiveSourceRecord 的行），
// 三向不变量守护：
//   1. 可投影 ∧ mapper 事件 = 0 → 违例（分类器与 mapper 漂移）
//   2. 白名单 ∧ mapper 事件 > 0 → 违例（白名单把有展示的行放过了）
//   3. 不支持 ∧ mapper 事件 > 0 → 违例（不支持却实际产出事件）
// 并输出 poison 计数（RC-B 修复后对象 error 不再 poison；语法错误仍会）。
//
// 运行（默认构建不编译）：
//
//	GOTOOLCHAIN=local go test ./go-bridge -tags realdata -count=1 -run TestClaudeSourceRowClassificationRealdataCensus -v
//
// 只输出计数，不打印正文/路径。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeSourceRowClassificationRealdataCensus(t *testing.T) {
	root := os.Getenv("HOME") + "/.claude/projects"
	cutoff := time.Now().AddDate(0, 0, -14)
	var files []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info.ModTime().After(cutoff) {
			files = append(files, path)
		}
		return nil
	})
	if len(files) == 0 {
		t.Skip("no recent transcripts on this machine")
	}
	counts := map[claudeSourceRowContentClass]int{
		claudeRowContentProjectable: 0, claudeRowContentProvenNoDisplay: 0, claudeRowContentUnsupported: 0,
	}
	poisonFiles := 0
	contentRows := 0
	violations := [3]int{}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		scan, err := scanCompleteClaudeRelayEntriesFromReader(strings.NewReader(string(data)), 0, &claudeRelayScanState{})
		if err != nil || scan.Poison != nil {
			poisonFiles++
			continue
		}
		for _, record := range scan.Records {
			if !record.Admitted {
				continue
			}
			e := record.Entry
			// relay 内容入口谓词（handlers_relay.go apply 分支同源）。
			if e.UUID == "" || e.Message == nil || (e.Type != "user" && e.Type != "assistant") {
				continue
			}
			if isClaudeUserInterruptRelayEntry(e) || isClaudeEchoOnlyUserRow(e) {
				continue
			}
			contentRows++
			class := classifyClaudeSourceRowContent(e)
			counts[class]++
			turnID := "census"
			events := claudeEntryToProjectionEvents(e, &turnID, nil, nil)
			switch {
			case class == claudeRowContentProjectable && len(events) == 0:
				violations[0]++
			case class == claudeRowContentProvenNoDisplay && len(events) > 0:
				violations[1]++
			case class == claudeRowContentUnsupported && len(events) > 0:
				violations[2]++
			}
		}
	}
	t.Logf("census(content-entry): files=%d poisonFiles=%d contentRows=%d projectable=%d proven_no_display=%d unsupported=%d violations(projectable0ev/provenDisplay/unsupportedDisplay)=%d/%d/%d",
		len(files), poisonFiles, contentRows,
		counts[claudeRowContentProjectable], counts[claudeRowContentProvenNoDisplay], counts[claudeRowContentUnsupported],
		violations[0], violations[1], violations[2])
	if violations[0] != 0 || violations[1] != 0 || violations[2] != 0 {
		t.Fatalf("三向不变量违例：%+v（分类器与 mapper 漂移）", violations)
	}
}
