package watcher

import (
	"testing"
)

const windsurfUserInputFixture = `{"type":"user_input","user_input":{"user_response":"deploy postgres canary"}}`

const windsurfPlannerResponseFixture = `{"type":"planner_response","planner_response":{"response":"Sure, deploying now."}}`

const windsurfMcpTool = `{"type":"mcp_tool","tool":"Bash"}`

const windsurfCodeAction = `{"type":"code_action","action":"edit"}`

func TestWindsurfAdapter_ExtractTurn_UserInput(t *testing.T) {
	a := NewWindsurfAdapter()
	turn, ok := a.ExtractTurn([]byte(windsurfUserInputFixture), "s-ws")
	if !ok {
		t.Fatal("expected ok=true for user_input")
	}
	if turn.SessionID != "s-ws" {
		t.Errorf("SessionID = %q, want s-ws", turn.SessionID)
	}
	if turn.Role != "user" {
		t.Errorf("Role = %q, want user", turn.Role)
	}
	if turn.Text != "deploy postgres canary" {
		t.Errorf("Text = %q", turn.Text)
	}
	if turn.Harness != "windsurf" {
		t.Errorf("Harness = %q, want windsurf", turn.Harness)
	}
}

func TestWindsurfAdapter_ExtractTurn_PlannerResponse(t *testing.T) {
	a := NewWindsurfAdapter()
	turn, ok := a.ExtractTurn([]byte(windsurfPlannerResponseFixture), "s-ws")
	if !ok {
		t.Fatal("expected ok=true for planner_response")
	}
	if turn.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", turn.Role)
	}
	if turn.Text != "Sure, deploying now." {
		t.Errorf("Text = %q", turn.Text)
	}
}

func TestWindsurfAdapter_ExtractTurn_DropsMcpTool(t *testing.T) {
	a := NewWindsurfAdapter()
	if _, ok := a.ExtractTurn([]byte(windsurfMcpTool), "s-ws"); ok {
		t.Error("expected ok=false for mcp_tool line")
	}
}

func TestWindsurfAdapter_ExtractTurn_DropsCodeAction(t *testing.T) {
	a := NewWindsurfAdapter()
	if _, ok := a.ExtractTurn([]byte(windsurfCodeAction), "s-ws"); ok {
		t.Error("expected ok=false for code_action line")
	}
}

func TestWindsurfAdapter_ExtractTurn_RejectsMalformedJSON(t *testing.T) {
	a := NewWindsurfAdapter()
	if _, ok := a.ExtractTurn([]byte(`not-json`), "s-ws"); ok {
		t.Error("expected ok=false for malformed JSON")
	}
}
