package drafter

import (
	"math"
	"strings"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// bm25Index indexes a corpus for BM25 ranking of RAKE tag candidates.
type bm25Index struct {
	docs   [][]string
	df     map[string]int
	avgLen float64
}

func newBM25Index(corpus []string) *bm25Index {
	idx := &bm25Index{df: make(map[string]int)}
	for _, doc := range corpus {
		tokens := tokenizeBM25(doc)
		idx.docs = append(idx.docs, tokens)
		seen := make(map[string]bool)
		for _, t := range tokens {
			if !seen[t] {
				seen[t] = true
				idx.df[t]++
			}
		}
	}
	total := 0
	for _, d := range idx.docs {
		total += len(d)
	}
	if len(idx.docs) > 0 {
		idx.avgLen = float64(total) / float64(len(idx.docs))
	}
	return idx
}

func (idx *bm25Index) score(query string, docTokens []string) float64 {
	queryTokens := tokenizeBM25(query)
	n := float64(len(idx.docs))
	dl := float64(len(docTokens))

	tf := make(map[string]int)
	for _, t := range docTokens {
		tf[t]++
	}

	var s float64
	for _, qt := range queryTokens {
		docFreq := float64(idx.df[qt])
		if docFreq == 0 {
			continue
		}
		idf := math.Log((n - docFreq + 0.5) / (docFreq + 0.5))
		termFreq := float64(tf[qt])
		tfNorm := (termFreq * (bm25K1 + 1)) / (termFreq + bm25K1*(1-bm25B+bm25B*dl/idx.avgLen))
		s += idf * tfNorm
	}
	return s
}

// tokenizeBM25 lowercases, strips punctuation, and removes stopwords.
func tokenizeBM25(s string) []string {
	s = strings.ToLower(s)
	var tokens []string
	for _, word := range strings.Fields(s) {
		clean := strings.Trim(word, ".,;:!?()[]{}\"'`")
		if clean != "" && !bm25Stopwords[clean] {
			tokens = append(tokens, clean)
		}
	}
	return tokens
}

// rankCandidates ranks RAKE candidates against the indexed vocabulary.
func (idx *bm25Index) rankCandidates(candidates []RakeCandidate) []string {
	type scored struct {
		tag   string
		score float64
	}
	var results []scored
	for _, c := range candidates {
		tokens := tokenizeBM25(c.Phrase)
		s := 0.0
		for _, doc := range idx.docs {
			s = math.Max(s, idx.score(c.Phrase, doc))
		}
		results = append(results, scored{tag: strings.Join(tokens, "-"), score: s + c.Score})
	}
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	var tags []string
	for _, r := range results {
		tags = append(tags, r.tag)
	}
	return tags
}

var bm25Stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "being": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "will": true, "would": true, "could": true,
	"should": true, "may": true, "might": true, "shall": true, "can": true,
	"this": true, "that": true, "these": true, "those": true,
	"i": true, "you": true, "he": true, "she": true, "it": true, "we": true, "they": true,
	"me": true, "him": true, "her": true, "us": true, "them": true,
	"my": true, "your": true, "his": true, "its": true, "our": true, "their": true,
	"what": true, "which": true, "who": true, "whom": true, "where": true, "when": true,
	"how": true, "why": true, "if": true, "then": true, "else": true,
	"and": true, "or": true, "but": true, "not": true, "so": true, "yet": true,
	"for": true, "with": true, "from": true, "to": true, "in": true, "on": true,
	"at": true, "by": true, "of": true, "about": true, "into": true, "through": true,
	"as": true, "up": true, "out": true, "off": true, "over": true, "under": true,
	"also": true, "just": true, "very": true, "much": true, "more": true, "most": true,
	"some": true, "any": true, "no": true, "all": true, "each": true, "every": true,
	"both": true, "few": true, "many": true, "such": true, "own": true, "same": true,
	"other": true, "than": true, "too": true, "only": true, "here": true, "there": true,
	"now": true, "while": true, "after": true,
	"before": true, "during": true, "since": true, "until": true,
}
