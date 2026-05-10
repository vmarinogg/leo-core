// Package gardener provides landmark computation for the memory graph.
// Landmarks are high-centrality memory documents that sit at structural
// crossroads — connected to many others via shared tags.
package gardener

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// MinDocsForLandmarks is the minimum number of memory docs required before
// landmark computation is meaningful. Corpora smaller than this threshold
// are skipped — the graph is too sparse to produce reliable centrality scores.
const MinDocsForLandmarks = 100

// docEntry is a minimal in-memory representation used during computation.
type docEntry struct {
	id   string
	tags []string
	path string
	raw  map[string]any
}

// ComputeLandmarks loads all memory docs from memDir, builds a tag co-occurrence
// graph, computes degree centrality (number of unique neighbours), and marks the
// top thresholdPct% as landmarks. Returns the number of doc files updated.
//
// Degree is the count of distinct other docs that share at least one tag with a
// given doc. Docs with the most connections are structural hubs — high blast
// radius. Scores are normalised to [0, 1].
//
// If len(docs) < MinDocsForLandmarks, computation is skipped and (0, nil) is
// returned.
func ComputeLandmarks(memDir string, thresholdPct float64) (int, error) {
	entries, err := os.ReadDir(memDir)
	if err != nil {
		return 0, fmt.Errorf("reading memory dir: %w", err)
	}

	// Load all JSON docs.
	var docs []docEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(memDir, e.Name())
		raw, err := loadRaw(path)
		if err != nil {
			// Skip unreadable/unparseable files.
			continue
		}
		id, _ := raw["id"].(string)
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".json")
		}
		tags := extractTags(raw)
		docs = append(docs, docEntry{id: id, tags: tags, path: path, raw: raw})
	}

	if len(docs) < MinDocsForLandmarks {
		return 0, nil
	}

	// Build tag → doc-index mapping to know how many docs share each tag.
	tagDocIndices := make(map[string][]int, 64)
	for i, d := range docs {
		for _, tag := range d.tags {
			tagDocIndices[tag] = append(tagDocIndices[tag], i)
		}
	}

	// Compute degree centrality: for each doc, count unique neighbours.
	// Two docs are neighbours if they share at least one tag.
	neighbours := make([]map[int]struct{}, len(docs))
	for i := range neighbours {
		neighbours[i] = make(map[int]struct{})
	}
	for _, indices := range tagDocIndices {
		if len(indices) < 2 {
			continue
		}
		for x := 0; x < len(indices); x++ {
			for y := x + 1; y < len(indices); y++ {
				neighbours[indices[x]][indices[y]] = struct{}{}
				neighbours[indices[y]][indices[x]] = struct{}{}
			}
		}
	}

	degree := make([]float64, len(docs))
	for i := range docs {
		degree[i] = float64(len(neighbours[i]))
	}

	// Normalise degree to [0, 1].
	maxDegree := 0.0
	for _, d := range degree {
		if d > maxDegree {
			maxDegree = d
		}
	}
	if maxDegree > 0 {
		for i := range degree {
			degree[i] /= maxDegree
		}
	}

	// Determine landmark count: top N%.
	cutoffCount := int(math.Ceil(float64(len(docs)) * thresholdPct / 100.0))
	if cutoffCount < 1 {
		cutoffCount = 0
	}

	// Greedy landmark selection: degree + diversity penalty.
	// Each round picks the candidate with the highest effective score.
	// After picking a landmark, candidates sharing tags with it get penalised
	// so that subsequent picks spread across different topic clusters.
	landmarkSet := selectLandmarksDiverse(docs, degree, tagDocIndices, cutoffCount)

	// Compute final scores: landmarks get their degree; non-landmarks keep degree
	// for centrality_score (useful for graph sizing).
	scores := degree

	// Write updated landmark and centrality_score fields back to each doc file.
	updated := 0
	for i, d := range docs {
		score := scores[i]
		isLandmark := landmarkSet[i]

		changed := false

		// Check existing values.
		existingLandmark, _ := d.raw["landmark"].(bool)
		if existingLandmark != isLandmark {
			changed = true
		}

		var existingScore *float64
		if v, ok := d.raw["centrality_score"].(float64); ok {
			existingScore = &v
		}
		scoreChanged := existingScore == nil || math.Abs(*existingScore-score) > 1e-9
		if scoreChanged {
			changed = true
		}

		// Always write to ensure both fields are in sync.
		d.raw["landmark"] = isLandmark
		if score == 0 {
			delete(d.raw, "centrality_score")
		} else {
			d.raw["centrality_score"] = score
		}

		if err := saveRaw(d.path, d.raw); err != nil {
			// Non-fatal: report but continue.
			continue
		}

		if changed {
			updated++
		}
	}

	return updated, nil
}

