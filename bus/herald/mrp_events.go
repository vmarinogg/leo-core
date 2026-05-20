package herald

// MRP v0 event structs — wire protocol between a harness adapter and MOM.
// Field names match JSON schema definitions in .github/mrp/schemas/.

// MRPSessionStart is the session.start MRP event.
type MRPSessionStart struct {
	MRPVersion  string `json:"mrp_version"`
	Event       string `json:"event"`
	SessionID   string `json:"session_id"`
	Harness     string `json:"harness"`
	Timestamp   string `json:"timestamp"`
	StartedAt   string `json:"started_at"`
	ProjectRoot string `json:"project_root,omitempty"`
	UserID      string `json:"user_id,omitempty"`
}

// MRPSessionEnd is the session.end MRP event.
type MRPSessionEnd struct {
	MRPVersion string  `json:"mrp_version"`
	Event      string  `json:"event"`
	SessionID  string  `json:"session_id"`
	Harness    string  `json:"harness"`
	Timestamp  string  `json:"timestamp"`
	StartedAt  string  `json:"started_at"`
	EndedAt    string  `json:"ended_at"`
	TurnCount  *int    `json:"turn_count,omitempty"`
	ExitReason *string `json:"exit_reason,omitempty"`
}

// MRPTurnComplete is the turn.complete MRP event (opt-in).
type MRPTurnComplete struct {
	MRPVersion       string `json:"mrp_version"`
	Event            string `json:"event"`
	SessionID        string `json:"session_id"`
	Harness          string `json:"harness"`
	Timestamp        string `json:"timestamp"`
	TurnIndex        int    `json:"turn_index"`
	PromptTokens     *int   `json:"prompt_tokens,omitempty"`
	CompletionTokens *int   `json:"completion_tokens,omitempty"`
}

// MRPCompactTriggered is the compact.triggered MRP event.
type MRPCompactTriggered struct {
	MRPVersion    string  `json:"mrp_version"`
	Event         string  `json:"event"`
	SessionID     string  `json:"session_id"`
	Harness       string  `json:"harness"`
	Timestamp     string  `json:"timestamp"`
	TriggerSource *string `json:"trigger_source,omitempty"`
}

// MRPClearTriggered is the clear.triggered MRP event.
type MRPClearTriggered struct {
	MRPVersion    string  `json:"mrp_version"`
	Event         string  `json:"event"`
	SessionID     string  `json:"session_id"`
	Harness       string  `json:"harness"`
	Timestamp     string  `json:"timestamp"`
	TriggerSource *string `json:"trigger_source,omitempty"`
}

// MRPClassifiedDraft is one entry in the classified_drafts array of a capture payload.
type MRPClassifiedDraft struct {
	Type       string   `json:"type"`
	Summary    string   `json:"summary"`
	Tags       []string `json:"tags,omitempty"`
	Confidence string   `json:"confidence"`
}

// MRPCapturePayload is the dual-output payload returned after a capture fires.
type MRPCapturePayload struct {
	RawExhaust       string               `json:"raw_exhaust"`
	ClassifiedDrafts []MRPClassifiedDraft `json:"classified_drafts"`
}

// MRPAdapterCapability is the adapter capability declaration embedded in each adapter binary.
type MRPAdapterCapability struct {
	Adapter      string   `json:"adapter"`
	Version      string   `json:"version"`
	Supports     []string `json:"supports"`
	Experimental []string `json:"experimental"`
}
