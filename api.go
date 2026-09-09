package main

import (
	"encoding/json"
	"net/http"
)

func checkHandler(store Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		owner := r.URL.Query().Get("owner")
		repo := r.URL.Query().Get("repo")
		if owner == "" || repo == "" {
			writeAPIError(w, http.StatusBadRequest, "use /api/check?owner=o&repo=r")
			return
		}
		installed := false
		if store != nil {
			if sub, err := store.Get(r.Context(), owner, repo); err == nil && sub.InstallationID != 0 {
				installed = true
			}
		}
		writeAPIJSON(w, http.StatusOK, map[string]any{
			"owner": owner, "repo": repo, "installed": installed,
		})
	})
}

func writeAPIJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	writeAPIJSON(w, status, map[string]string{"error": msg})
}
