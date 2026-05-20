package drafter

import (
	"strings"
	"sync"

	stopwordsiso "github.com/toadharvard/stopwords-iso"
)

// stopwords is a merged set of stop words from all languages (stopwords-iso).
// Loaded once on first use.
var (
	stopwords     map[string]bool
	stopwordsOnce sync.Once
)

func loadStopwords() {
	stopwords = make(map[string]bool)
	mapping, err := stopwordsiso.NewStopwordsMapping()
	if err != nil {
		// Fallback: minimal English set if lib fails.
		for _, w := range []string{"the", "a", "an", "is", "are", "and", "or", "but", "for", "to", "in", "on", "at", "by", "of"} {
			stopwords[w] = true
		}
		return
	}
	for _, words := range mapping {
		for _, w := range words {
			// Normalize: lowercase, strip accents.
			w = strings.ToLower(strings.TrimSpace(w))
			if w != "" {
				stopwords[w] = true
			}
		}
	}
}

func isStopword(w string) bool {
	stopwordsOnce.Do(loadStopwords)
	return stopwords[w]
}

// maxPhraseWords limits RAKE candidate phrases to avoid overly long tags.
const maxPhraseWords = 4

// RakeCandidate is a keyword candidate with a score.
type RakeCandidate struct {
	Phrase string
	Score  float64
}

// RAKE extracts keyword candidates from text using the RAKE algorithm.
// Returns top N candidates sorted by score descending.
func RAKE(text string, topN int) []RakeCandidate {
	// Lowercase
	text = strings.ToLower(text)

	// Split at stopwords and punctuation to get candidate phrases
	words := tokenize(text)
	phrases := splitAtStopwords(words)

	// Calculate word frequency and degree
	wordFreq := make(map[string]int)
	wordDeg := make(map[string]int)
	for _, phrase := range phrases {
		for _, word := range phrase {
			wordFreq[word]++
			wordDeg[word] += len(phrase)
		}
	}

	// Score each phrase
	scored := make(map[string]float64)
	for _, phrase := range phrases {
		key := strings.Join(phrase, " ")
		if key == "" {
			continue
		}
		var score float64
		for _, word := range phrase {
			if wordFreq[word] > 0 {
				score += float64(wordDeg[word]) / float64(wordFreq[word])
			}
		}
		scored[key] = score
	}

	// Sort by score
	var candidates []RakeCandidate
	for phrase, score := range scored {
		candidates = append(candidates, RakeCandidate{Phrase: phrase, Score: score})
	}
	sortCandidates(candidates)

	if topN > 0 && len(candidates) > topN {
		candidates = candidates[:topN]
	}
	return candidates
}
