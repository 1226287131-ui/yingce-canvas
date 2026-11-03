package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCloudAgentCanvasArgumentFeedbackIdentifiesFailingOperation(t *testing.T) {
	for _, tc := range []struct{ op, field, issue string }{
		{`{"type":"add_node","id":"note","nodeType":"text","snapshotHash":"misplaced"}`, "ops[0].snapshotHash", "unexpected_field"},
		{`{"type":"add_node","nodeType":"text"}`, "ops[0].id", "required"},
		{`{"type":"add_node","id":"note"}`, "ops[0].nodeType", "required"},
		{`{"type":"connect_nodes","id":"edge","toNodeId":"target"}`, "ops[0].fromNodeId", "required"},
		{`{"type":"update_node","id":"note"}`, "ops[0].patch", "required"},
		{`{"type":"add_node","id":"note","nodeType":"text","x":"private-sentinel"}`, "ops[0].x", "type_mismatch"},
		{`{"type":"add_node","id":"note","nodeType":"text","private-sentinel":"private-sentinel"}`, "ops[0]", "unexpected_field"},
	} {
		t.Run(tc.field+tc.issue, func(t *testing.T) {
			call := cloudAgentCall{ID: "write"}
			call.Function.Name, call.Function.Arguments = "canvas_apply_ops", `{"snapshotHash":"snapshot","ops":[`+tc.op+`]}`
			// Malformed calls must fail before reading, approving, or writing.
			_, err := prepareCloudAgentCanvasMutation(nil, "user", "canvas", call)
			if err == nil {
				t.Fatal("invalid operation accepted")
			}
			req := agentTestRequest()
			req.PermissionMode = "request_approval"
			state := &cloudAgentRuntime{Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
			cloudAgentToolResult("run", state, call, nil, err)
			content := state.Canonical.Messages[0]["content"].(string)
			var result map[string]any
			if err := json.Unmarshal([]byte(content), &result); err != nil {
				t.Fatal(err)
			}
			if result["field"] != tc.field || result["issue"] != tc.issue || result["reason"] != "invalid_tool_arguments" || result["parameters"] == nil || strings.Contains(content, "private-sentinel") {
				t.Fatalf("incorrect repair feedback: %s", content)
			}
		})
	}
	// Replay the logged failure with only the misplaced field removed.
	s, _, _ := agentMediaFixture(t)
	canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	doc, _ := creationDocument(canvas.PayloadJSON)
	call := cloudAgentCall{ID: "corrected"}
	call.Function.Name = "canvas_apply_ops"
	call.Function.Arguments = `{"snapshotHash":"` + cloudAgentCanvasHash(doc) + `","ops":[{"type":"add_node","id":"note","nodeType":"text"}]}`
	plan, err := prepareCloudAgentCanvasMutation(s.repo, "user", "agent-canvas", call)
	if err != nil || plan == nil {
		t.Fatalf("corrected call failed: %v", err)
	}
	stored, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if stored.PayloadJSON != canvas.PayloadJSON {
		t.Fatal("preview wrote without approval")
	}
}

func TestCloudAgentGenerationDiagnosticsSeparateOutputAndTask(t *testing.T) {
	s, db, _ := agentMediaFixture(t)
	for _, tc := range []struct {
		name, user, canvas, detail string
		status                     model.TaskStatus
		visible                    bool
	}{
		{"failed", "user", "agent-canvas", "media dimensions must be between 256 and 5760 pixels", model.TaskStatusFailed, true},
		{"cancelled", "user", "agent-canvas", "任务已取消", model.TaskStatusCancelled, true},
		{"sensitive", "user", "agent-canvas", "https://private-sentinel.invalid?token=private-sentinel", model.TaskStatusFailed, true},
		{"other-user", "other", "agent-canvas", "private-sentinel", model.TaskStatusFailed, false},
		{"other-canvas", "user", "other-canvas", "private-sentinel", model.TaskStatusFailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.Task{ID: tc.name, UserID: tc.user, ProjectID: tc.canvas, Status: tc.status, Error: tc.detail}
			if err := db.Create(task).Error; err != nil {
				t.Fatal(err)
			}
			node := map[string]any{"id": "video", "type": "video", "metadata": map[string]any{"status": "loading", "taskId": task.ID}}
			doc := map[string]any{"nodes": []map[string]any{node}}
			view, err := cloudAgentCanvasState(s.repo, "user", "agent-canvas", doc, 0, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			item := view.(map[string]any)["nodes"].([]any)[0].(map[string]any)
			generation := item["generation"].(map[string]any)
			output := item["outputReference"].(map[string]any)
			if output["ready"] != false || output["issue"] == nil || generation["submitBlockedReason"] != "task_bound" {
				t.Fatalf("ambiguous state: %+v", item)
			}
			if tc.visible {
				if generation["taskId"] != task.ID || generation["taskStatus"] != string(tc.status) || generation["error"] == nil {
					t.Fatalf("lost task failure: %+v", generation)
				}
				if tc.name == "failed" && generation["error"] != tc.detail {
					t.Fatalf("lost useful diagnostic: %+v", generation)
				}
			} else if generation["taskStatus"] != "unavailable" || generation["taskId"] != nil || generation["error"] != nil {
				t.Fatalf("task ownership leaked: %+v", generation)
			}
			raw, _ := json.Marshal(view)
			if strings.Contains(string(raw), "private-sentinel") {
				t.Fatal("unsafe error leaked")
			}
			call := cloudAgentCall{ID: "query"}
			call.Function.Name, call.Function.Arguments = "task_get", `{"taskId":"`+task.ID+`"}`
			result, err := cloudAgentReadTool(s.repo, "user", &cloudAgentRuntime{Request: agentTestRequest()}, call)
			if tc.visible {
				if err != nil || result.(map[string]any)["error"] != generation["error"] {
					t.Fatalf("task_get lost diagnostic: %+v %v", result, err)
				}
			} else if err == nil {
				t.Fatal("task_get crossed ownership boundary")
			}
		})
	}
}

func TestCloudAgentMediaAdmissionReportsTaskBinding(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	doc, _ := creationDocument(canvas.PayloadJSON)
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": args.NodeID, "type": "video", "metadata": map[string]any{"status": "error", "taskId": "original-task"}})
	raw, _ := json.Marshal(doc)
	if err := db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	args.SnapshotHash, args.DraftRunID = cloudAgentMediaContentHash(doc), "current-run"
	_, _, _, err := cloudAgentMediaDocument(s.repo, "user", "agent-canvas", args)
	if err == nil {
		t.Fatal("existing task overwritten")
	}
	state := &cloudAgentRuntime{}
	cloudAgentToolResult("run", state, agentMediaCall(args), map[string]any{"phase": "admission", "taskSubmitted": false}, err)
	var result map[string]any
	if err := json.Unmarshal([]byte(state.Canonical.Messages[0]["content"].(string)), &result); err != nil {
		t.Fatal(err)
	}
	if result["reason"] != "task_bound" || result["nodeId"] != args.NodeID || result["taskSubmitted"] != false {
		t.Fatalf("missing admission diagnosis: %+v", result)
	}
	var count int64
	db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count)
	if count != 0 {
		t.Fatal("admission submitted task")
	}
}

