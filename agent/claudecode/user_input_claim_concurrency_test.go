package claudecode

// user_input_claim_concurrency_test.go 锁定 §4.5（设计 v6）的 claim 并发收口、
// submitted at-least-once 与 cancel/teardown 唤醒：
//   - A 成功/B 等待 → B already_resolved（无 ok=true+pending）；
//   - A 失败/B 接管（B 重新 Claim 写一次 control response）；
//   - A/B 超时 → retryable claim_timeout；
//   - control cancel 唤醒 waiter（interaction_not_found）；
//   - session teardown（Clear）唤醒 waiter；
//   - control write 次数 ≤ 1（任何路径）。

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// blockingStdin 是可编程阻塞的 stdin：首次写阻塞直到 release（模拟 A 写慢）。
type blockingStdin struct {
	mu      sync.Mutex
	buf     strings.Builder
	block   chan struct{}
	failAll bool
}

func newBlockingStdin() *blockingStdin {
	return &blockingStdin{block: make(chan struct{})}
}

func (b *blockingStdin) Write(p []byte) (int, error) {
	b.mu.Lock()
	if b.failAll {
		b.mu.Unlock()
		return 0, context.DeadlineExceeded
	}
	block := b.block
	b.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-time.After(5 * time.Second):
			// 测试保护：不应永久阻塞
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.WriteString(string(p))
}

func (b *blockingStdin) Close() error { return nil }

func (b *blockingStdin) unblock() {
	b.mu.Lock()
	if b.block != nil {
		close(b.block)
		b.block = nil
	}
	b.mu.Unlock()
}

func (b *blockingStdin) lines() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, line := range strings.Split(b.buf.String(), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// TestClaimWaiterATakesOverAfterAFails：A Claim 成功但写失败（ReleaseClaim）→
// 等待中的 B 在同一 RPC 内重新 Claim 并成功写一次 control response。
func TestClaimWaiterATakesOverAfterAFails(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	stdin2 := newBlockingStdin()
	stdin2.failAll = true // A 的写全部失败
	cs.stdin = stdin2
	cs.handleControlRequest(makeAskControlRequest("req-takeover", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	ui := ev.UserInput
	answers := []core.UserInputAnswer{{QuestionID: ui.Questions[0].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ui.Questions[0].Options[0].ID}}}}

	aDone := make(chan error, 1)
	go func() {
		_, err := cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-A", core.UserInputActionAnswer, answers)
		aDone <- err
	}()
	// 等 A 拿到 claim。
	deadline := time.Now().Add(time.Second)
	for cs.claudeUserInputReg.Status(ui.InteractionID) != claudeUIClaimed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// B 到达：等待 A 的确定结果。
	bDone := make(chan error, 1)
	go func() {
		_, err := cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-B", core.UserInputActionAnswer, answers)
		bDone <- err
	}()
	// A 写失败（failAll）→ ReleaseClaim → B 醒来接管。
	if err := <-aDone; err == nil {
		t.Fatal("A 写失败应返回错误")
	}
	if cs.claudeUserInputReg.Status(ui.InteractionID) != claudeUIPending {
		t.Fatal("A 失败后 claim 应释放回 pending")
	}
	// B 接管：换回可写 stdin。
	cs.stdin = stdin
	if err := <-bDone; err != nil {
		t.Fatalf("B 接管失败: %v", err)
	}
	if stdin.linesWritten() != 1 {
		t.Fatalf("B 接管应写恰好一次 control response，实际 %d", stdin.linesWritten())
	}
	if cs.claudeUserInputReg.Status(ui.InteractionID) != claudeUIResolved {
		t.Fatal("B 成功后 registry 应 resolved")
	}
}

// TestClaimWaiterBothTimeout：A 持 claim 不放（写永久阻塞）→ B 等待超时返回
// retryable claim_timeout 错误（不是成功 pending）。
func TestClaimWaiterBothTimeout(t *testing.T) {
	cs, _ := newAskV2TestSession(t)
	stdin2 := newBlockingStdin()
	cs.stdin = stdin2 // 永不 release → A 卡在写
	cs.handleControlRequest(makeAskControlRequest("req-stuck", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	ui := ev.UserInput
	answers := []core.UserInputAnswer{{QuestionID: ui.Questions[0].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ui.Questions[0].Options[0].ID}}}}

	go func() {
		_, _ = cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-A", core.UserInputActionAnswer, answers)
	}()
	deadline := time.Now().Add(time.Second)
	for cs.claudeUserInputReg.Status(ui.InteractionID) != claudeUIClaimed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	resolution, err := cs.ResolveUserInput(ctx, ui.InteractionID, "client-B", core.UserInputActionAnswer, answers)
	if err == nil {
		t.Fatalf("B 等待超时应返回错误，实际成功 %+v", resolution)
	}
	uie, ok := err.(*core.UserInputError)
	if !ok || uie.Code != "claim_timeout" {
		t.Fatalf("应为 retryable claim_timeout，实际 %T %v", err, err)
	}
	stdin2.unblock()
}

// TestClaimWaiterControlCancelWakes：B 等待期间 control_cancel_request 按
// request ID Remove entry → B 醒来收到 interaction_not_found（停止写）。
func TestClaimWaiterControlCancelWakes(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	stdin2 := newBlockingStdin()
	cs.stdin = stdin2
	cs.handleControlRequest(makeAskControlRequest("req-cancel", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	ui := ev.UserInput
	answers := []core.UserInputAnswer{{QuestionID: ui.Questions[0].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ui.Questions[0].Options[0].ID}}}}

	go func() {
		_, _ = cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-A", core.UserInputActionAnswer, answers)
	}()
	deadline := time.Now().Add(time.Second)
	for cs.claudeUserInputReg.Status(ui.InteractionID) != claudeUIClaimed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	bDone := make(chan error, 1)
	go func() {
		_, err := cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-B", core.UserInputActionAnswer, answers)
		bDone <- err
	}()
	time.Sleep(50 * time.Millisecond) // 让 B 进入等待
	// CLI 取消该 control request。
	cs.handleReadLoopLine(`{"type":"control_cancel_request","request_id":"req-cancel"}`)
	stdin2.unblock()
	err := <-bDone
	uie, ok := err.(*core.UserInputError)
	if !ok || uie.Code != "interaction_not_found" {
		t.Fatalf("cancel 后 B 应收到 interaction_not_found，实际 %T %v", err, err)
	}
	_ = stdin
}

// TestClaimWaiterSessionTeardownWakes：B 等待期间 session teardown（Clear）→
// B 醒来收到 interaction_not_found。
func TestClaimWaiterSessionTeardownWakes(t *testing.T) {
	cs, _ := newAskV2TestSession(t)
	stdin2 := newBlockingStdin()
	cs.stdin = stdin2
	cs.handleControlRequest(makeAskControlRequest("req-teardown", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	ui := ev.UserInput
	answers := []core.UserInputAnswer{{QuestionID: ui.Questions[0].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ui.Questions[0].Options[0].ID}}}}

	go func() {
		_, _ = cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-A", core.UserInputActionAnswer, answers)
	}()
	deadline := time.Now().Add(time.Second)
	for cs.claudeUserInputReg.Status(ui.InteractionID) != claudeUIClaimed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	bDone := make(chan error, 1)
	go func() {
		_, err := cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-B", core.UserInputActionAnswer, answers)
		bDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cs.claudeUserInputReg.Clear()
	stdin2.unblock()
	err := <-bDone
	uie, ok := err.(*core.UserInputError)
	if !ok || uie.Code != "interaction_not_found" {
		t.Fatalf("teardown 后 B 应收到 interaction_not_found，实际 %T %v", err, err)
	}
}

// TestControlResponseWrittenAtMostOnce：并发 N 个 claimant + 1 个成功 →
// backend 恰好写一次 control response（first-writer-wins + waiter 收口）。
func TestControlResponseWrittenAtMostOnce(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-once", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	ui := ev.UserInput
	answers := []core.UserInputAnswer{{QuestionID: ui.Questions[0].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ui.Questions[0].Options[0].ID}}}}

	const n = 5
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-"+string(rune('A'+i)), core.UserInputActionAnswer, answers)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("并发 claimant 不应失败: %v", err)
		}
	}
	if stdin.linesWritten() != 1 {
		t.Fatalf("control response 应恰好写一次，实际 %d", stdin.linesWritten())
	}
}

