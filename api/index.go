package handler

import (
	"net/http"

	"rapid-screening/pkg/verceladapter"
)

// Handler é o entrypoint obrigatório da Vercel Go Serverless Function (/api/index.go).
func Handler(w http.ResponseWriter, r *http.Request) {
	verceladapter.Handler(w, r)
}
