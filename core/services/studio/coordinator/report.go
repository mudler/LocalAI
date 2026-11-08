package coordinator

import "time"

// ReportExpiry is how long a helper report may wait for delivery. A report
// older than this is dropped by PruneReports: the coordinator was away too
// long for the outcome to matter.
const ReportExpiry = 7 * 24 * time.Hour

// Report is a helper whose end the coordinator has not been told yet. The row
// is written when the helper starts and stays until the report is delivered
// (Done) or expires, so a frontend that dies after the helper ends loses
// nothing: another frontend finds the row in the store and delivers it.
type Report struct {
	// ChildID is the chat of the helper; it is the key.
	ChildID string
	// ParentChat is the chat that started the helper and gets the report. For
	// a helper of a helper it is the chat of the parent helper.
	ParentChat string
	// CoordinatorID is the agent of the coordinator whose tree the helper is in.
	CoordinatorID string
	Principal     string
	Title         string
	Created       time.Time
	// LeaseUntil is the end of the claim of the frontend that is delivering
	// the report; zero when nobody is.
	LeaseUntil time.Time
	// Done is when the report was delivered; zero while it is not.
	Done time.Time
}

func (r Report) validate() error {
	if r.ChildID == "" || r.ParentChat == "" || r.CoordinatorID == "" {
		return invalid("a report needs a child, a parent chat and a coordinator")
	}
	return nil
}
