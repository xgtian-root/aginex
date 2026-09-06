package app

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	frameworkaudit "github.com/xgtian-root/aginex/server/framework/audit"
	"github.com/xgtian-root/aginex/server/framework/httpx"
	frameworkjobs "github.com/xgtian-root/aginex/server/framework/jobs"
	"gorm.io/gorm"
)

func (a *App) listJobs(c *gin.Context) {
	request, err := jobListRequest(c)
	if err != nil {
		writeProblem(
			c,
			http.StatusBadRequest,
			"Invalid job filters",
			"Use a supported state, handler type, and pagination values.",
		)
		return
	}
	if a.jobInspector == nil {
		httpx.WriteProblem(
			c,
			http.StatusServiceUnavailable,
			"JOBS_UNAVAILABLE",
			"Job administration unavailable",
			"The durable job queue is not configured.",
		)
		return
	}
	result, err := a.jobInspector.List(c.Request.Context(), request)
	if err != nil {
		if errors.Is(err, frameworkjobs.ErrInvalid) {
			writeProblem(
				c,
				http.StatusBadRequest,
				"Invalid job filters",
				"Use a supported state, handler type, and pagination values.",
			)
			return
		}
		logRequestFailure(c, "list_jobs", err)
		writeProblem(
			c,
			http.StatusInternalServerError,
			"Jobs unavailable",
			"The durable jobs could not be read.",
		)
		return
	}
	items := make([]JobResponse, 0, len(result.Jobs))
	for _, job := range result.Jobs {
		items = append(items, jobResponse(job))
	}
	c.JSON(http.StatusOK, Page[JobResponse]{
		Items:    items,
		Page:     result.Page,
		PageSize: result.PageSize,
		Total:    result.Total,
	})
}

func (a *App) retryDeadJob(c *gin.Context) {
	id, err := frameworkjobs.NormalizeJobID(c.Param("id"))
	if err != nil {
		writeProblem(
			c,
			http.StatusBadRequest,
			"Invalid job identifier",
			"The job identifier must be a UUID.",
		)
		return
	}
	if a.jobs == nil {
		httpx.WriteProblem(
			c,
			http.StatusServiceUnavailable,
			"JOBS_UNAVAILABLE",
			"Job administration unavailable",
			"The durable job queue is not configured.",
		)
		return
	}

	principal := currentPrincipal(c)
	response := JobRetryResponse{
		ID:    id,
		State: string(frameworkjobs.StatePending),
	}
	err = a.writes.Run(
		c.Request.Context(),
		func(tx *gorm.DB) (frameworkaudit.Event, error) {
			queue, err := a.jobs.Bind(tx)
			if err != nil {
				return frameworkaudit.Event{}, err
			}
			if err := queue.RetryDead(c.Request.Context(), id); err != nil {
				return frameworkaudit.Event{}, err
			}
			if err := a.completeIdempotentWrite(
				c,
				tx,
				http.StatusAccepted,
				response,
				nil,
			); err != nil {
				return frameworkaudit.Event{}, err
			}
			return successfulAuditEvent(
				c,
				&principal.User.ID,
				"jobs:retry",
				"job",
				id,
				"Retried a dead durable job",
				map[string]any{"state": frameworkjobs.StateDead},
				map[string]any{"state": frameworkjobs.StatePending},
			), nil
		},
	)
	switch {
	case err == nil:
		c.JSON(http.StatusAccepted, response)
	case errors.Is(err, frameworkjobs.ErrNotFound):
		writeProblem(
			c,
			http.StatusNotFound,
			"Job not found",
			"No durable job matches this identifier.",
		)
	case errors.Is(err, frameworkjobs.ErrInvalidTransition):
		writeProblem(
			c,
			http.StatusConflict,
			"Job cannot be retried",
			"Only jobs in the dead state can be retried.",
		)
	case errors.Is(err, frameworkjobs.ErrInvalid):
		writeProblem(
			c,
			http.StatusBadRequest,
			"Invalid job identifier",
			"The job identifier is invalid.",
		)
	default:
		writeProblem(
			c,
			http.StatusInternalServerError,
			"Job could not be retried",
			"The durable job state could not be changed.",
		)
	}
}

func jobListRequest(c *gin.Context) (frameworkjobs.ListRequest, error) {
	page, err := jobQueryInt(c, "page", 1)
	if err != nil {
		return frameworkjobs.ListRequest{}, err
	}
	pageSize, err := jobQueryInt(c, "pageSize", 20)
	if err != nil {
		return frameworkjobs.ListRequest{}, err
	}
	return frameworkjobs.NormalizeList(frameworkjobs.ListRequest{
		State:    frameworkjobs.State(strings.TrimSpace(c.Query("state"))),
		Type:     c.Query("type"),
		Page:     page,
		PageSize: pageSize,
	})
}

func jobQueryInt(c *gin.Context, name string, fallback int) (int, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid %s", frameworkjobs.ErrInvalid, name)
	}
	return parsed, nil
}

func jobResponse(job frameworkjobs.Job) JobResponse {
	return JobResponse{
		ID:            job.ID,
		Type:          job.Type,
		Version:       job.Version,
		State:         string(job.State),
		ScheduledAt:   job.ScheduledAt,
		Attempts:      job.Attempts,
		MaxAttempts:   job.MaxAttempts,
		LockedBy:      job.LockedBy,
		LockedAt:      job.LockedAt,
		HeartbeatAt:   job.HeartbeatAt,
		HasError:      strings.TrimSpace(job.LastError) != "",
		CreatedByKind: string(job.CreatedBy.Kind),
		CreatedByID:   job.CreatedBy.ID,
		RequestID:     job.Trace.RequestID,
		CreatedAt:     job.CreatedAt,
		UpdatedAt:     job.UpdatedAt,
		CompletedAt:   job.CompletedAt,
	}
}
