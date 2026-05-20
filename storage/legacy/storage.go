// Package storage defines the StorageAdapter interface for memory persistence.
package storage

import (
	"time"

	"github.com/momhq/mom/storage/memory"
)

// Doc represents a memory document in the storage layer.
type Doc struct {
	ID              string              `json:"id"`
	Boot            bool                `json:"boot,omitempty"`
	Scope           string              `json:"scope"`
	Tags            []string            `json:"tags"`
	Created         time.Time           `json:"created"`
	CreatedBy       string              `json:"created_by"`
	SessionID       string              `json:"session_id,omitempty"`
	PromotionState  string              `json:"promotion_state,omitempty"`
	Classification  string              `json:"classification,omitempty"`
	Compartments    map[string][]string `json:"compartments,omitempty"`
	Provenance      *memory.Provenance  `json:"provenance,omitempty"`
	Landmark        bool                `json:"landmark,omitempty"`
	CentralityScore *float64            `json:"centrality_score,omitempty"`
	Content         map[string]any      `json:"content"`
}

// Index represents the memory index.
type Index struct {
	Version     string              `json:"version"`
	LastRebuilt string              `json:"last_rebuilt"`
	ByTag       map[string][]string `json:"by_tag"`
	ByScope     map[string][]string `json:"by_scope"`
}

// QueryFilter defines criteria for querying memory documents.
type QueryFilter struct {
	Tags  []string
	Scope string
}

// Adapter is the interface that storage backends must implement.
// The JSON adapter (free tier) reads/writes flat JSON files in .mom/memory/.
// Future adapters (MongoDB, etc.) implement the same interface.
type Adapter interface {
	Read(id string) (*Doc, error)
	Write(doc *Doc) error
	Query(filter QueryFilter) ([]*Doc, error)
	Delete(id string) error
	List() (*Index, error)
	BulkWrite(docs []*Doc) error
}
