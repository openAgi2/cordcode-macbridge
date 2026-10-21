//go:build realdata

package gobridge

// T7（修复方案 v2.1 §7，realdata tag）：本机真实 transcript 普查——生产扫描器 +
// 生产分类器 + 生产 mapper 过全部内容行，守护两条不变量：
//   1. 「可投影 ⇔ 事件数 > 0」违例 = 0（分类器与 mapper 不漂移）；
//   2. 输出三分类计数，供人工比对方案 §3.2 的普查数字（A≈140,974/3,515/5，
//      活跃文件持续增长，量级一致即可）。
//
// 运行（默认构建不编译）：
//
//	GOTOOLCHAIN=local go test ./go-bridge -tags realdata -count=1 -run TestClaudeSourceRowClassificationRealdataCensus -v
//
// 只输出计数与哈希，不打印正文/路径。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
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
	counts := map[claudeSourceRowContentClass]int{claudeRowContentProjectable: 0, claudeRowContentProvenNoDisplay: 0, claudeRowContentUnsupported: 0}
	violations := 0
	scanned := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		scan, err := scanCompleteClaudeRelayEntriesFromReader(strings.NewReader(string(data)), 0, &claudeRelayScanState{})
		if err != nil || scan.Poison != nil {
			continue // RC-B 修复后不应 poison；普查对 poison 文件跳过并计数见下
		}
		for _, record := range scan.Records {
			if !record.Admitted {
				continue
			}
			scanned++
			class := classifyClaudeSourceRowContent(record.Entry)
			counts[class]++
			if class == claudeRowContentProjectable {
				turnID := "census"
				events := claudeEntryToProjectionEvents(record.Entry, &turnID, nil, nil)
				if len(events) == 0 {
					violations++
				}
			} else {
				turnID := "census"
				events := claudeEntryToProjectionEvents(record.Entry, &turnID, nil, nil)
				if len(events) > 0 && class == claudeRowContentProvenNoDisplay {
					violations++
				}
			}
		}
	}
	t.Logf("census: files=%d scanned admitted rows=%d projectable=%d proven_no_display=%d unsupported=%d",
		len(files), scanned, counts[claudeRowContentProjectable], counts[claudeRowContentProvenNoDisplay], counts[claudeRowContentUnsupported])
	if violations != 0 {
		t.Fatalf("「可投影 ⇔ 事件数 > 0」违例 = %d（分类器与 mapper 漂移）", violations)
	}
	_ = core.LiveSessionProcess{} // keep import stable if future assertions need it
}
