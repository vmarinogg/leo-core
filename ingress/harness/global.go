package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	momBlockStart = "<!-- BEGIN MOM GENERATED BLOCK -->"
	momBlockEnd   = "<!-- END MOM GENERATED BLOCK -->"
)

// GlobalAdapter is implemented by harnesses that can install MOM for all
// projects through user-level configuration.
type GlobalAdapter interface {
	Adapter
	GenerateGlobalContextFile(config Config, constraints []Constraint, skills []Skill, identity *Identity) error
	RegisterGlobalMCP() error
}

// GlobalHookInstaller is implemented by harnesses with user-level hooks.
type GlobalHookInstaller interface {
	RegisterGlobalHooks() error
}

// GlobalExtensionInstaller is implemented by harnesses with user-level extensions.
type GlobalExtensionInstaller interface {
	RegisterGlobalExtension() error
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func homePath(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	items := append([]string{home}, parts...)
	return filepath.Join(items...), nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func buildGlobalContext(watermark string, config Config, constraints []Constraint, skills []Skill, identity *Identity) string {
	var body string
	if config.Delivery == "context-file" {
		body = BuildContextContent(config, constraints, skills, identity)
	} else {
		body = BuildMinimalContextContent()
	}
	return strings.TrimSpace(watermark+"\n\n"+body) + "\n"
}

func upsertManagedBlock(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	block := momBlockStart + "\n" + strings.TrimSpace(body) + "\n" + momBlockEnd + "\n"
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	content := string(existing)
	start := strings.Index(content, momBlockStart)
	end := strings.Index(content, momBlockEnd)
	if start >= 0 && end >= start {
		end += len(momBlockEnd)
		updated := strings.TrimRight(content[:start], "\n")
		if updated != "" {
			updated += "\n\n"
		}
		updated += strings.TrimRight(block, "\n")
		tail := strings.TrimLeft(content[end:], "\n")
		if tail != "" {
			updated += "\n\n" + tail
		} else {
			updated += "\n"
		}
		return os.WriteFile(path, []byte(updated), 0o644)
	}
	if strings.TrimSpace(content) == "" {
		return os.WriteFile(path, []byte(block), 0o644)
	}
	updated := strings.TrimRight(content, "\n") + "\n\n" + block
	return os.WriteFile(path, []byte(updated), 0o644)
}

// RemoveManagedBlock strips the MOM-generated block from a global context
// file (e.g. ~/.claude/CLAUDE.md), preserving any surrounding user content.
// If the block is absent, the file is left untouched. If no other content
// remains after removal, the file is deleted.
func RemoveManagedBlock(path string) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	content := string(existing)
	start := strings.Index(content, momBlockStart)
	end := strings.Index(content, momBlockEnd)
	if start < 0 || end < 0 || end < start {
		return nil
	}
	end += len(momBlockEnd)
	head := strings.TrimRight(content[:start], "\n")
	tail := strings.TrimLeft(content[end:], "\n")
	var updated string
	switch {
	case head == "" && tail == "":
		return os.Remove(path)
	case head == "":
		updated = tail
	case tail == "":
		updated = head + "\n"
	default:
		updated = head + "\n\n" + tail
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

func upsertClaudeUserMCP() error {
	path, err := homePath(".claude.json")
	if err != nil {
		return err
	}
	root := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
		}
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}
	servers["mom"] = map[string]any{
		"type":    "stdio",
		"command": "mom",
		"args":    []string{"serve", "mcp"},
		"env":     map[string]string{},
	}
	root["mcpServers"] = servers
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
