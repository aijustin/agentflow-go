package contextwindow_test

import (
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
)

func TestMaskObservationsAndCompactContext(t *testing.T) {
	messages := []contextwindow.Message{
		{Role: contextwindow.RoleUser, Content: "u1"},
		{Role: contextwindow.RoleAssistant, Content: "a1"},
		{Role: contextwindow.RoleTool, Content: "old tool output"},
		{Role: contextwindow.RoleUser, Content: "u2"},
		{Role: contextwindow.RoleAssistant, Content: "a2"},
		{Role: contextwindow.RoleTool, Content: "recent tool output"},
	}

	masked := contextwindow.MaskObservations(messages, 1)
	if masked[2].Content == "old tool output" {
		t.Fatalf("expected old tool result masked: %q", masked[2].Content)
	}
	if masked[5].Content != "recent tool output" {
		t.Fatalf("expected recent tool result preserved: %q", masked[5].Content)
	}

	compacted := contextwindow.CompactContext(masked)
	if len(compacted) != 5 {
		t.Fatalf("expected masked tool dropped, got %d messages", len(compacted))
	}
}

func TestManagerAppliesObservationMaskBeforeSummarization(t *testing.T) {
	messages := []contextwindow.Message{
		{Role: contextwindow.RoleSystem, Content: "sys"},
		{Role: contextwindow.RoleUser, Content: "u1"},
		{Role: contextwindow.RoleAssistant, Content: "a1"},
		{Role: contextwindow.RoleTool, Content: "old tool output that should be masked"},
		{Role: contextwindow.RoleUser, Content: "u2"},
		{Role: contextwindow.RoleAssistant, Content: "a2"},
		{Role: contextwindow.RoleTool, Content: "recent"},
	}
	result := contextwindow.New(contextwindow.Policy{
		Strategy:                  contextwindow.StrategyNone,
		MaxInputTokens:            10000,
		ObservationMaskAfterTurns: 1,
	}).Prepare(messages)
	if result.Messages[3].Content == "old tool output that should be masked" {
		t.Fatalf("manager did not mask old tool output: %q", result.Messages[3].Content)
	}
}

// Prod session e7441293 shape: successful recharge is followed by loop_guard
// denials and an intervening list_cinemas success. With afterTurns=2 the old
// turn-distance mask hid recharge behind "[masked tool result: N bytes]" while
// keeping the denial visible — the model then asked the user for dates.
func TestMaskObservationsKeepsSuccessAcrossDenialTurns(t *testing.T) {
	rechargeBody := `{"tool":"recharge_order_report","output":{"total":34067.16,"rows":[]}}`
	messages := []contextwindow.Message{
		{Role: contextwindow.RoleUser, Content: "上周末 三溪店的会员收入数据"},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"r1"}},
		{Role: contextwindow.RoleTool, Name: "recharge_order_report", ToolCallID: "r1", Content: rechargeBody},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"t1"}},
		{Role: contextwindow.RoleTool, Name: "current_time", ToolCallID: "t1", Content: "tool invocation blocked by governance: run_tool_loop_guard: tool=current_time same_input_repeated=3 limit=3"},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"c1"}},
		{Role: contextwindow.RoleTool, Name: "list_cinemas", ToolCallID: "c1", Content: `{"tool":"list_cinemas","output":{"cinemas":[{"code":"44002861"}]}}`},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"t2"}},
		{Role: contextwindow.RoleTool, Name: "current_time", ToolCallID: "t2", Content: "tool invocation blocked by governance: run_tool_loop_guard: tool=current_time same_input_repeated=4 limit=3"},
	}

	masked := contextwindow.MaskObservations(messages, 2)
	if got := masked[2].Content; got != rechargeBody {
		t.Fatalf("recharge success must stay unmasked, got %q", got)
	}
	if got := masked[6].Content; !strings.Contains(got, "44002861") {
		t.Fatalf("list_cinemas success must stay visible, got %q", got)
	}
	if contextwindow.IsMaskedObservation(masked[2]) {
		t.Fatal("recharge must not be a masked placeholder")
	}
}

