package librarian

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SearchFilter narrows a SearchMemories call. All fields are optional;
// the zero value is "all memories ordered by created_at desc".
//
// FTSQuery, when non-empty, is passed verbatim to FTS5 MATCH and rows
// are ranked by bm25 with ADR 0007 column weights (0 on id, 2 on
// summary, 10 on content_text). Without FTSQuery, rows are ordered by
// created_at descending then id descending.
//
// Tags is an AND-filter: a memory must have every named tag to match.
//
// PromotionState narrows by lifecycle ("draft", "curated"); empty
// means any.
type SearchFilter struct {
	FTSQuery       string
	Tags           []string
	SessionID      string
	PromotionState string
	Limit          int

	// ProjectId restricts results to the named project (per ADR 0016).
	// Empty means no project filter — all projects are considered.
	ProjectId string
	// StrictProject controls handling of NULL project_id rows when
	// ProjectId is set. Zero value (false) = NULL rows are INCLUDED (the
	// ADR 0016 default; legacy memories remain findable). Setting true
	// excludes NULL rows, used by `mom recall --strict-project`.
	StrictProject bool
}

// SearchedMemory is a Memory plus the BM25 score from the matching
// FTS5 row. Score is 0 when no FTSQuery was supplied (no ranking).
//
// Lower BM25 scores are better in SQLite's bm25() function — it
// returns negative ranks where more negative = more relevant. We
// preserve that convention so callers can sort ascending; the public
// helper Sort() in finder normalises if needed.
type SearchedMemory struct {
	Memory
	Score float64
}

