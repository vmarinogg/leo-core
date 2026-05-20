package drafter

// Turn represents a single conversation turn with metadata for boundary detection.
type Turn struct {
	Text      string
	FilePaths []string
	Keywords  []string
	ToolNames []string
}

// Chunk represents a contiguous range of turns with shared context.
type Chunk struct {
	StartIdx int
	EndIdx   int
}

// DetectBoundaries detects chunk boundaries based on file path and keyword divergence.
// A new chunk starts when >threshold of file paths OR RAKE keywords change.
func DetectBoundaries(turns []Turn, threshold float64) []Chunk {
	if len(turns) == 0 {
		return nil
	}
	if threshold == 0 {
		threshold = 0.6
	}

	var chunks []Chunk
	current := Chunk{StartIdx: 0}
	currentPaths := make(map[string]bool)
	currentKeywords := make(map[string]bool)

	for i, turn := range turns {
		turnPaths := make(map[string]bool)
		for _, p := range turn.FilePaths {
			turnPaths[p] = true
		}
		turnKeywords := make(map[string]bool)
		for _, k := range turn.Keywords {
			turnKeywords[k] = true
		}

		if i > 0 && (divergence(currentPaths, turnPaths) > threshold || divergence(currentKeywords, turnKeywords) > threshold) {
			current.EndIdx = i
			chunks = append(chunks, current)
			current = Chunk{StartIdx: i}
			currentPaths = turnPaths
			currentKeywords = turnKeywords
		} else {
			for p := range turnPaths {
				currentPaths[p] = true
			}
			for k := range turnKeywords {
				currentKeywords[k] = true
			}
		}
	}

	current.EndIdx = len(turns)
	chunks = append(chunks, current)
	return chunks
}

func divergence(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	total := len(a)
	if total == 0 {
		return 1.0
	}
	overlap := 0
	for k := range a {
		if b[k] {
			overlap++
		}
	}
	return 1.0 - float64(overlap)/float64(total)
}
