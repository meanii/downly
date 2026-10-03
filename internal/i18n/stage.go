package i18n

import "strings"

// Stage translates a progress phrase produced by the downloader or worker
// (always English, e.g. "Downloading 45.2%", "Finalizing file") into l.
// Unknown phrases are returned unchanged.
func Stage(l Lang, text string) string {
	switch {
	case text == "":
		return ""
	case strings.HasPrefix(text, "Downloading image"), text == "Image downloaded":
		return T(l, "stage_image")
	case strings.HasPrefix(text, "Downloading"):
		return T(l, "stage_downloading")
	case strings.HasPrefix(text, "Starting"):
		return T(l, "stage_starting")
	case strings.HasPrefix(text, "Finalizing"):
		return T(l, "stage_finalizing")
	case strings.HasPrefix(text, "Trying"):
		return T(l, "stage_trying_alt")
	case text == "Extracting audio":
		return T(l, "stage_audio")
	case text == "Uploading to Telegram":
		return T(l, "stage_uploading")
	case text == "Queued (retry)":
		return T(l, "stage_retry")
	case text == "Queued (recovered)", text == "Queued (bot restarted)":
		return T(l, "stage_recovered")
	case text == "Queued":
		return T(l, "stage_queued")
	}
	return text
}

// Status translates a job status ("pending", "done", ...).
func Status(l Lang, status string) string {
	key := "status_" + status
	if _, ok := catalog[Default][key]; ok {
		return T(l, key)
	}
	return status
}