// selectLandmarksDiverse picks landmarks using greedy degree + diversity.
// Each iteration selects the candidate with the highest effective score, then
// penalises all candidates that share tags with the selected landmark. This
// ensures landmarks spread across different topic clusters rather than
// concentrating in the densest one.
//
// The diversity penalty for a candidate is: 0.5 * (shared_tags / total_tags)
// where shared_tags is the count of tags this candidate shares with any
// already-selected landmark, and total_tags is the candidate's tag count.
func selectLandmarksDiverse(docs []docEntry, degree []float64, tagDocIndices map[string][]int, k int) map[int]bool {
	n := len(docs)
	if k <= 0 || n == 0 {
		return make(map[int]bool)
	}

	selected := make(map[int]bool, k)
	// Track which tags are already "covered" by selected landmarks.
	coveredTags := make(map[string]bool)

	// Penalty per doc — starts at 0, increases as landmarks are selected.
	penalty := make([]float64, n)

	for round := 0; round < k && round < n; round++ {
		bestIdx := -1
		bestScore := -1.0

		for i := 0; i < n; i++ {
			if selected[i] || degree[i] == 0 {
				continue
			}
			effective := degree[i] - penalty[i]
			if effective < 0 {
				effective = 0
			}
			if effective > bestScore {
				bestScore = effective
				bestIdx = i
			}
		}

		if bestIdx < 0 || bestScore <= 0 {
			break
		}

		selected[bestIdx] = true

		// Penalise candidates sharing tags with the newly selected landmark.
		for _, tag := range docs[bestIdx].tags {
			if coveredTags[tag] {
				continue // already penalised for this tag
			}
			coveredTags[tag] = true
			// Apply penalty to all docs sharing this tag.
			for _, idx := range tagDocIndices[tag] {
				if selected[idx] {
					continue
				}
				tagCount := len(docs[idx].tags)
				if tagCount > 0 {
					penalty[idx] += 0.5 / float64(tagCount)
				}
			}
		}
	}

	return selected
}

// TagGraph represents tag co-occurrence data for incremental updates.
// TODO(v0.11): evaluate renaming to TagIndex or TaggerGraph once Herald/Tagger
// component boundaries are stable.
type TagGraph struct {
	// Tags maps each tag to the list of doc IDs that carry it.
	Tags map[string][]string `json:"tags"`
	// DocCount is the total number of docs included in this graph snapshot.
	DocCount int `json:"doc_count"`
}

// WriteTagGraph builds a tag co-occurrence graph from memDir and writes it to
// cacheDir/tag-graph.json. Creates cacheDir if it does not exist.
func WriteTagGraph(memDir, cacheDir string) error {
	entries, err := os.ReadDir(memDir)
	if err != nil {
		return fmt.Errorf("reading memory dir: %w", err)
	}

	tagDocs := make(map[string][]string)
	docCount := 0

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := loadRaw(filepath.Join(memDir, e.Name()))
		if err != nil {
			continue
		}
		id, _ := raw["id"].(string)
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".json")
		}
		docCount++
		for _, tag := range extractTags(raw) {
			tagDocs[tag] = append(tagDocs[tag], id)
		}
	}

	graph := TagGraph{
		Tags:     tagDocs,
		DocCount: docCount,
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}

	data, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling tag graph: %w", err)
	}
	data = append(data, '\n')

	outPath := filepath.Join(cacheDir, "tag-graph.json")
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return fmt.Errorf("writing tag graph: %w", err)
	}

	return nil
}

// GraphNode represents a document node in the visualization graph.
type GraphNode struct {
	ID        string   `json:"id"`
	Score     float64  `json:"score"`
	Landmark  bool     `json:"landmark"`
	Type      string   `json:"type"`
	Summary   string   `json:"summary"`
	Tags      []string `json:"tags"`
	EdgeCount int      `json:"edge_count"`
}

// GraphEdge represents a weighted edge between two docs.
type GraphEdge struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Weight float64 `json:"weight"`
	Tag    string  `json:"tag"` // the shared tag that created this edge
}

