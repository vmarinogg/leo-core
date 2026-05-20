package mcp

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/momhq/mom/storage/canonical"
)

// resourceDef describes one MCP resource for resources/list.
type resourceDef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MIMEType    string `json:"mimeType"`
}

// resourceContent is a single content item in a resources/read response.
type resourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType"`
	Text     string `json:"text"`
}

// allResources returns the static resource catalogue.
func allResources() []resourceDef {
	return []resourceDef{
		{
			URI:         "mom://vault",
			Name:        "MOM Central Vault",
			Description: "Central vault status for MOM's single-vault architecture.",
			MIMEType:    "application/json",
		},
	}
}

// handleResourcesList returns the static resource catalogue.
func (s *Server) handleResourcesList() (any, *rpcError) {
	resources := allResources()
	out := make([]any, len(resources))
	for i, r := range resources {
		out[i] = r
	}
	return map[string]any{"resources": out}, nil
}

// handleResourcesRead reads the requested resource and returns its content.
func (s *Server) handleResourcesRead(params json.RawMessage) (any, *rpcError) {
	var req struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "invalid resources/read params: " + err.Error()}
	}

	switch req.URI {
	case "mom://vault":
		return s.readVault()
	default:
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "unknown resource URI: " + req.URI}
	}
}

// readVault returns central-vault status without scope hierarchy semantics.
func (s *Server) readVault() (any, *rpcError) {
	path, err := canonical.Path()
	if err != nil {
		return nil, &rpcError{Code: errCodeInternalError, Message: fmt.Sprintf("resolving central vault path: %v", err)}
	}
	cwd, _ := os.Getwd()

	status := map[string]any{
		"architecture":    "central-vault",
		"vault_path":      path,
		"project_mom_dir": s.momDir,
		"cwd":             cwd,
	}
	if s.openErr != nil {
		status["status"] = "unavailable"
		status["error"] = s.openErr.Error()
	} else {
		status["status"] = "ok"
	}

	text, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return nil, &rpcError{Code: errCodeInternalError, Message: fmt.Sprintf("marshaling vault status: %v", err)}
	}
	return map[string]any{
		"contents": []resourceContent{
			{URI: "mom://vault", MIMEType: "application/json", Text: string(text)},
		},
	}, nil
}
