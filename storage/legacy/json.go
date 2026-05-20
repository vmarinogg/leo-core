package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/momhq/mom/storage/memory"
)

// JSONAdapter implements the Adapter interface using flat JSON files
// in .mom/memory/. The index is built on-the-fly by scanning the directory;
// there is no persistent index.json file.
type JSONAdapter struct {
	docsDir string
}

// NewJSONAdapter creates a JSONAdapter for the given .mom/ directory.
func NewJSONAdapter(momDir string) *JSONAdapter {
	return &JSONAdapter{
		docsDir: filepath.Join(momDir, "memory"),
	}
}

func (a *JSONAdapter) Read(id string) (*Doc, error) {
	path := filepath.Join(a.docsDir, id+".json")
	kbDoc, err := memory.LoadDoc(path)
	if err != nil {
		return nil, fmt.Errorf("reading doc %q: %w", id, err)
	}
	return kbDocToStorage(kbDoc), nil
}

func (a *JSONAdapter) Write(doc *Doc) error {
	kbDoc := storageDocToKB(doc)
	if err := kbDoc.Validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	path := filepath.Join(a.docsDir, doc.ID+".json")
	if err := os.MkdirAll(a.docsDir, 0755); err != nil {
		return fmt.Errorf("creating docs dir: %w", err)
	}

	return memory.SaveDoc(path, kbDoc)
}

func (a *JSONAdapter) Query(filter QueryFilter) ([]*Doc, error) {
	idx, err := a.List()
	if err != nil {
		return nil, err
	}

	// Collect matching IDs from the index.
	candidates := a.filterIDs(idx, filter)

	var docs []*Doc
	for _, id := range candidates {
		doc, err := a.Read(id)
		if err != nil {
			continue
		}
		docs = append(docs, doc)
	}

	return docs, nil
}

func (a *JSONAdapter) Delete(id string) error {
	path := filepath.Join(a.docsDir, id+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("deleting doc %q: %w", id, err)
	}

	return nil
}

// List scans .mom/memory/ and builds an Index on-the-fly.
// No persistent index.json is required.
func (a *JSONAdapter) List() (*Index, error) {
	entries, err := os.ReadDir(a.docsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &Index{Version: "1"}, nil
		}
		return nil, fmt.Errorf("reading docs dir: %w", err)
	}

	idx := &Index{
		Version: "1",
		ByTag:   make(map[string][]string),
		ByScope: make(map[string][]string),
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(a.docsDir, e.Name())
		doc, err := memory.LoadDoc(path)
		if err != nil {
			continue
		}

		id := doc.ID

		for _, tag := range doc.Tags {
			idx.ByTag[tag] = appendUnique(idx.ByTag[tag], id)
		}
		idx.ByScope[doc.Scope] = appendUnique(idx.ByScope[doc.Scope], id)
	}

	// Sort all slices for deterministic output.
	sortMapValues(idx.ByTag)
	sortMapValues(idx.ByScope)

	return idx, nil
}

func (a *JSONAdapter) BulkWrite(docs []*Doc) error {
	for _, doc := range docs {
		kbDoc := storageDocToKB(doc)
		if err := kbDoc.Validate(); err != nil {
			return fmt.Errorf("validation failed for %q: %w", doc.ID, err)
		}

		path := filepath.Join(a.docsDir, doc.ID+".json")
		if err := os.MkdirAll(a.docsDir, 0755); err != nil {
			return fmt.Errorf("creating docs dir: %w", err)
		}

		if err := memory.SaveDoc(path, kbDoc); err != nil {
			return err
		}
	}

	return nil
}

func (a *JSONAdapter) filterIDs(idx *Index, filter QueryFilter) []string {
	seen := make(map[string]bool)
	var result []string

	addAll := func(ids []string) {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				result = append(result, id)
			}
		}
	}

	hasFilter := false

	if filter.Scope != "" {
		hasFilter = true
		if ids, ok := idx.ByScope[filter.Scope]; ok {
			addAll(ids)
		}
	}
	for _, tag := range filter.Tags {
		hasFilter = true
		if ids, ok := idx.ByTag[tag]; ok {
			if len(result) > 0 {
				result = intersect(result, ids)
			} else {
				addAll(ids)
			}
		}
	}

	if !hasFilter {
		// No filter — return all doc IDs from all scopes.
		for _, ids := range idx.ByScope {
			addAll(ids)
		}
	}

	sort.Strings(result)
	return result
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

func sortMapValues(m map[string][]string) {
	for _, v := range m {
		sort.Strings(v)
	}
}

func intersect(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, s := range b {
		set[s] = true
	}
	var result []string
	for _, s := range a {
		if set[s] {
			result = append(result, s)
		}
	}
	return result
}

// Conversion helpers between storage.Doc and memory.Doc.
func kbDocToStorage(d *memory.Doc) *Doc {
	return &Doc{
		ID:              d.ID,
		Boot:            d.Boot,
		Scope:           d.Scope,
		Tags:            d.Tags,
		Created:         d.Created,
		CreatedBy:       d.CreatedBy,
		SessionID:       d.SessionID,
		PromotionState:  d.PromotionState,
		Classification:  d.Classification,
		Compartments:    d.Compartments,
		Provenance:      d.Provenance,
		Landmark:        d.Landmark,
		CentralityScore: d.CentralityScore,
		Content:         d.Content,
	}
}

func storageDocToKB(d *Doc) *memory.Doc {
	return &memory.Doc{
		ID:              d.ID,
		Boot:            d.Boot,
		Scope:           d.Scope,
		Tags:            d.Tags,
		Created:         d.Created,
		CreatedBy:       d.CreatedBy,
		SessionID:       d.SessionID,
		PromotionState:  d.PromotionState,
		Classification:  d.Classification,
		Compartments:    d.Compartments,
		Provenance:      d.Provenance,
		Landmark:        d.Landmark,
		CentralityScore: d.CentralityScore,
		Content:         d.Content,
	}
}
