package telegram

import (
	"fmt"
	"strings"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/i18n"
)

// jobLine renders one job for /queue and /history.
func jobLine(lang i18n.Lang, job db.Job, history bool) string {
	parts := []string{fmt.Sprintf("#%d", job.ID), i18n.Status(lang, string(job.Status)), trimURL(job.URL)}
	if l := db.ModeLabel(job); l != "" {
		parts = append(parts, l)
	}
	if history {
		if job.Platform != "" && job.Platform != "unknown" {
			parts = append(parts, job.Platform)
		}
		if job.Status == db.StatusCanceled && job.ErrorMessage != "" {
			parts = append(parts, i18n.T(lang, "canceled_by_user"))
		}
		if job.FinishedAt != nil {
			parts = append(parts, job.FinishedAt.Format("2006-01-02 15:04"))
		}
		return strings.Join(parts, " | ")
	}
	if job.Status == db.StatusPending && job.QueuePosition > 0 {
		parts = append(parts, i18n.T(lang, "position_short", job.QueuePosition))
	}
	if job.Status == db.StatusPending || job.Status == db.StatusProcessing {
		progress := i18n.Stage(lang, job.ProgressText)
		if job.ProgressPercent > 0 {
			progress = strings.TrimSpace(fmt.Sprintf("%s %d%%", progress, job.ProgressPercent))
		}
		if progress != "" {
			parts = append(parts, progress)
		}
	}
	if job.Status == db.StatusCanceled && job.ErrorMessage != "" {
		parts = append(parts, i18n.T(lang, "reason_short", i18n.T(lang, "canceled_by_user")))
	}
	return strings.Join(parts, " | ")
}

func formatUserQueue(lang i18n.Lang, jobs []db.Job) string {
	if len(jobs) == 0 {
		return i18n.T(lang, "queue_empty")
	}
	lines := []string{i18n.T(lang, "queue_title")}
	for _, job := range jobs {
		lines = append(lines, jobLine(lang, job, false))
	}
	return strings.Join(lines, "\n")
}

func formatUserHistory(lang i18n.Lang, jobs []db.Job) string {
	if len(jobs) == 0 {
		return i18n.T(lang, "history_empty")
	}
	lines := []string{i18n.T(lang, "history_title")}
	for _, job := range jobs {
		lines = append(lines, jobLine(lang, job, true))
	}
	return strings.Join(lines, "\n")
}