// SearchMemories runs the filtered query and returns matching rows. It
// is the SQL composition primitive for Finder; Finder layers
// relaxation (AND→OR) and tier escalation (curated→draft) on top.
//
// Empty/whitespace tag names in f.Tags are rejected with ErrEmptyArg
// — the previous behaviour silently produced WHERE name IN (..., ”)
// with COUNT(DISTINCT) = N+1, which always returned zero rows and
// gave callers no signal that their input was malformed.
func (l *Librarian) SearchMemories(f SearchFilter) ([]SearchedMemory, error) {
	for i, t := range f.Tags {
		if strings.TrimSpace(t) == "" {
			return nil, fmt.Errorf("SearchMemories: tags[%d] (%q): %w", i, t, ErrEmptyArg)
		}
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}

	var (
		sb      strings.Builder
		args    []any
		joins   []string
		wheres  []string
		orderBy = "m.created_at DESC, m.id DESC"
		hasFTS  = strings.TrimSpace(f.FTSQuery) != ""
	)

	if hasFTS {
		// ADR 0007: bm25(memories_fts, 0, 2, 10) — weight 0 on id (UUID),
		// 2 on summary, 10 on content_text. Lower = more relevant.
		joins = append(joins, "JOIN memories_fts fts ON fts.rowid = m.rowid")
		wheres = append(wheres, "memories_fts MATCH ?")
		args = append(args, f.FTSQuery)
		orderBy = "bm25(memories_fts, 0, 2, 10) ASC, m.id DESC"
	}

	// Tag AND-filter. Each named tag adds one INTERSECT clause via a
	// HAVING COUNT(DISTINCT) — the canonical SQL idiom that lets the
	// query plan use indexes on memory_tags(tag_id) and tags(name).
	tagAND := strings.TrimSpace(strings.Join(f.Tags, ""))
	if tagAND != "" && len(f.Tags) > 0 {
		// Filter to memories that have AT LEAST every requested tag.
		var placeholders []string
		for _, t := range f.Tags {
			placeholders = append(placeholders, "?")
			args = append(args, t)
		}
		wheres = append(wheres, fmt.Sprintf(
			`m.id IN (
				SELECT mt.memory_id FROM memory_tags mt
				  JOIN tags t ON t.id = mt.tag_id
				 WHERE t.name IN (%s)
				 GROUP BY mt.memory_id
				HAVING COUNT(DISTINCT t.name) = %d
			)`, strings.Join(placeholders, ", "), len(f.Tags),
		))
	}

	if f.SessionID != "" {
		wheres = append(wheres, "m.session_id = ?")
		args = append(args, f.SessionID)
	}
	if f.PromotionState != "" {
		wheres = append(wheres, "m.promotion_state = ?")
		args = append(args, f.PromotionState)
	}
	if f.ProjectId != "" {
		// ADR 0016: NULL project_id is "unknown / legacy". By default we
		// include those rows in scoped queries so the foundation migration
		// does not silently hide pre-existing memory. `StrictProject` flips
		// to exclude.
		if f.StrictProject {
			wheres = append(wheres, "m.project_id = ?")
			args = append(args, f.ProjectId)
		} else {
			wheres = append(wheres, "(m.project_id = ? OR m.project_id IS NULL)")
			args = append(args, f.ProjectId)
		}
	}

	sb.WriteString(`SELECT m.id, m.type, m.summary, m.content, m.created_at, m.session_id,
		m.project_id, m.provenance_actor, m.provenance_source_type, m.provenance_trigger_event,
		m.promotion_state, m.landmark, m.centrality_score`)
	if hasFTS {
		sb.WriteString(`, bm25(memories_fts, 0, 2, 10)`)
	} else {
		sb.WriteString(`, 0`)
	}
	sb.WriteString(` FROM memories m`)
	for _, j := range joins {
		sb.WriteString(" ")
		sb.WriteString(j)
	}
	if len(wheres) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(wheres, " AND "))
	}
	sb.WriteString(" ORDER BY ")
	sb.WriteString(orderBy)
	sb.WriteString(" LIMIT ?")
	args = append(args, limit)

	out := []SearchedMemory{}
	err := l.v.Query(sb.String(), args, func(rs *sql.Rows) error {
		for rs.Next() {
			var (
				sm                                                  SearchedMemory
				summary, projectId, actor, sourceType, triggerEvent sql.NullString
				createdAtStr                                        string
				landmarkInt                                         int64
			)
			if err := rs.Scan(
				&sm.ID, &sm.Type, &summary, &sm.Content, &createdAtStr, &sm.SessionID,
				&projectId, &actor, &sourceType, &triggerEvent,
				&sm.PromotionState, &landmarkInt, &sm.CentralityScore, &sm.Score,
			); err != nil {
				return err
			}
			sm.Summary = summary.String
			sm.ProjectId = projectId.String
			sm.ProvenanceActor = actor.String
			sm.ProvenanceSourceType = sourceType.String
			sm.ProvenanceTriggerEvent = triggerEvent.String
			sm.Landmark = landmarkInt != 0
			t, err := parseTime(createdAtStr)
			if err != nil {
				return fmt.Errorf("parse created_at %q: %w", createdAtStr, err)
			}
			sm.CreatedAt = t
			out = append(out, sm)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("SearchMemories: %w", err)
	}
	return out, nil
}

// RecentDrafts returns newest draft memories created within the given window.
func (l *Librarian) RecentDrafts(since time.Duration, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 50
	}
	cutoff := formatTime(l.now().Add(-since))
	out := []Memory{}
	err := l.v.Query(
		`SELECT id, type, summary, content, created_at, session_id,
		        provenance_actor, provenance_source_type, provenance_trigger_event,
		        promotion_state, landmark, centrality_score
		 FROM memories
		 WHERE promotion_state = 'draft' AND created_at >= ?
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`,
		[]any{cutoff, limit},
		func(rs *sql.Rows) error {
			for rs.Next() {
				var (
					m                                        Memory
					summary, actor, sourceType, triggerEvent sql.NullString
					createdAtStr                             string
					landmarkInt                              int64
				)
				if err := rs.Scan(
					&m.ID, &m.Type, &summary, &m.Content, &createdAtStr, &m.SessionID,
					&actor, &sourceType, &triggerEvent,
					&m.PromotionState, &landmarkInt, &m.CentralityScore,
				); err != nil {
					return err
				}
				m.Summary = summary.String
				m.ProvenanceActor = actor.String
				m.ProvenanceSourceType = sourceType.String
				m.ProvenanceTriggerEvent = triggerEvent.String
				m.Landmark = landmarkInt != 0
				t, err := parseTime(createdAtStr)
				if err != nil {
					return fmt.Errorf("parse created_at %q: %w", createdAtStr, err)
				}
				m.CreatedAt = t
				out = append(out, m)
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("RecentDrafts: %w", err)
	}
	return out, nil
}

// Landmarks returns landmark memories ordered by centrality_score descending.
func (l *Librarian) Landmarks(limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	out := []Memory{}
	err := l.v.Query(
		`SELECT id, type, summary, content, created_at, session_id,
		        provenance_actor, provenance_source_type, provenance_trigger_event,
		        promotion_state, landmark, centrality_score
		 FROM memories
		 WHERE landmark = 1
		 ORDER BY centrality_score DESC, created_at DESC, id DESC
		 LIMIT ?`,
		[]any{limit},
		func(rs *sql.Rows) error {
			for rs.Next() {
				var (
					m                                        Memory
					summary, actor, sourceType, triggerEvent sql.NullString
					createdAtStr                             string
					landmarkInt                              int64
				)
				if err := rs.Scan(
					&m.ID, &m.Type, &summary, &m.Content, &createdAtStr, &m.SessionID,
					&actor, &sourceType, &triggerEvent,
					&m.PromotionState, &landmarkInt, &m.CentralityScore,
				); err != nil {
					return err
				}
				m.Summary = summary.String
				m.ProvenanceActor = actor.String
				m.ProvenanceSourceType = sourceType.String
				m.ProvenanceTriggerEvent = triggerEvent.String
				m.Landmark = landmarkInt != 0
				t, err := parseTime(createdAtStr)
				if err != nil {
					return fmt.Errorf("parse created_at %q: %w", createdAtStr, err)
				}
				m.CreatedAt = t
				out = append(out, m)
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("Landmarks: %w", err)
	}
	return out, nil
}