func TestMaskObservationsKeepsLatestSuccessPerToolName(t *testing.T) {
	oldSuccess := `{"tool":"recharge_order_report","output":{"page":1}}`
	newSuccess := `{"tool":"recharge_order_report","output":{"page":2,"total":99}}`
	messages := []contextwindow.Message{
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"r0"}},
		{Role: contextwindow.RoleTool, Name: "recharge_order_report", ToolCallID: "r0", Content: oldSuccess},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"a1"}},
		{Role: contextwindow.RoleTool, Name: "list_cinemas", ToolCallID: "a1", Content: `{"ok":true,"n":1}`},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"a2"}},
		{Role: contextwindow.RoleTool, Name: "list_cinemas", ToolCallID: "a2", Content: `{"ok":true,"n":2}`},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"r1"}},
		{Role: contextwindow.RoleTool, Name: "recharge_order_report", ToolCallID: "r1", Content: newSuccess},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"a3"}},
		{Role: contextwindow.RoleTool, Name: "list_cinemas", ToolCallID: "a3", Content: `{"ok":true,"n":3}`},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"a4"}},
		{Role: contextwindow.RoleTool, Name: "list_cinemas", ToolCallID: "a4", Content: `{"ok":true,"n":4}`},
	}
	// afterTurns=2 masks older success-bearing turns; latest recharge must remain.
	masked := contextwindow.MaskObservations(messages, 2)
	if !contextwindow.IsMaskedObservation(masked[1]) {
		t.Fatalf("superseded old recharge should be masked, got %q", masked[1].Content)
	}
	if masked[7].Content != newSuccess {
		t.Fatalf("latest recharge success must stay unmasked, got %q", masked[7].Content)
	}
}

func TestMaskObservationsNeverMasksPinnedToolNames(t *testing.T) {
	oldHITL := `{"title":"核心信息","values":{"Item_strItemDescription":"张亮专用01"}}`
	newHITL := `{"title":"确认方案","values":{"vendorCode":"01000013"}}`
	messages := []contextwindow.Message{
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"rui-1"}},
		{Role: contextwindow.RoleTool, Name: "request_user_interaction", ToolCallID: "rui-1", Content: oldHITL},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"v1"}},
		{Role: contextwindow.RoleTool, Name: "ho_vendor_list", ToolCallID: "v1", Content: `{"ok":true}`},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"rui-2"}},
		{Role: contextwindow.RoleTool, Name: "request_user_interaction", ToolCallID: "rui-2", Content: newHITL},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"c1"}},
		{Role: contextwindow.RoleTool, Name: "ho_item_class_list", ToolCallID: "c1", Content: `{"ok":true}`},
		{Role: contextwindow.RoleAssistant, ToolCallIDs: []string{"c2"}},
		{Role: contextwindow.RoleTool, Name: "ho_tax_list", ToolCallID: "c2", Content: `{"ok":true}`},
	}
	masked := contextwindow.MaskObservations(messages, 2, "request_user_interaction")
	if masked[1].Content != oldHITL {
		t.Fatalf("pinned HITL result must stay unmasked even when superseded, got %q", masked[1].Content)
	}
	if masked[5].Content != newHITL {
		t.Fatalf("latest HITL result must stay unmasked, got %q", masked[5].Content)
	}
}

func TestClassifyToolResultDetectsLoopGuard(t *testing.T) {
	msg := contextwindow.Message{
		Role:    contextwindow.RoleTool,
		Name:    "current_time",
		Content: "tool invocation blocked by governance: run_tool_loop_guard: tool=current_time same_input_repeated=3 limit=3",
	}
	if got := contextwindow.ClassifyToolResult(msg); got != contextwindow.ToolResultClassDenied {
		t.Fatalf("expected denied, got %q", got)
	}
}
