package app

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// Large exports can outlive the server's ordinary request write timeout.
// Cancellation still propagates through the request context and failed writes.
func prepareDownload(w http.ResponseWriter, contentType, filename string) http.ResponseWriter {
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(downloadIdleTimeout))
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Accel-Buffering", "no")
	return &downloadResponse{ResponseWriter: w}
}

const (
	downloadIdleTimeout  = 30 * time.Second
	maxConcurrentExports = 2
)

type downloadResponse struct{ http.ResponseWriter }

func (w *downloadResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *downloadResponse) Write(p []byte) (int, error) {
	// Bound a blocked client write without limiting the total export duration.
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(downloadIdleTimeout))
	return w.ResponseWriter.Write(p)
}

func (a *App) acquireExport(w http.ResponseWriter) bool {
	select {
	case a.exportSlots <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "Two exports are already running; retry when a download finishes", http.StatusServiceUnavailable)
		return false
	}
}

// Once bytes have been sent, an HTTP error or a closing JSON/ZIP delimiter
// would turn an interrupted download into a seemingly successful partial file.
func abortDownload(err error) {
	log.Printf("Session download interrupted: %v", err)
	panic(http.ErrAbortHandler)
}
