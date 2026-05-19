package herald

// SessionEvent records an agent session lifecycle boundary.
type SessionEvent struct {
	Kind          string  `json:"kind"`
	SessionID     string  `json:"session_id"`
	OrgID         string  `json:"org_id,omitempty"`
	RepoID        string  `json:"repo_id"`
	Harness       string  `json:"harness"`
	StartedAt     string  `json:"started_at"`
	EndedAt       *string `json:"ended_at"`
	Trigger       string  `json:"trigger"`
	TurnCount     int     `json:"turn_count"`
	ToolCallCount int     `json:"tool_call_count"`
}

// CaptureEvent records a memory-capture extraction run.
type CaptureEvent struct {
	Kind             string   `json:"kind"`
	CaptureID        string   `json:"capture_id"`
	SessionID        string   `json:"session_id"`
	TS               string   `json:"ts"`
	ExtractorModel   string   `json:"extractor_model"`
	ExtractorVersion string   `json:"extractor_version"`
	MemoriesProposed int      `json:"memories_proposed"`
	MemoriesAccepted int      `json:"memories_accepted"`
	Tags             []string `json:"tags"`
	Summary          string   `json:"summary"`
}

// MemoryMutation records a create/update/dedup/deprecate/reconcile on a memory doc.
type MemoryMutation struct {
	Kind           string  `json:"kind"`
	MemoryID       string  `json:"memory_id"`
	Op             string  `json:"op"`
	TS             string  `json:"ts"`
	PrevHash       *string `json:"prev_hash"`
	NewHash        string  `json:"new_hash"`
	PromotionState string  `json:"promotion_state"`
	By             string  `json:"by"`
}

// ConsumptionEvent records a memory doc being read by an agent.
type ConsumptionEvent struct {
	Kind      string  `json:"kind"`
	MemoryID  string  `json:"memory_id"`
	SessionID *string `json:"session_id"`
	TS        string  `json:"ts"`
	ByAgent   string  `json:"by_agent"`
	Context   string  `json:"context"`
}

// HarnessHealth records health metrics for a harness at wrap-up time.
type HarnessHealth struct {
	Kind          string  `json:"kind"`
	Harness       string  `json:"harness"`
	TS            string  `json:"ts"`
	WrapUpSuccess bool    `json:"wrap_up_success"`
	ErrorType     *string `json:"error_type"`
	LatencyMS     int64   `json:"latency_ms"`
}
