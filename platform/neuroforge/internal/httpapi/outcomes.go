package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"neuroforge/internal/brain"
	"neuroforge/internal/core"
)

type validatedOutcomeRequest struct {
	OutcomeID      string `json:"outcome_id"`
	RunID          string `json:"run_id"`
	TicketID       int64  `json:"ticket_id"`
	Decision       string `json:"decision"` // accepted | corrected
	TicketInput    string `json:"ticket_input"`
	ProposedReply  string `json:"proposed_reply,omitempty"`
	ConfirmedReply string `json:"confirmed_reply"`
	CategoryID     int64  `json:"category_id,omitempty"`
	CategoryName   string `json:"category_name,omitempty"`
	KnowledgeID    string `json:"knowledge_id,omitempty"`
	SupersedesID   string `json:"supersedes_id,omitempty"`
	Actor          string `json:"actor"`
	Note           string `json:"note,omitempty"`
}

// integrationValidatedOutcome is deliberately narrower than /api/v1/learn.
// Only a human-confirmed or human-corrected operational outcome can enter this
// path, and trusted provenance is assigned server-side rather than accepted
// from the caller.
func (s *Server) integrationValidatedOutcome(w http.ResponseWriter, r *http.Request) {
	var q validatedOutcomeRequest
	if err := decode(r, &q); err != nil {
		s.err(w, http.StatusBadRequest, err)
		return
	}
	q.OutcomeID = strings.TrimSpace(q.OutcomeID)
	q.RunID = strings.TrimSpace(q.RunID)
	q.Decision = strings.ToLower(strings.TrimSpace(q.Decision))
	q.TicketInput = strings.TrimSpace(q.TicketInput)
	q.ProposedReply = strings.TrimSpace(q.ProposedReply)
	q.ConfirmedReply = strings.TrimSpace(q.ConfirmedReply)
	q.CategoryName = strings.TrimSpace(q.CategoryName)
	q.KnowledgeID = strings.TrimSpace(q.KnowledgeID)
	q.SupersedesID = strings.TrimSpace(q.SupersedesID)
	q.Actor = strings.TrimSpace(q.Actor)
	q.Note = strings.TrimSpace(q.Note)

	if q.OutcomeID == "" || q.RunID == "" || q.TicketID <= 0 || q.TicketInput == "" || q.ConfirmedReply == "" || q.Actor == "" {
		s.err(w, http.StatusBadRequest, errors.New("outcome_id, run_id, ticket_id, ticket_input, confirmed_reply and actor are required"))
		return
	}
	if q.Decision != "accepted" && q.Decision != "corrected" {
		s.err(w, http.StatusBadRequest, errors.New("decision must be accepted or corrected"))
		return
	}
	if q.Decision == "accepted" && q.ProposedReply == "" {
		s.err(w, http.StatusBadRequest, errors.New("accepted outcomes require proposed_reply"))
		return
	}
	if len([]rune(q.TicketInput)) > 12000 || len([]rune(q.ConfirmedReply)) > 12000 || len([]rune(q.Note)) > 4000 {
		s.err(w, http.StatusRequestEntityTooLarge, errors.New("validated outcome exceeds size limits"))
		return
	}

	source := "glpi.outcome." + q.Decision
	confidence := 0.99
	if q.Decision == "corrected" {
		confidence = 1.0
	}
	var text strings.Builder
	text.WriteString("GLPI helpdesk outcome verified by a technician.\n\nProblem:\n")
	text.WriteString(q.TicketInput)
	text.WriteString("\n\nVerified solution:\n")
	text.WriteString(q.ConfirmedReply)
	if q.CategoryName != "" || q.CategoryID > 0 {
		text.WriteString("\n\nCategory: ")
		if q.CategoryName != "" {
			text.WriteString(q.CategoryName)
		}
		if q.CategoryID > 0 {
			text.WriteString(" (#")
			text.WriteString(strconv.FormatInt(q.CategoryID, 10))
			text.WriteString(")")
		}
	}

	tags := []string{"integration:glpi", "validated:human", "outcome:" + q.Decision, "ticket:" + strconv.FormatInt(q.TicketID, 10), "run:" + q.RunID}
	if q.CategoryID > 0 {
		tags = append(tags, "category:"+strconv.FormatInt(q.CategoryID, 10))
	}
	if q.KnowledgeID != "" {
		tags = append(tags, "knowledge:"+q.KnowledgeID)
	}
	if q.SupersedesID != "" {
		tags = append(tags, "supersedes-outcome:"+q.SupersedesID)
	}

	m, err := s.brain.Learn(r.Context(), brain.LearnRequest{
		Text:       text.String(),
		Kind:       "validated_outcome",
		MemoryType: core.MemorySemantic,
		Tags:       tags,
		Salience:   1.2,
		Confidence: confidence,
		Source:     source,
		Actor:      q.Actor,
		SourceID:   q.OutcomeID,
		SourceURI:  fmt.Sprintf("glpi://Ticket/%d#run=%s", q.TicketID, q.RunID),
		Note:       q.Note,
	})
	if err != nil {
		s.err(w, http.StatusBadGateway, err)
		return
	}
	supersededMemoryID := ""
	if q.SupersedesID != "" {
		if prior, ok := s.store.MemoryByProvenanceSourceID(q.SupersedesID); ok && prior.Memory.ID != m.ID {
			if err := s.store.SupersedeMemory(prior.Memory.ID, m.ID); err != nil {
				s.err(w, http.StatusInternalServerError, fmt.Errorf("persist outcome supersession: %w", err))
				return
			}
			supersededMemoryID = prior.Memory.ID
		}
	}
	_ = s.store.AddKnowledgeEvent(core.KnowledgeEvent{
		Type: "integration.outcome_validated", MemoryID: m.ID,
		Summary:  "Human-confirmed GLPI ticket outcome learned",
		Reason:   "technician explicitly accepted or corrected the AI proposal",
		Actor:    q.Actor,
		Metadata: map[string]string{"source": source, "outcome_id": q.OutcomeID, "run_id": q.RunID, "ticket_id": strconv.FormatInt(q.TicketID, 10), "decision": q.Decision, "knowledge_id": q.KnowledgeID, "supersedes_outcome_id": q.SupersedesID},
	})
	s.json(w, http.StatusCreated, map[string]any{"memory": m, "outcome_id": q.OutcomeID, "decision": q.Decision, "source": source, "superseded_memory_id": supersededMemoryID})
}

type validatedOutcomeSearchRequest struct {
	Text          string  `json:"text"`
	K             int     `json:"k"`
	MinSimilarity float64 `json:"min_similarity,omitempty"`
}

func (s *Server) integrationValidatedOutcomeSearch(w http.ResponseWriter, r *http.Request) {
	var q validatedOutcomeSearchRequest
	if err := decode(r, &q); err != nil {
		s.err(w, http.StatusBadRequest, err)
		return
	}
	q.Text = strings.TrimSpace(q.Text)
	if q.Text == "" {
		s.err(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}
	if len([]rune(q.Text)) > 12000 {
		s.err(w, http.StatusRequestEntityTooLarge, errors.New("search text exceeds size limit"))
		return
	}
	if q.K <= 0 {
		q.K = 8
	}
	if q.K > 50 {
		q.K = 50
	}
	if q.MinSimilarity == 0 {
		q.MinSimilarity = 0.50
	}
	hits, err := s.brain.SearchByProvenanceSources(r.Context(), q.Text, q.K, q.MinSimilarity, "glpi.outcome.accepted", "glpi.outcome.corrected")
	if err != nil {
		s.err(w, http.StatusBadGateway, err)
		return
	}
	s.json(w, http.StatusOK, hits)
}
