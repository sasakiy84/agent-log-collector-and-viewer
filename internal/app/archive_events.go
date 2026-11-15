package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (a *App) archiveChanges() <-chan struct{} {
	a.archiveMu.Lock()
	defer a.archiveMu.Unlock()
	if a.archiveChange == nil {
		a.archiveChange = make(chan struct{})
	}
	return a.archiveChange
}
func (a *App) notifyArchive() {
	a.archiveMu.Lock()
	defer a.archiveMu.Unlock()
	if a.archiveChange != nil {
		close(a.archiveChange)
	}
	a.archiveChange = make(chan struct{})
}

// Subscribe only while a job is running. State is pushed after committed writes;
// no browser polling or periodic list reload is needed.
func (a *App) archiveEvents(w http.ResponseWriter, r *http.Request) {
	changed := a.archiveChanges()
	job, err := a.archiveJob(r.Context(), r.PathValue("id"))
	if err != nil {
		archiveError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	controller.SetWriteDeadline(time.Time{})
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	previous := ""
	for {
		payload, err := json.Marshal(job)
		if err != nil {
			return
		}
		if string(payload) != previous {
			if _, err = fmt.Fprintf(w, "event: archive\ndata: %s\n\n", payload); err != nil {
				return
			}
			if err = controller.Flush(); err != nil {
				return
			}
			previous = string(payload)
		}
		if job.Status != "preparing" && job.Status != "deleting" && job.Status != "compacting" {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-a.ctx.Done():
			return
		case <-changed:
			changed = a.archiveChanges()
			job, err = a.archiveJob(r.Context(), job.ID)
			if err != nil {
				return
			}
		case <-heartbeat.C:
			if _, err = fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			if err = controller.Flush(); err != nil {
				return
			}
		}
	}
}
