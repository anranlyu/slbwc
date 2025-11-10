package koorde

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"slbwc/internal/overlay"
)

// RegisterHandlers wires the Koorde HTTP handlers into mux.
func RegisterHandlers(mux *http.ServeMux, node *Node) {
	mux.HandleFunc(internalBasePath+"/find_successor", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID overlay.Identifier `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		nodeResp, hops, err := node.FindSuccessor(ctx, req.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Node overlay.RemoteNode `json:"node"`
			Hops int                `json:"hops"`
		}{Node: nodeResp, Hops: hops})
	})

	mux.HandleFunc(internalBasePath+"/successor", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		nodeResp := node.Successor()
		_ = json.NewEncoder(w).Encode(struct {
			Node overlay.RemoteNode `json:"node"`
		}{Node: nodeResp})
	})

	mux.HandleFunc(internalBasePath+"/predecessor", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		pred := node.Predecessor()
		if pred.IsZero() {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Node overlay.RemoteNode `json:"node"`
		}{Node: pred})
	})

	mux.HandleFunc(internalBasePath+"/notify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var payload struct {
			Node overlay.RemoteNode `json:"node"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		node.Notify(payload.Node)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc(internalBasePath+"/ping", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc(internalBasePath+"/successors", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		list := node.SuccessorList()
		_ = json.NewEncoder(w).Encode(struct {
			Nodes []overlay.RemoteNode `json:"nodes"`
		}{Nodes: list})
	})
}