func TestCloudAgentCancellationRecordsSourceOnce(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	// Cancellation and cleanup are driven explicitly; do not race the approval waiter.
	s.closeApprovedCloudAgentMediaWaiters()
	run, _ := agentMediaRun(t, s, args, "request_approval")
	if err := s.advanceCloudAgentByID("user", run.ID); err != nil {
		t.Fatal(err)
	}
	approveAgentMediaDraft(t, s, run.ID)
	run, _ = s.repo.CloudAgent("user", run.ID)
	before, err := cloudAgentDecode(run)
	if err != nil || before.MediaTaskID == "" {
		t.Fatalf("missing media task: %v", err)
	}
	for range 2 {
		if err := s.CancelCloudAgent(context.Background(), "user", run.ID); err != nil {
			t.Fatal(err)
		}
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	after, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range after.Events {
		if event.Type != "run_cancelled" {
			continue
		}
		count++
		if event.Payload["source"] != "user_request" || event.Payload["mediaTaskId"] != before.MediaTaskID {
			t.Fatalf("missing cancel source: %+v", event)
		}
	}
	if count != 1 {
		t.Fatalf("expected one cancellation event, got %d", count)
	}
	task, _ := s.repo.TaskForUser("user", before.MediaTaskID)
	if task.Status != model.TaskStatusCancelled {
		t.Fatalf("child not cancelled: %s", task.Status)
	}
}

func TestCloudAgentCancellationRetriesCleanupAfterApprovalWaiter(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	run, _ := agentMediaRun(t, s, args, "request_approval")
	if err := s.advanceCloudAgentByID("user", run.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	waiterPaused, releaseWaiter, waiterDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var armed, pauseQueued, pauseTerminal atomic.Bool
	var cleanupConflicts atomic.Int64
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseWaiter) }) }
	defer release()
	// Pause the real approval waiter on a queued read. Once cancellation has
	// read the terminal child for write-back, let the waiter finish first, so
	// cleanup must encounter a stale revision rather than relying on timing.
	const callbackName = "test:cancel_cleanup_waiter"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		task, ok := tx.Statement.Dest.(*model.Task)
		if !armed.Load() || !ok || tx.Error != nil || tx.RowsAffected == 0 || task.UserID != "user" || task.Type != "canvas_video" {
			return
		}
		if task.Status == model.TaskStatusQueued && pauseQueued.CompareAndSwap(false, true) {
			close(waiterPaused)
			select {
			case <-releaseWaiter:
			case <-ctx.Done():
				tx.AddError(ctx.Err())
			}
		} else if task.Status == model.TaskStatusCancelled && pauseTerminal.CompareAndSwap(false, true) {
			release()
			select {
			case <-waiterDone:
			case <-ctx.Done():
				tx.AddError(ctx.Err())
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove(callbackName)
	const conflictCallback = "test:cancel_cleanup_observe_conflict"
	if err := db.Callback().Update().After("gorm:update").Register(conflictCallback, func(tx *gorm.DB) {
		if pauseTerminal.Load() && tx.Statement.Table == "cloud_agent_executions" && tx.Error == nil && tx.RowsAffected == 0 {
			cleanupConflicts.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Update().Remove(conflictCallback)
	defer func() {
		release()
		cancel()
		s.closeApprovedCloudAgentMediaWaiters()
	}()
	// Install callbacks before approval starts its waiter, and remove them only
	// after that waiter exits, so callback registration itself does not race.
	approveAgentMediaDraft(t, s, run.ID)
	run, state := agentInterjectionState(t, s, run.ID)
	mediaTaskID := state.MediaTaskID
	if mediaTaskID == "" {
		t.Fatal("approved media task missing")
	}
	// Approval also reads the queued child synchronously. Arm the barrier only
	// after it returns; the still-running waiter will read again on its next tick.
	armed.Store(true)
	go func() {
		s.approvedMediaWg.Wait()
		close(waiterDone)
	}()
	select {
	case <-waiterPaused:
	case <-ctx.Done():
		t.Fatal("approval waiter did not reach the queued task")
	}
	if err := s.CancelCloudAgent(ctx, "user", run.ID); err != nil {
		t.Fatal(err)
	}
	if !pauseTerminal.Load() || cleanupConflicts.Load() == 0 {
		t.Fatal("cleanup did not retry a conflict with the approval waiter")
	}
	if err := s.CancelCloudAgent(ctx, "user", run.ID); err != nil {
		t.Fatalf("repeated cancellation was not idempotent: %v", err)
	}
	run, state = agentInterjectionState(t, s, run.ID)
	if run.Status != "cancelled" || run.CleanupPending || run.ActiveTaskID != "" || run.MediaTaskID != "" || state.MediaTaskID != "" {
		t.Fatalf("cancellation cleanup did not converge: %+v", run)
	}
	cancellations, completions := 0, 0
	for _, event := range state.Events {
		if event.Type == "run_cancelled" {
			cancellations++
			if event.Payload["source"] != "user_request" || event.Payload["mediaTaskId"] != mediaTaskID {
				t.Fatalf("cancellation identity changed: %+v", event)
			}
		}
		if event.Type == "canvas_updated" && event.Payload["operation"] == "generate_media_complete" {
			completions++
		}
	}
	if cancellations != 1 || completions != 1 {
		t.Fatalf("duplicate or missing terminal events: cancellations=%d completions=%d", cancellations, completions)
	}
	task, err := s.repo.TaskForUser("user", mediaTaskID)
	if err != nil || task.Status != model.TaskStatusCancelled || task.CancellationSource != model.TaskCancellationParentCancelled {
		t.Fatalf("child cancellation changed: task=%+v err=%v", task, err)
	}
	var tasks, leases int64
	if err := db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentResourceLease{}).Where("run_id = ?", run.ID).Count(&leases).Error; err != nil {
		t.Fatal(err)
	}
	if tasks != 1 || leases != 0 {
		t.Fatalf("cleanup resubmitted media or retained leases: tasks=%d leases=%d", tasks, leases)
	}
}

func TestCloudAgentCancellationCleanupConflictIsBounded(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	s.closeApprovedCloudAgentMediaWaiters()
	run, _ := agentMediaRun(t, s, args, "request_approval")
	var cleanupStarted atomic.Bool
	var conflicts atomic.Int64
	const queryCallback = "test:cancel_cleanup_started"
	if err := db.Callback().Query().After("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
		current, ok := tx.Statement.Dest.(*model.CloudAgentExecution)
		if ok && tx.Error == nil && tx.RowsAffected > 0 && current.ID == run.ID && current.Status == "cancelled" && current.CleanupPending {
			cleanupStarted.Store(true)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove(queryCallback)
	const updateCallback = "test:cancel_cleanup_conflict"
	if err := db.Callback().Update().Before("gorm:update").Register(updateCallback, func(tx *gorm.DB) {
		if cleanupStarted.Load() && tx.Statement.Table == "cloud_agent_executions" {
			conflicts.Add(1)
			tx.AddError(repository.ErrCreationConflict)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Update().Remove(updateCallback)
	if err := s.CancelCloudAgent(context.Background(), "user", run.ID); !errors.Is(err, repository.ErrCreationConflict) {
		t.Fatalf("cleanup conflict was swallowed: %v", err)
	}
	if conflicts.Load() != 8 {
		t.Fatalf("cleanup retry was not bounded: %d attempts", conflicts.Load())
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	if run.Status != "cancelled" || !run.CleanupPending {
		t.Fatalf("failed cleanup lost cancellation intent: %+v", run)
	}
}
