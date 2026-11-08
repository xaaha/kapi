// Package app is everything a web service gets from kapi. A service imports app and nothing from another service.
package app

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"time"
)

// Service is one feature. Its routes live under /api/<Name>/ and its browser files under /assets/<Name>/.
// The page shell loads main.js as a module and style.css when the service ships them.
type Service interface {
	Name() string
	Routes(r Router)
	Files() fs.FS
}

type Router struct {
	mux  *http.ServeMux
	name string
}

func NewRouter(mux *http.ServeMux, name string) Router { return Router{mux: mux, name: name} }

func (r Router) Get(path string, h http.HandlerFunc)  { r.mux.HandleFunc("GET /api/"+r.name+path, h) }
func (r Router) Post(path string, h http.HandlerFunc) { r.mux.HandleFunc("POST /api/"+r.name+path, h) }

// Page states: an agent is watching it, a person wrote something no agent has read, nothing is happening, or its session ended.
const (
	Live    = "live"
	Waiting = "waiting"
	Idle    = "idle"
	Ended   = "ended"
)

// Page is one page kapi knows about, wherever its file lives.
type Page struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Title   string    `json:"title"`
	Summary string    `json:"summary,omitempty"`
	Folders []string  `json:"folders,omitempty"`
	Changed time.Time `json:"changed,omitzero"`
	State   string    `json:"state"`
	Missing bool      `json:"missing,omitempty"`
}

func Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body: " + err.Error()})
		return false
	}
	return true
}

func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}
