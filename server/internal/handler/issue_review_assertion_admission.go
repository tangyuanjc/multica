package handler

import (
	"fmt"
	"net/http"

	"github.com/multica-ai/multica/server/internal/issueguard"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) admitExistingIssueToReview(
	w http.ResponseWriter,
	r *http.Request,
	issue db.Issue,
	title string,
	description string,
) bool {
	result := issueguard.CheckReviewAssertionAdmission(issueguard.ReviewAssertionAdmissionInput{
		Identifier: fmt.Sprintf(
			"%s-%d",
			h.getIssuePrefix(r.Context(), issue.WorkspaceID),
			issue.Number,
		),
		Title:       title,
		Description: description,
		CreatedAt:   issue.CreatedAt.Time,
	})
	if result.Allowed {
		return true
	}

	writeError(w, http.StatusBadRequest, result.Message)
	return false
}

func admitNewIssueToReview(
	w http.ResponseWriter,
	title string,
	description string,
) bool {
	result := issueguard.CheckReviewAssertionAdmission(issueguard.ReviewAssertionAdmissionInput{
		Title:       title,
		Description: description,
	})
	if result.Allowed {
		return true
	}

	writeError(w, http.StatusBadRequest, result.Message)
	return false
}
