// Package lifecycle реализует runtime contract процесса controlplane
// (docs/cloud-native-runtime.md): health state, технический порт,
// обработку SIGTERM, readiness-propagation delay и drain. Бизнес-код от него
// не зависит: он получает только context.Context.
package lifecycle

import (
	"net/http"
	"sync/atomic"
)

const (
	reasonInitializing = "initializing"
	reasonShuttingDown = "shutting_down"
)

// Health хранит уже вычисленное состояние экземпляра; handlers только читают его
// и никогда не обращаются к зависимостям.
type Health struct {
	started      atomic.Bool
	shuttingDown atomic.Bool
}

func (h *Health) markStarted()      { h.started.Store(true) }
func (h *Health) markShuttingDown() { h.shuttingDown.Store(true) }

// Ready сообщает, может ли экземпляр принимать новую работу.
func (h *Health) Ready() bool { return h.started.Load() && !h.shuttingDown.Load() }

// Handler обслуживает /health/startup, /health/live и /health/ready.
func (h *Health) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/startup", func(w http.ResponseWriter, _ *http.Request) {
		write(w, h.started.Load(), reasonInitializing)
	})
	// Liveness не зависит от внешних систем и остаётся успешной во время shutdown.
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		write(w, true, "")
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		switch {
		case h.shuttingDown.Load():
			write(w, false, reasonShuttingDown)
		default:
			write(w, h.started.Load(), reasonInitializing)
		}
	})
	return mux
}

func write(w http.ResponseWriter, ok bool, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if ok {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"status":"unavailable","reason":"` + reason + `"}`))
}