// TestSubmittedReplayAfterFirstLoss：首次 submitted 事件丢失（模拟：确认 committed
// 后事件被丢弃）→ 同 action 重试幂等重发 submitted、不重写 control response。
func TestSubmittedReplayAfterFirstLoss(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-loss", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	ui := ev.UserInput
	answers := []core.UserInputAnswer{{QuestionID: ui.Questions[0].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ui.Questions[0].Options[0].ID}}}}

	if _, err := cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-A", core.UserInputActionAnswer, answers); err != nil {
		t.Fatalf("首次 answer: %v", err)
	}
	// 丢弃首次 submitted + resolved（模拟 Kernel 前丢失）。
	drainAllEvents(cs)
	before := stdin.linesWritten()

	// 同 action 重试：重发 submitted、不重写 control response。
	if _, err := cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-A", core.UserInputActionAnswer, answers); err != nil {
		t.Fatalf("重试: %v", err)
	}
	if stdin.linesWritten() != before {
		t.Fatalf("重试不得重写 control response，before=%d after=%d", before, stdin.linesWritten())
	}
	events := drainAllEvents(cs)
	submittedCount := 0
	for _, e := range events {
		if e.Type == core.EventUserInputSubmitted {
			submittedCount++
		}
	}
	if submittedCount != 1 {
		t.Fatalf("重试应重发恰好 1 个 submitted，实际 %d", submittedCount)
	}

	// 新 action id 重试（另一客户端）：already_resolved + 重发 submitted。
	if _, err := cs.ResolveUserInput(context.Background(), ui.InteractionID, "client-B", core.UserInputActionAnswer, answers); err != nil {
		t.Fatalf("新 action 重试: %v", err)
	}
	if stdin.linesWritten() != before {
		t.Fatalf("新 action 重试也不得重写 control response")
	}
	events = drainAllEvents(cs)
	submittedCount = 0
	for _, e := range events {
		if e.Type == core.EventUserInputSubmitted {
			submittedCount++
		}
	}
	if submittedCount != 1 {
		t.Fatalf("新 action 重试应重发恰好 1 个 submitted，实际 %d", submittedCount)
	}
}
