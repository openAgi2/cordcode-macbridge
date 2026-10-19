//go:build realdata

package gobridge

// S-2 E-6 realdata 证据（方案 §3.2/§5 E-6：本地 realdata tag 测试——owner 事故
// transcript，测试代码不提交内容）。默认构建不编译（go:build realdata）：
//
//	go test ./go-bridge -run TestMaterializationRealdataIncidentTranscript -tags realdata -count=1 -v
//
// 验证：101MB 级真实 transcript 上的 (ii) 分类 digest 命中 + suffix 折叠基线 +
// 大投影 round-trip/差分写（IO 如实 = committed 前缀整读，§3.2.3 档 (a)）。

import (
	"os"
	"strings"
	"testing"
	"time"
)

const materializationRealdataTranscript = "/Users/jacklee/.claude/projects/-Users-jacklee-Projects-cordcode-ios-native-message-timeline/bbe396e6-11ea-4754-943e-df5d2effcd14.jsonl"

func TestMaterializationRealdataIncidentTranscript(t *testing.T) {
	info, err := os.Stat(materializationRealdataTranscript)
	if err != nil {
		t.Skipf("realdata transcript unavailable: %v", err)
	}
	store, _ := materializationTestStore(t)

	// 段 = 整文件（committed 前缀取 min(size, 32MiB)——101MB digest 已覆盖真实
	// 量级；全量 digest 亦可，时间预算考虑取 32MiB 前缀 + 完整 suffix 语义验证）。
	committed := info.Size()
	if committed > 32<<20 {
		committed = 32 << 20
	}
	full := ProjectionSourceDescriptor{
		Identity: "bbe396e6-11ea-4754-943e-df5d2effcd14",
		Path:     materializationRealdataTranscript,
		Cursor:   committed,
	}
	started := time.Now()
	sources, err := BuildProjectionSourceCheckpoints(full)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("prefix digest (%d bytes): %v", committed, time.Since(started))

	// 大投影（119 turns，对齐事故样本 turn 数；每 turn ~300KB 文本模拟 34MB 投影）。
	bigText := strings.Repeat("r", 300*1024)
	turns := make([]TurnProjection, 0, 119)
	for i := 0; i < 119; i++ {
		turns = append(turns, TurnProjection{
			TurnID:    "turn-real-" + itoaMaterialization(i),
			Status:    "completed",
			StartedAt: int64(i),
			Assistant: &MessageProjection{
				ID:    "m-" + itoaMaterialization(i),
				Role:  "assistant",
				Parts: []ProjectionPart{{Type: "text", Text: bigText}},
			},
		})
	}
	projection := SessionProjection{
		SessionID: full.Identity,
		SyncRev:   900,
		Execution: ExecutionView{Phase: "running", ActiveTurnID: "turn-real-118"},
		Turns:     turns,
	}
	saveStart := time.Now()
	if err := store.Save(NewReadyProjectionCheckpoint("claude", full.Identity, sources[0], projection, time.Now())); err != nil {
		t.Fatal(err)
	}
	t.Logf("seed save (119 turns ~35MB): %v", time.Since(saveStart))

	// 重启恢复（(iii) 全等零读——文件未变）。
	loadStart := time.Now()
	loaded, err := store.LoadValidated("claude", full.Identity, materializationDescriptorFor(full))
	if err != nil {
		t.Fatalf("realdata reload: %v", err)
	}
	t.Logf("reload (fast-path): %v turns=%d", time.Since(loadStart), len(loaded.Projection.Turns))
	if len(loaded.Projection.Turns) != 119 || loaded.Source.Cursor != committed {
		t.Fatalf("realdata round-trip drift: turns=%d cursor=%d", len(loaded.Projection.Turns), loaded.Source.Cursor)
	}

	// suffix 推进（(ii)：digest committed 前缀 + 推进接受）。
	advanced := full
	advanced.Cursor = info.Size()
	advStart := time.Now()
	loaded2, err := store.LoadValidated("claude", full.Identity, materializationDescriptorFor(advanced))
	if err != nil {
		t.Fatalf("realdata suffix advance: %v", err)
	}
	t.Logf("suffix-advance load (digest %d bytes): %v base-cursor=%d", committed, time.Since(advStart), loaded2.Source.Cursor)
	if loaded2.Source.Cursor != committed {
		t.Fatalf("advance must restore stored cursor %d as fold base", committed)
	}

	// 差分写：改 1 turn。
	turns[60].Assistant.Parts[0].Text = bigText + "-mutated"
	next := projection
	next.SyncRev = 901
	next.Turns = turns
	diffStart := time.Now()
	if err := store.Save(NewReadyProjectionCheckpoint("claude", full.Identity, sources[0], next, time.Now())); err != nil {
		t.Fatal(err)
	}
	t.Logf("diff save (1 turn changed): %v", time.Since(diffStart))
	loaded3, err := store.LoadValidated("claude", full.Identity, materializationDescriptorFor(full))
	if err != nil || loaded3.Projection.Turns[60].Assistant.Parts[0].Text != bigText+"-mutated" {
		t.Fatalf("realdata diff drift: %v", err)
	}
}
