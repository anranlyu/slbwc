package membership

import "net/http"

// RegisterHandlers attaches membership endpoints.
func RegisterHandlers(mux *http.ServeMux, manager *Manager) {
	mux.HandleFunc("/internal/membership/gossip", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		manager.HandleGossip(w, r)
	})

	mux.HandleFunc("/internal/membership/ping", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		manager.HandlePing(w, r)
	})
}

