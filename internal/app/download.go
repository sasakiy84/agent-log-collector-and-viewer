package app

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// Large exports can outlive the server's ordinary request write timeout.
// Cancellation still propagates through the request context and failed writes.
func prepareDownload(w http.ResponseWriter, contentType, filename string) {
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Accel-Buffering", "no")
}

// Once bytes have been sent, an HTTP error or a closing JSON/ZIP delimiter
// would turn an interrupted download into a seemingly successful partial file.
func abortDownload(err error) {
	log.Printf("Session download interrupted: %v", err)
	panic(http.ErrAbortHandler)
}