// GraphData holds the full visualization payload.
type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	Stats GraphStats  `json:"stats"`
}

// GraphStats holds summary statistics.
type GraphStats struct {
	TotalDocs     int `json:"total_docs"`
	TotalEdges    int `json:"total_edges"`
	LandmarkCount int `json:"landmark_count"`
	TagCount      int `json:"tag_count"`
}

// BuildGraphData reads memory docs and constructs the visualization graph.
// It includes all docs as nodes and edges for shared tags (limited to tags
// shared by <= maxTagSize docs to avoid overwhelming the graph).
func BuildGraphData(memDir string, maxTagSize int) (*GraphData, error) {
	entries, err := os.ReadDir(memDir)
	if err != nil {
		return nil, fmt.Errorf("reading memory dir: %w", err)
	}

	type docInfo struct {
		id   string
		tags []string
		raw  map[string]any
	}

	var docs []docInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := loadRaw(filepath.Join(memDir, e.Name()))
		if err != nil {
			continue
		}
		id, _ := raw["id"].(string)
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".json")
		}
		tags := extractTags(raw)
		docs = append(docs, docInfo{id: id, tags: tags, raw: raw})
	}

	// Build tag → doc indices
	tagDocIndices := make(map[string][]int)
	for i, d := range docs {
		for _, tag := range d.tags {
			tagDocIndices[tag] = append(tagDocIndices[tag], i)
		}
	}

	// Build nodes
	nodes := make([]GraphNode, len(docs))
	for i, d := range docs {
		landmark, _ := d.raw["landmark"].(bool)
		score := 0.0
		if s, ok := d.raw["centrality_score"].(float64); ok {
			score = s
		}
		docType, _ := d.raw["type"].(string)
		summary, _ := d.raw["summary"].(string)
		if summary == "" {
			if content, ok := d.raw["content"].(map[string]any); ok {
				summary, _ = content["summary"].(string)
			}
		}

		nodes[i] = GraphNode{
			ID:       d.id,
			Score:    score,
			Landmark: landmark,
			Type:     docType,
			Summary:  summary,
			Tags:     d.tags,
		}
	}

	// Build edges — only for tags shared by <= maxTagSize docs
	type edgeKey struct{ a, b int }
	edgeWeights := make(map[edgeKey]float64)
	edgeTags := make(map[edgeKey]string) // store the strongest tag per edge

	for tag, indices := range tagDocIndices {
		if len(indices) < 2 || len(indices) > maxTagSize {
			continue
		}
		w := 1.0 / float64(len(indices))
		for i := 0; i < len(indices); i++ {
			for j := i + 1; j < len(indices); j++ {
				key := edgeKey{indices[i], indices[j]}
				if w > edgeWeights[key] {
					edgeTags[key] = tag
				}
				edgeWeights[key] += w
			}
		}
	}

	// Count edges per node and build edge list.
	edgeCounts := make(map[int]int, len(docs))
	edges := make([]GraphEdge, 0, len(edgeWeights))
	for key, weight := range edgeWeights {
		edgeCounts[key.a]++
		edgeCounts[key.b]++
		edges = append(edges, GraphEdge{
			Source: docs[key.a].id,
			Target: docs[key.b].id,
			Weight: weight,
			Tag:    edgeTags[key],
		})
	}

	// Set edge counts on nodes.
	for i := range nodes {
		nodes[i].EdgeCount = edgeCounts[i]
	}

	landmarkCount := 0
	for _, n := range nodes {
		if n.Landmark {
			landmarkCount++
		}
	}

	return &GraphData{
		Nodes: nodes,
		Edges: edges,
		Stats: GraphStats{
			TotalDocs:     len(docs),
			TotalEdges:    len(edges),
			LandmarkCount: landmarkCount,
			TagCount:      len(tagDocIndices),
		},
	}, nil
}

// loadRaw reads a JSON file and returns its content as a raw map.
func loadRaw(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// saveRaw writes a raw map as formatted JSON.
func saveRaw(path string, raw map[string]any) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling doc: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// extractTags pulls the tags array from a raw doc map.
func extractTags(raw map[string]any) []string {
	v, ok := raw["tags"]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []any:
		tags := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				tags = append(tags, s)
			}
		}
		return tags
	case []string:
		return t
	}
	return nil
}
