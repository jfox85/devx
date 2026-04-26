package web

import (
	"encoding/json"
	"net/http"

	artifactpkg "github.com/jfox85/devx/artifact"
)

type artifactNotifyEvent struct {
	Session    string `json:"session"`
	ArtifactID string `json:"artifact_id"`
	Title      string `json:"title"`
	Focus      bool   `json:"focus"`
}

func (s *Server) handleArtifactNotify(w http.ResponseWriter, r *http.Request) {
	sess, ok := artifactSessionFromRequest(w, r)
	if !ok {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		id = artifactpkg.FocusedID(sess)
	}
	manifest, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	a, _ := artifactpkg.Find(manifest, id)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
		return
	}
	payload, _ := json.Marshal(artifactNotifyEvent{Session: sess.Name, ArtifactID: a.ID, Title: a.Title, Focus: a.Focus})
	s.hub.broadcastEvent("artifact", string(payload))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
